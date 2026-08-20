# Replace Postgres with a `StreamMonitor` Kubernetes CRD

This ExecPlan is a living document. The sections `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective` must be kept up to date as work proceeds. This plan must be maintained in accordance with `.agent/PLANS.md` at the repository root.

This is plan 1 of 2. Plan 2, `docs/coding-agent/plans/02-streammonitor-scheduled-reservations.md`, adds reservation-based automatic start/stop on top of the CRD this plan introduces, and must not begin until this plan is fully complete and validated.

## Purpose / Big Picture

Today, every "monitor" (one YouTube livestream being watched for health problems) is a row in a Postgres database that only the Gateway process (the API server, `cmd/gateway`) can read or write. After this change, there is no database anywhere in the system: every monitor is a Kubernetes Custom Resource (a `StreamMonitor` object — the same kind of thing as a `Pod` or a `Deployment`, but one this project defines itself), and the only other state is what already lives in worker Pod memory today (unchanged). You will be able to run `kubectl get streammonitors` and see every monitor's identity and live status directly from Kubernetes, with no separate database to inspect, back up, or operate. The public HTTP API (`POST/GET/PATCH/DELETE /api/v1/monitors`) behaves exactly as it does today, verified end-to-end against a real local Kubernetes cluster.

## Progress

- [ ] Not started.

## Surprises & Discoveries

(none yet — update this section as implementation proceeds)

## Decision Log

- Decision: Use `k8s.io/client-go/dynamic` + `unstructured.Unstructured` for all CRD access, not a generated typed clientset (no `client-gen`/`controller-gen`/`controller-runtime`).
  Rationale: the repository already manages Kubernetes objects with plain `client-go` (see `internal/k8s/k8s.go`), not `controller-runtime`. The dynamic client needs zero scheme registration and zero generated code to talk to a CRD; `k8s.io/apimachinery`'s `runtime.DefaultUnstructuredConverter` (already a transitive dependency, no new module) handles JSON-tag-driven conversion to/from hand-written Go structs. This keeps the new code in the same style as the code around it and avoids introducing a code-generation step this repository has never had.
  Date/Author: plan authored 2026-08-21.

- Decision: `DeepCopyObject()` for the hand-written CRD Go types is implemented via a JSON marshal/unmarshal round-trip, not a field-by-field copy, and no `deepcopy-gen` is run.
  Rationale: these objects are created/updated at most a few dozen times per minute (bounded by the existing 25/min API rate limit), so the CPU cost of a JSON round-trip is irrelevant, and it is impossible to forget a field when the struct grows (as it will in plan 2, with `ScheduledEndTime`) since the same JSON tags already used for the wire format also drive the copy.
  Date/Author: plan authored 2026-08-21.

- Decision: The `monitor_events` audit log (Postgres table, `MonitorEvent` Go type, `GET /api/v1/monitors/:id/events`, `POST /internal/v1/monitors/:id/events`) is deleted outright, not migrated into the CRD or any other store.
  Rationale: confirmed with the user that this history is not used by any consumer today. Webhook delivery itself is unaffected (the existing `internal/webhook.Sender` still sends webhooks); only the practice of writing a permanent record of each delivery attempt is removed, replaced by a local `zap` log line.
  Date/Author: user decision, recorded 2026-08-21.

- Decision: Periodic deletion of stale/terminal monitors (`runCleanupLoop`, `DeleteStaleMonitors`, `MonitorRetentionPeriod`/`CleanupInterval` config) is removed rather than reimplemented against the CRD store.
  Rationale: unlike a growing Postgres table, a handful of leftover terminal-phase `StreamMonitor` objects in etcd cost nothing meaningful at rest — there is no table-bloat or query-slowdown concern to guard against. Removing it is simpler than reimplementing it, and an operator can still run `kubectl delete streammonitor -l ...` manually or add a `CronJob` later if this turns out to be needed in practice.
  Date/Author: plan authored 2026-08-21, flagged as a judgment call rather than an explicit user instruction — revisit if stale `StreamMonitor` accumulation becomes a real operational nuisance.

- Decision: The worker Pod's `ownerReferences` now point at the owning `StreamMonitor` custom resource instead of the Gateway's `Deployment`.
  Rationale: now that a "monitor" is itself a real Kubernetes object with a stable UID, Kubernetes' built-in garbage collector can delete a monitor's Pod automatically when its `StreamMonitor` is deleted, as a backstop alongside the existing explicit `DeleteWorkerPod` call. This also deletes now-pointless code (`ResolveOwnerDeployment`, `SetOwnerReference`, the `Deployment`/`ReplicaSet` RBAC rules) that existed solely to resolve the Gateway's own Deployment identity.
  Date/Author: plan authored 2026-08-21.

- Decision: Duplicate-active-`stream_url` prevention and active-monitor counting are served from an in-process `SharedIndexInformer` cache (eventually consistent, typically sub-second staleness) instead of a live, strongly-consistent read (as Postgres's unique index and `SELECT COUNT` provided).
  Rationale: CRDs have no equivalent of a SQL unique index or a transactional `COUNT`. An informer cache keeps `POST /api/v1/monitors` fast (no live API-server round trip on every call) and keeps behavior correct in the overwhelming majority of cases; a race window exists only under near-simultaneous concurrent creates for the *same* `stream_url`, which is inherently rare and now further bounded by the existing 25/min rate limit.
  Date/Author: plan authored 2026-08-21, accepted limitation — do not "fix" this with a live API read without discussing the performance trade-off first.

- Decision: The CRD's `status.phase` enum includes `"scheduled"` starting now, even though nothing in this plan produces that phase (plan 2 does).
  Rationale: Helm's `crds/` directory is only applied on `helm install`, never on `helm upgrade` — changing the CRD schema later requires a manual `kubectl apply` by the operator. Shipping the full enum now avoids a second such manual step when plan 2 ships.
  Date/Author: plan authored 2026-08-21.

## Context and Orientation

This is a Go 1.24 monorepo (module `github.com/xpadev-net/youtube-stream-tracker`) with two binaries:

- `cmd/gateway/main.go` — a stateless-ish REST API (using the `gin` web framework) that lets external callers create/list/get/patch/delete "monitors," and that creates/deletes Kubernetes Pods to actually do the monitoring work. "The Gateway" below always refers to this process.
- `cmd/worker/main.go` — one Pod per monitor, launched by the Gateway, which repeatedly shells out to `yt-dlp` (a command-line YouTube downloader) to check whether a specific YouTube stream is live, downloads video/audio segments while it is live, and uses `ffmpeg` to detect "blackout" (frozen/black video) or "silence" (dead audio) problems. It reports its status back to the Gateway over plain HTTP calls (not by talking to Kubernetes or a database directly) and sends "webhooks" (HTTP POST notifications) to a caller-supplied URL when something interesting happens (stream started late, went black, went silent, ended). "The worker" below always refers to this process; there is one worker Pod per active monitor.

A "monitor" is the system's central concept: one YouTube livestream being watched. Today a monitor is a row in a Postgres table (`monitors`, plus a 1:1 `monitor_stats` row and a list of `monitor_events` audit rows) defined in `internal/db/migrations/001_initial_schema.sql` and read/written exclusively by the Gateway process through `internal/db/monitor_repository.go`. The worker never touches Postgres — it only ever talks to the Gateway's own internal HTTP API (`internal/worker/callback.go`), a detail that stays true after this change and significantly limits its blast radius.

The Go types for a monitor live in `internal/db/models.go`: `MonitorStatus` (an enum: `initializing`, `waiting`, `monitoring`, `completed`, `stopped`, `error` — with an `IsActive()` helper that returns true for the first three), `StreamStatus` (`unknown`, `scheduled`, `live`, `ended`), `HealthStatus` (`ok`, `warning`, `error`, `unknown`), `MonitorConfig` (check interval, blackout/silence thresholds, an optional `ScheduledStartTime`, a delay-tolerance setting), `Monitor` (identity + config + current status + which Pod is running it), `MonitorStats` (live counters: segments analyzed, blackout/silence event counts, last-check time, video/audio health, current `StreamStatus`), and `MonitorEvent` (an audit-log row — this type is being deleted, not migrated, see Decision Log).

The HTTP surface lives in `internal/api/handlers.go`, wired up in `cmd/gateway/main.go`:

- `POST /api/v1/monitors` (`CreateMonitor`) — validate a YouTube "watch" URL and a callback URL, check the count of currently-active monitors against a configured maximum (`MAX_MONITORS`, default 50), insert a database row, then synchronously call `k8s.Reconciler.CreateMonitorPod` (`internal/k8s/reconcile.go`) which calls `k8s.Client.CreateWorkerPod` (`internal/k8s/k8s.go`) to create a Kubernetes Pod running the worker image, passing the monitor's ID/URL/config to the Pod via environment variables.
- `GET /api/v1/monitors/:id`, `GET /api/v1/monitors` (list, paginated), `PATCH /api/v1/monitors/:id` (update callback URL / config on an active monitor), `DELETE /api/v1/monitors/:id` (delete the database row, then best-effort delete the Pod).
- Internal-only endpoints, authenticated with a separate key, called by the worker Pod itself: `PUT /internal/v1/monitors/:id/status` (the worker reports its current status/health/counters roughly once per `CheckIntervalSec`, default every 10 seconds), `POST /internal/v1/monitors/:id/terminate`, and `POST /internal/v1/monitors/:id/events` (the audit-log write — being deleted).

Two background loops run inside the Gateway process (started from `cmd/gateway/main.go`): `internal/k8s/watcher.go`'s `PodWatcher.Run`, which watches Kubernetes for Pods that failed or finished and updates the corresponding monitor's status plus sends a failure webhook; and `internal/k8s/reconcile.go`'s `Reconciler.RunPeriodic`, which periodically compares "which monitors does the database say are active" against "which Pods actually exist" to fix three kinds of drift: a monitor marked active with no Pod (mark it `error`), a Pod that exists for a monitor already in a terminal state (delete the "zombie" Pod), and a Pod with no corresponding monitor at all (delete the "orphaned" Pod). Both of these use a compare-and-swap style update, `UpdateStatusWithCondition(ctx, id, expectedCurrentStatus, newStatus) (updated bool, err error)`, so that if the watcher and the reconciler both notice the same dead Pod at nearly the same time, only one of them actually acts — the other sees `updated=false` and does nothing (not an error, not something to retry).

Kubernetes access today uses the plain `k8s.io/client-go` library directly (no `controller-runtime`, no code generation) — see `internal/k8s/k8s.go`'s `Client` struct, which wraps a `*kubernetes.Clientset` and has methods like `CreateWorkerPod`, `DeleteWorkerPod`, `ListWorkerPods`, `WatchWorkerPods`. `go.mod` already requires `k8s.io/api`, `k8s.io/apimachinery`, and `k8s.io/client-go` at v0.32.0 — this plan adds no new Go modules for its Kubernetes access (the `dynamic` client and `SharedIndexInformer` cache used below are both already part of the `k8s.io/client-go` module that is already required).

Deployment is via a Helm chart at `helm/stream-monitor/` (`templates/deployment.yaml` — the Gateway `Deployment`, there is no worker `Deployment`, worker Pods are created dynamically — `templates/rbac.yaml`, `templates/service.yaml`, `templates/serviceaccount.yaml`, `templates/configmap.yaml`, `templates/secret.yaml`, and `values.yaml`). "Helm chart" means a packaged, templated set of Kubernetes YAML manifests installed with the `helm` command-line tool. `docker-compose.yaml` at the repository root runs a local Postgres for development via `docker compose up`. Rate limiting (an unrelated concern, left entirely alone by this plan) is an in-memory per-API-key limiter in `internal/httpapi/middleware.go`, applied to `CreateMonitor`/`PatchMonitor` at 25 requests per minute.

"CRD" (Custom Resource Definition) means teaching a Kubernetes cluster about a new kind of object, the same way it already knows about `Pod` or `Deployment`. Once installed, you can `kubectl get`/`create`/`delete` objects of that new kind just like any built-in one, and — critically for this plan — Kubernetes itself becomes the durable store for those objects (backed by its own database, `etcd`, which this application never talks to directly). This plan defines one new kind, `StreamMonitor`.

## Plan of Work

**Goal at the end of this plan:** there is no Postgres anywhere in this codebase (no `internal/db` package, no `pgx` dependency, no `docker-compose.yaml` Postgres service, no `DB_DSN`/`DATABASE_URL` config). Every monitor is a `StreamMonitor` Kubernetes custom resource. All existing user-facing behavior (create/list/get/patch/delete a monitor; the worker reporting status; failure detection; reconciliation of drift between "what should be running" and "what is running") works exactly as it does today, verified against a real local Kubernetes cluster. The only behavior intentionally removed is the audit-log endpoints (`GET .../events`, `POST .../events`) and the periodic stale-monitor cleanup loop, per the Decision Log above.

**1. Define the CRD.**

Create `helm/stream-monitor/crds/streammonitor-crd.yaml`. Helm's `crds/` directory (distinct from `templates/`) is special: its contents are installed once, automatically, the first time someone runs `helm install`, are never templated (no `{{ }}` placeholders are evaluated inside it), and — this is the important operational caveat to remember for the rest of this project's life — are **never** touched by `helm upgrade`. If this CRD's schema needs to change after the chart has been installed once, whoever operates the cluster must separately run `kubectl apply -f helm/stream-monitor/crds/streammonitor-crd.yaml` after upgrading the chart. Write this manifest:

    apiVersion: apiextensions.k8s.io/v1
    kind: CustomResourceDefinition
    metadata:
      name: streammonitors.streamtracker.xpadev.net
    spec:
      group: streamtracker.xpadev.net
      names:
        kind: StreamMonitor
        listKind: StreamMonitorList
        plural: streammonitors
        singular: streammonitor
        shortNames: ["smon"]
      scope: Namespaced
      versions:
        - name: v1alpha1
          served: true
          storage: true
          subresources:
            status: {}
          additionalPrinterColumns:
            - name: Phase
              type: string
              jsonPath: .status.phase
            - name: Stream Status
              type: string
              jsonPath: .status.streamStatus
            - name: Pod
              type: string
              jsonPath: .status.podName
            - name: Video
              type: string
              jsonPath: .status.videoHealth
            - name: Audio
              type: string
              jsonPath: .status.audioHealth
            - name: Age
              type: date
              jsonPath: .metadata.creationTimestamp
          schema:
            openAPIV3Schema:
              type: object
              required: ["spec"]
              properties:
                spec:
                  type: object
                  required: ["streamURL", "callbackURL"]
                  properties:
                    streamURL: {type: string}
                    callbackURL: {type: string}
                    checkIntervalSec: {type: integer, minimum: 1}
                    blackoutThresholdSec: {type: integer, minimum: 0}
                    silenceThresholdSec: {type: integer, minimum: 0}
                    silenceDBThreshold: {type: number, maximum: 0}
                    scheduledStartTime: {type: string, format: date-time}
                    scheduledEndTime: {type: string, format: date-time}
                    startDelayToleranceSec: {type: integer, minimum: 0}
                    metadata: {type: object, x-kubernetes-preserve-unknown-fields: true}
                status:
                  type: object
                  properties:
                    phase:
                      type: string
                      enum: ["initializing", "waiting", "monitoring", "scheduled", "completed", "stopped", "error"]
                    podName: {type: string}
                    streamStatus:
                      type: string
                      enum: ["unknown", "scheduled", "live", "ended"]
                    videoHealth:
                      type: string
                      enum: ["ok", "warning", "error", "unknown"]
                    audioHealth:
                      type: string
                      enum: ["ok", "warning", "error", "unknown"]
                    totalSegments: {type: integer}
                    blackoutEvents: {type: integer}
                    silenceEvents: {type: integer}
                    lastCheckAt: {type: string, format: date-time}

Note the `subresources: {status: {}}` block: this splits a `StreamMonitor` object's writable surface into `spec` (identity/configuration — what the API's create/patch handlers write) and `status` (live state — what the worker's frequent status callbacks write), each with independent optimistic-concurrency tracking. This means the API's `PATCH /api/v1/monitors/:id` handler and the worker's once-every-10-seconds status report can never clobber each other's write.

Also note `"scheduled"` already appears in the `phase` enum — nothing sets that value until plan 2, but see the Decision Log entry above for why it ships now.

**2. Define the pure domain types in a new dependency-free package.**

Create `internal/model/model.go`. Move (do not just copy — this replaces `internal/db/models.go`, which is deleted at the end of this plan) everything currently in `internal/db/models.go` **except** `MonitorEvent` and `WebhookStatus`/its constants (both deleted, not moved — see Decision Log). Change the package name to `model`. Everything else — `MonitorStatus` and its constants and `IsActive()`, `HealthStatus`, `StreamStatus`, `MonitorConfig` and its `Validate()`, `DefaultMonitorConfig()`, `Monitor`, `MonitorStats`, `MonitorWithStats` — keeps the same field names, JSON tags, and behavior. Add one new field to `Monitor`: `UID types.UID` (import `"k8s.io/apimachinery/pkg/types"`) — populated from the underlying `StreamMonitor` object's `metadata.uid`, needed later so a worker Pod's `ownerReferences` can point at the exact CR that owns it (Kubernetes owner references require a UID, not just a name).

**3. Hand-write the CRD's Go types.**

Create `internal/k8s/apis/streamtracker/v1alpha1/types.go`. Because this plan deliberately avoids `controller-gen`/`deepcopy-gen` (see Decision Log — this repository has never used code generation and this plan keeps it that way), the small amount of boilerplate those tools would normally generate is written by hand instead:

    package v1alpha1

    import (
    	"encoding/json"

    	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    	"k8s.io/apimachinery/pkg/runtime"
    	"k8s.io/apimachinery/pkg/runtime/schema"

    	"github.com/xpadev-net/youtube-stream-tracker/internal/model"
    )

    const (
    	GroupName = "streamtracker.xpadev.net"
    	Version   = "v1alpha1"
    	Kind      = "StreamMonitor"
    	Plural    = "streammonitors"
    )

    var SchemeGroupVersion = schema.GroupVersion{Group: GroupName, Version: Version}

    var GVR = SchemeGroupVersion.WithResource(Plural)

    type StreamMonitorSpec struct {
    	StreamURL              string                `json:"streamURL"`
    	CallbackURL            string                `json:"callbackURL"`
    	CheckIntervalSec       int                   `json:"checkIntervalSec"`
    	BlackoutThresholdSec   int                   `json:"blackoutThresholdSec"`
    	SilenceThresholdSec    int                   `json:"silenceThresholdSec"`
    	SilenceDBThreshold     float64               `json:"silenceDBThreshold"`
    	ScheduledStartTime     *metav1.Time          `json:"scheduledStartTime,omitempty"`
    	ScheduledEndTime       *metav1.Time          `json:"scheduledEndTime,omitempty"`
    	StartDelayToleranceSec int                   `json:"startDelayToleranceSec"`
    	Metadata               *runtime.RawExtension `json:"metadata,omitempty"`
    }

    type StreamMonitorStatus struct {
    	Phase          model.MonitorStatus `json:"phase,omitempty"`
    	PodName        string              `json:"podName,omitempty"`
    	StreamStatus   model.StreamStatus  `json:"streamStatus,omitempty"`
    	VideoHealth    model.HealthStatus  `json:"videoHealth,omitempty"`
    	AudioHealth    model.HealthStatus  `json:"audioHealth,omitempty"`
    	TotalSegments  int                 `json:"totalSegments,omitempty"`
    	BlackoutEvents int                 `json:"blackoutEvents,omitempty"`
    	SilenceEvents  int                 `json:"silenceEvents,omitempty"`
    	LastCheckAt    *metav1.Time        `json:"lastCheckAt,omitempty"`
    }

    type StreamMonitor struct {
    	metav1.TypeMeta   `json:",inline"`
    	metav1.ObjectMeta `json:"metadata,omitempty"`
    	Spec              StreamMonitorSpec   `json:"spec"`
    	Status            StreamMonitorStatus `json:"status,omitempty"`
    }

    type StreamMonitorList struct {
    	metav1.TypeMeta `json:",inline"`
    	metav1.ListMeta `json:"metadata,omitempty"`
    	Items           []StreamMonitor `json:"items"`
    }

    // DeepCopyObject implements runtime.Object via a JSON round-trip rather
    // than a hand-maintained field-by-field copy — see this plan's Decision
    // Log for why. It is required so *StreamMonitor and *StreamMonitorList
    // satisfy the runtime.Object interface (TypeMeta already provides
    // GetObjectKind/SetGroupVersionKind).
    func (m *StreamMonitor) DeepCopyObject() runtime.Object {
    	out := &StreamMonitor{}
    	b, _ := json.Marshal(m)
    	_ = json.Unmarshal(b, out)
    	return out
    }

    func (l *StreamMonitorList) DeepCopyObject() runtime.Object {
    	out := &StreamMonitorList{}
    	b, _ := json.Marshal(l)
    	_ = json.Unmarshal(b, out)
    	return out
    }

No `runtime.Scheme` registration is needed anywhere: because all CRD access goes through the *dynamic* client (`unstructured.Unstructured`, next step), these typed structs exist purely as a well-documented intermediate representation for converting to/from `unstructured.Unstructured` — they are never passed to a codec or a typed REST client.

**4. Build the informer-backed store that replaces `*db.MonitorRepository`.**

Create package `internal/k8s/store`. This is the biggest new piece of code in this plan and is what lets `internal/api/handlers.go`, `internal/k8s/reconcile.go`, and `internal/k8s/watcher.go` change only mechanically (swap the type they hold, adjust error handling) instead of being rewritten, because it exposes almost the same method set `*db.MonitorRepository` did.

Create `internal/k8s/store/store.go`:

    package store

    import (
    	"context"
    	"crypto/sha256"
    	"encoding/hex"
    	"errors"

    	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
    	"k8s.io/client-go/dynamic"
    	"k8s.io/client-go/tools/cache"

    	"github.com/xpadev-net/youtube-stream-tracker/internal/model"
    )

    var (
    	ErrMonitorNotFound  = errors.New("monitor not found")
    	ErrDuplicateMonitor = errors.New("duplicate monitor for stream URL")
    	ErrMonitorNotActive = errors.New("monitor is not in an active state")
    )

    const LabelStreamURLHash = "streamtracker.xpadev.net/stream-url-hash"

    type Store struct {
    	dyn       dynamic.Interface
    	namespace string
    	informer  cache.SharedIndexInformer
    }

    func NewStore(dyn dynamic.Interface, namespace string) *Store { ... }

    // Run starts the informer's list-then-watch loop; blocks until ctx is done.
    // Must be started (in a goroutine) before the Store is used for reads.
    func (s *Store) Run(ctx context.Context)

    // WaitForCacheSync blocks until the informer's initial List has completed,
    // or ctx is cancelled (returns false in that case).
    func (s *Store) WaitForCacheSync(ctx context.Context) bool

    type CreateMonitorParams struct {
    	ID           string
    	StreamURL    string
    	CallbackURL  string
    	Config       model.MonitorConfig
    	Metadata     json.RawMessage
    	InitialPhase model.MonitorStatus // this plan's callers always pass model.StatusInitializing
    }
    func (s *Store) Create(ctx context.Context, p CreateMonitorParams) (*model.Monitor, error)

    func (s *Store) GetByID(ctx context.Context, id string) (*model.Monitor, error)
    func (s *Store) GetWithStats(ctx context.Context, id string) (*model.MonitorWithStats, error)

    type ListParams struct {
    	Status *model.MonitorStatus
    	Limit  int
    	Offset int
    }
    func (s *Store) List(ctx context.Context, p ListParams) ([]*model.Monitor, int, error)

    func (s *Store) UpdateStatus(ctx context.Context, id string, status model.MonitorStatus) error
    func (s *Store) UpdateStatusWithCondition(ctx context.Context, id string, current, next model.MonitorStatus) (bool, error)
    func (s *Store) UpdatePodName(ctx context.Context, id string, podName string) error
    func (s *Store) UpdateStats(ctx context.Context, stats *model.MonitorStats) error
    func (s *Store) Delete(ctx context.Context, id string) error
    func (s *Store) GetActiveMonitors(ctx context.Context) ([]*model.Monitor, error)
    func (s *Store) CountActiveMonitors(ctx context.Context) (int, error)

    type UpdateMonitorParams struct {
    	CallbackURL *string
    	Config      *model.MonitorConfig
    }
    func (s *Store) UpdateMonitor(ctx context.Context, id string, p UpdateMonitorParams) (*model.Monitor, error)

    func StreamURLHash(streamURL string) string {
    	sum := sha256.Sum256([]byte(streamURL))
    	return hex.EncodeToString(sum[:])[:16]
    }

(Plan 2 adds one more read method, `ListScheduled`, to this same `Store` — a straightforward addition using the same phase index described below, not introduced here since nothing produces the `scheduled` phase yet.)

Implementation details that matter and must be followed precisely:

- The `SharedIndexInformer` is built from a `cache.ListerWatcher` backed by `dyn.Resource(v1alpha1.GVR).Namespace(namespace)` (list/watch of `unstructured.Unstructured` objects), with two indexers registered: one keyed by the `LabelStreamURLHash` label value, one keyed by `status.phase`. `GetActiveMonitors`, `CountActiveMonitors`, and the duplicate-URL check in `Create` use these indexes (via `informer.GetIndexer().ByIndex(...)`) rather than scanning every object, mirroring how the old SQL queries had a `WHERE status IN (...)` index to lean on.
- `Create` does, in order: (a) compute `hash := StreamURLHash(p.StreamURL)`; (b) look up the informer's stream-url-hash index for that hash, and if any matching object's `status.phase` is one of the active phases (`initializing`, `waiting`, `monitoring` — see `model.MonitorStatus.IsActive()`, unchanged in this plan; plan 2 later adds `scheduled` to this list), return `ErrDuplicateMonitor` immediately; (c) build an `unstructured.Unstructured` with `metadata.name = p.ID`, `metadata.labels[LabelStreamURLHash] = hash`, and the `spec.*` fields, and call `dyn.Resource(gvr).Namespace(ns).Create(ctx, obj, metav1.CreateOptions{})`; (d) **as a required second call**, set `status.phase = p.InitialPhase` on the object just returned by Create and call `.UpdateStatus(ctx, obj, metav1.UpdateOptions{})`. Step (d) is required — not optional — because enabling the `status` subresource means the API server always creates new objects with an empty `status`, silently discarding any `status` fields present in the Create payload; status can only be set afterward, through the status subresource. A novice implementer who skips step (d) will see every newly created `StreamMonitor` come back with `status.phase` empty and will likely spend time confused about why, so implement and test this two-step sequence explicitly.
- `UpdateStatusWithCondition(ctx, id, current, next)`: `Get` the object live from `dyn` (not the informer cache — the whole point of this operation is to act on a fresh `resourceVersion`, so a cache read defeats it), compare its `status.phase` to `current`; if it doesn't match, return `(false, nil)` immediately (someone already changed it — this is not an error). If it matches, set `status.phase = next` and call `UpdateStatus` using the `resourceVersion` obtained from the `Get`. If that call fails with a Conflict error (check with `k8serrors.IsConflict(err)`), also return `(false, nil)` rather than retrying — this exactly matches the "someone else already handled it, skip, don't retry" semantics `internal/k8s/reconcile.go` and `internal/k8s/watcher.go` already depend on from the old SQL `UPDATE ... WHERE status = $x`.
- `GetByID`/`GetWithStats`/`List` read from the informer's local indexer (`informer.GetIndexer().GetByKey(namespace + "/" + id)` for a single object, or a full `List()` of the indexer for the list endpoint, filtered/paginated in Go) rather than issuing a live API call — this keeps `GET` endpoints fast and avoids hammering the API server, at the cost of results being up to one watch-event behind the true state (typically well under a second). This mirrors the same trade-off already accepted for the duplicate-URL check.
- `Delete` calls the dynamic client's `Delete` directly (a write, must go live, not through the cache).
- Add `internal/k8s/store/convert.go` holding the two conversion functions between `*unstructured.Unstructured` and (`*model.Monitor`, `*model.MonitorStats`), implemented via `runtime.DefaultUnstructuredConverter.FromUnstructured`/`.ToUnstructured` against the `v1alpha1.StreamMonitor` struct as the intermediate step, so the JSON tags on `v1alpha1.StreamMonitorSpec`/`StreamMonitorStatus` are the single place that defines the mapping.
- Add `internal/k8s/store/store_test.go` using `k8s.io/client-go/dynamic/fake.NewSimpleDynamicClientWithCustomListKinds` (part of the already-required `k8s.io/client-go` module — no new dependency) to exercise `Create`, the duplicate-check path, and `UpdateStatusWithCondition`'s CAS behavior (including the "someone else already changed it" case) entirely in-memory, with no real cluster.

**5. Add the `scheduled` phase constant without wiring it up yet.**

In `internal/model/model.go`, add `StatusScheduled MonitorStatus = "scheduled"` to the enum now (per the Decision Log entry on shipping the full enum now), but do **not** change `IsActive()` to include it — nothing produces `StatusScheduled` in this plan, and plan 2 changes `IsActive()` together with the code that starts producing that phase, so the two ship together and are easy to reason about as one unit.

**6. Update `internal/k8s/k8s.go`: Pod creation takes an owner CR, not a Deployment.**

- Change `CreatePodParams.Config` from `*db.MonitorConfig` to `*model.MonitorConfig`.
- Add two fields to `CreatePodParams`: `OwnerUID types.UID` and `OwnerName string`.
- In `CreateWorkerPod`, replace the `OwnerReferences: c.buildOwnerReferences()` line with a literal owner reference to the `StreamMonitor`:

      OwnerReferences: []metav1.OwnerReference{{
      	APIVersion:         "streamtracker.xpadev.net/v1alpha1",
      	Kind:               "StreamMonitor",
      	Name:               params.OwnerName,
      	UID:                params.OwnerUID,
      	BlockOwnerDeletion: boolPtr(true),
      	Controller:         boolPtr(true),
      }},

- Delete `Client.ownerRef`, `SetOwnerReference`, `ResolveOwnerDeployment`, `buildOwnerReferences`, `findOwnerReference`, `buildDeploymentOwnerReference`, `BuildOwnerReference` — all of this existed solely to resolve the Gateway's own Deployment identity for the old owner-reference scheme, and is now dead code.

**7. Update `internal/k8s/reconcile.go` and `internal/k8s/watcher.go`.**

- `Reconciler.repo`/`PodWatcher.repo` change type from `*db.MonitorRepository` to `*store.Store`; `NewReconciler`/`NewPodWatcher` signatures update to match.
- Every `db.Status*`, `db.Monitor`, reference becomes `model.Status*`, `model.Monitor`.
- In `sendErrorWebhook` (reconcile.go) and `sendFailureWebhook` (watcher.go), delete the audit-trail block entirely — the code that builds a `db.MonitorEvent` and calls `CreateEvent`/`UpsertEvent`/`UpdateEventWebhookStatus`. Keep the webhook-send goroutine itself, and add (or keep, if already present) a `zap` log line recording success/failure locally, per the Decision Log.
- `CreateMonitorPod` (reconcile.go) now needs the monitor's UID to build the Pod's owner reference: pass `OwnerUID: monitor.UID, OwnerName: monitor.ID` into `CreatePodParams` (this is why step 2 above added a `UID` field to `model.Monitor`).
- The missing/zombie/orphaned-pod reconciliation logic in `ReconcileStartup` is structurally unchanged — it drives off `repo.GetActiveMonitors`/`k8sClient.ListWorkerPods`, both of which keep their existing signatures, just backed by `store.Store` now instead of Postgres.

**8. Update `internal/api/handlers.go`.**

- `Handler.repo` changes type from `*db.MonitorRepository` to `*store.Store`; `NewHandler` signature updates to match.
- Replace every `db.*` reference (`db.Status*`, `db.StreamStatus*`, `db.HealthStatus*`, `db.MonitorConfig`, `db.CreateMonitorParams`, `db.ListParams`, `db.UpdateMonitorParams`, `db.ErrMonitorNotFound`, `db.ErrDuplicateMonitor`, `db.ErrMonitorNotActive`, `db.DefaultMonitorConfig()`) with the corresponding `model.*`/`store.*` name.
- Delete entirely: the `RecordWebhookEventRequest` struct, the `RecordWebhookEvent` handler, the `ListEventsResponse`/`EventSummary` structs, and the `ListEvents` handler. These implement the two audit-log endpoints being removed.
- `CreateMonitor`'s control flow is otherwise unchanged in this plan — it still always creates a Pod immediately (plan 2 adds the deferred-creation branch).

**9. Update `cmd/gateway/main.go`.**

- Remove the `db.New`/`database.Migrate`/`db.NewMonitorRepository` block. In its place: build a `*rest.Config` (in-cluster if `cfg.InCluster`, else from `cfg.KubeConfigPath`/the default kubeconfig location — reuse the logic already in `k8s.NewClient`; factor it into a small shared helper, e.g. `k8s.BuildRESTConfig(cfg k8s.Config) (*rest.Config, error)`, so both the existing typed `*kubernetes.Clientset` and the new `dynamic.Interface` are built from one config-construction code path instead of two copies of the same logic). Then:

      dynClient, err := dynamic.NewForConfig(restConfig)
      ...
      monitorStore := store.NewStore(dynClient, cfg.Namespace)
      storeCtx, storeCancel := context.WithCancel(context.Background())
      go monitorStore.Run(storeCtx)
      syncCtx, syncCancel := context.WithTimeout(ctx, 30*time.Second)
      if !monitorStore.WaitForCacheSync(syncCtx) {
      	log.Fatal("timed out waiting for StreamMonitor cache to sync")
      }
      syncCancel()

- Delete the `cfg.PodName != ""` / `ResolveOwnerDeployment` / `SetOwnerReference` block entirely (dead per step 6).
- Delete the `runCleanupLoop` function, its goroutine launch, and the `cleanupCancel` plumbing (per the Decision Log entry on dropping stale-monitor cleanup).
- `readyzHandler(database *db.DB)` becomes `readyzHandler(store *store.Store)`, checking `store.WaitForCacheSync` (with a short/zero timeout, since by the time `/readyz` is polled the initial sync has already completed once — this call should just check "has it synced," not block) instead of `database.Health`.
- Remove the two routes for the deleted audit-log endpoints: `v1.GET("/monitors/:monitor_id/events", ...)` and `internal.POST("/monitors/:monitor_id/events", ...)`.

**10. Update the worker side.**

- `cmd/worker/main.go`: no changes are needed here (confirmed by reading its imports — it never touches `internal/db` beyond the shared types, see next bullet).
- `internal/worker/worker.go` and `internal/worker/callback.go`: change the import from `internal/db` to `internal/model`, and update every `db.MonitorStatus`/`db.WebhookStatus` reference to `model.*`. Delete the `WebhookEventReport` type, the `ReportWebhookEvent` method on the `CallbackReporter` interface, its implementation in `callback.go`, and the call site in `worker.go` that posts an audit event after sending a webhook (keep the webhook-send call itself — only the follow-up audit POST is removed). Update `internal/worker/worker_test.go` to match: delete `spyCallbackClient.ReportWebhookEvent` and any assertions on it, update `db.` references to `model.`.
- `internal/config/config.go`: `LoadWorkerConfig` currently calls `db.DefaultMonitorConfig().SilenceDBThreshold` — change this import to `internal/model`.

**11. Delete Postgres entirely.**

- Delete the `internal/db/` directory in full: `db.go`, `monitor_repository.go`, `models.go`, `migrations/001_initial_schema.sql`, `migrations/002_add_webhook_status_skipped.sql`.
- In `internal/config/config.go`: remove `GatewayConfig.DatabaseURL`, the `getEnvWithFallback("DB_DSN", "DATABASE_URL", "")` call and its required-field check in `LoadGatewayConfig`; remove `MonitorRetentionPeriod`/`CleanupInterval` fields (no longer used, per step 9).
- `docker-compose.yaml`: remove the `postgres` service block; remove the `gateway` service's `DATABASE_URL` env var and `depends_on: postgres`.
- `helm/stream-monitor/templates/deployment.yaml`: remove the `DATABASE_PASSWORD`/`DB_DSN`/`DATABASE_URL` env var blocks.
- `helm/stream-monitor/values.yaml`: remove the entire `postgresql:` section.
- `helm/stream-monitor/templates/_helpers.tpl`: remove the `stream-monitor.databaseURL` template define.
- `helm/stream-monitor/templates/rbac.yaml`: remove the `apps` `deployments`/`replicasets`/`deployments/finalizers` rules (dead per step 6's owner-reference change); add the new rule:

      - apiGroups: ["streamtracker.xpadev.net"]
        resources: ["streammonitors", "streammonitors/status"]
        verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]

- Run `go mod tidy` after all the above source edits so `github.com/jackc/pgx/v5` and its transitive dependencies (`pgpassfile`, `pgservicefile`, `puddle/v2`) are removed from `go.mod`/`go.sum` automatically.
- Confirm nothing still imports the deleted package: `grep -rl '"github.com/xpadev-net/youtube-stream-tracker/internal/db"' --include='*.go' .` must return no results.

## Concrete Steps

Run all commands from the repository root.

1. `go build ./...` — must succeed with zero errors, and zero remaining references to `internal/db` (verify with the `grep` command in step 11 of the Plan of Work above).
2. `go test ./...` — all tests pass, including the pre-existing `internal/worker` tests (they already use hand-written fakes — `stubYtDlpClient`, `spyCallbackClient` in `internal/worker/worker_test.go` — so they run with no real network access and needed no new test infrastructure) and the new `internal/k8s/store/store_test.go`.
3. `kind version` and `kubectl version --client` — confirm both are installed (install `kind` and `kubectl` first if not; this plan assumes a novice may not have them).
4. `kind create cluster --name stream-tracker`
5. `kubectl apply -f helm/stream-monitor/crds/streammonitor-crd.yaml` — expect `customresourcedefinition.apiextensions.k8s.io/streammonitors.streamtracker.xpadev.net created`, then `kubectl get crd streammonitors.streamtracker.xpadev.net` should show `Established`.
6. `docker build -t stream-monitor-gateway:dev -f Dockerfile.gateway .` then `kind load docker-image stream-monitor-gateway:dev --name stream-tracker` (repeat for the worker image with `Dockerfile.worker` / `stream-monitor-worker:dev`).
7. `helm install stream-monitor helm/stream-monitor --set gateway.image.tag=dev --set worker.image.tag=dev --set secrets.apiKey=test-key --set secrets.internalApiKey=test-internal --set secrets.webhookSigningKey=test-signing` (adjust flags to match this chart's actual `values.yaml` key names, which may differ slightly from these illustrative names — check `helm/stream-monitor/values.yaml` at implementation time).
8. `kubectl get pods` — the gateway Pod reaches `Running`, `1/1 Ready`. `kubectl logs deploy/stream-monitor-gateway` shows `starting API Gateway` and no database-connection errors (there is no database code left to produce one).
9. `kubectl port-forward svc/stream-monitor-gateway 8080:8080 &`
10. `curl -s -X POST localhost:8080/api/v1/monitors -H 'X-API-Key: test-key' -H 'Content-Type: application/json' -d '{"stream_url":"https://www.youtube.com/watch?v=dQw4w9WgXcQ","callback_url":"http://example.com/callback"}'` — expect HTTP `201` and a JSON body containing a `monitor_id` shaped like `mon-...`.
11. `kubectl get streammonitors` — the new object appears with printer columns showing a `Phase`. `kubectl get streammonitor <id> -o yaml` shows `spec.streamURL`, `metadata.labels."streamtracker.xpadev.net/stream-url-hash"`, and (once the worker Pod has reported at least once) `status.phase`/`status.podName`.
12. `kubectl get pods -l app=stream-monitor` — a `stream-monitor-<id>` Pod exists; `kubectl get pod stream-monitor-<id> -o jsonpath='{.metadata.ownerReferences}'` shows `"kind":"StreamMonitor"`.
13. Repeat step 10's exact `curl` command with the same `stream_url` — expect HTTP `409` with an error code indicating a duplicate monitor, proving the informer-backed uniqueness check works.
14. `curl -X DELETE localhost:8080/api/v1/monitors/<id> -H 'X-API-Key: test-key'` — then `kubectl get streammonitor <id>` returns "not found", and shortly after, `kubectl get pod stream-monitor-<id>` also returns "not found" (proving the Pod-owned-by-CR garbage collection from step 6 of the Plan of Work works, in addition to the explicit delete call).
15. Teardown: `helm uninstall stream-monitor`, `kubectl delete -f helm/stream-monitor/crds/streammonitor-crd.yaml`, `kind delete cluster --name stream-tracker`.

## Validation and Acceptance

This plan is accepted when every numbered step in "Concrete Steps" produces the stated observation, `go build ./...` and `go test ./...` both succeed, and `grep -rl "internal/db"` finds nothing. At that point, the system has no database anywhere, and its full existing behavior (create, list, get, patch, delete a monitor; a worker's status reports reaching the CRD's `status`; Pod-failure detection; drift reconciliation) is demonstrated working against a real cluster exactly as it did before this change, via the same public HTTP API.

Only once this is true should `docs/coding-agent/plans/02-streammonitor-scheduled-reservations.md` be started.

## Idempotence and Recovery

Every step in "Concrete Steps" can be re-run safely: `kubectl apply` on the CRD is idempotent (no-op if unchanged), `helm install`/`helm uninstall` are the chart's normal lifecycle commands, `kind create/delete cluster` fully resets local state, and the `curl` calls are the system's normal public API (a duplicate `POST` correctly fails with `409` rather than corrupting state, per step 13). If a step fails partway — e.g. `helm install` succeeds but the gateway Pod crash-loops — inspect `kubectl logs deploy/stream-monitor-gateway` and `kubectl describe pod ...`, fix the underlying Go/config issue, `docker build`+`kind load` the corrected image again, and `kubectl rollout restart deploy/stream-monitor-gateway` — no manual cleanup of Kubernetes state is required since Pods/CRs created by a failed attempt are ordinary objects that `helm uninstall`/`kind delete cluster` remove along with everything else. There is no database migration step to roll back, by design.

## Artifacts and Notes

(To be filled in during implementation with real `kubectl`/`curl` transcripts proving each Concrete Step, per PLANS.md's requirement to capture evidence.)

## Interfaces and Dependencies

In `internal/model` (package `model`), define: `MonitorStatus`, `HealthStatus`, `StreamStatus`, `MonitorConfig` (with `Validate() error`), `DefaultMonitorConfig() MonitorConfig`, `Monitor` (including a new `UID types.UID` field), `MonitorStats`, `MonitorWithStats`.

In `internal/k8s/apis/streamtracker/v1alpha1` (package `v1alpha1`), define: `StreamMonitorSpec`, `StreamMonitorStatus`, `StreamMonitor`, `StreamMonitorList`, `GVR schema.GroupVersionResource`, each satisfying `k8s.io/apimachinery/pkg/runtime.Object` via a hand-written `DeepCopyObject()`.

In `internal/k8s/store` (package `store`), define `type Store struct{...}` with the full method set enumerated in step 4 above — this is the interface that `internal/api.Handler`, `internal/k8s.Reconciler`, and `internal/k8s.PodWatcher` depend on in place of `*internal/db.MonitorRepository`.

In `internal/k8s` (package `k8s`, existing), `Client.CreateWorkerPod`'s `CreatePodParams` gains `OwnerUID types.UID`/`OwnerName string`.

Dependencies used, all already present in `go.mod` at v0.32.0, none newly added: `k8s.io/client-go/dynamic`, `k8s.io/client-go/dynamic/fake`, `k8s.io/client-go/tools/cache`, `k8s.io/apimachinery/pkg/apis/meta/v1/unstructured`, `k8s.io/apimachinery/pkg/runtime`. Dependency removed: `github.com/jackc/pgx/v5` and its transitive tree, via `go mod tidy` after all source edits.
</content>
