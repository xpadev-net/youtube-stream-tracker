# Reservation-based automatic start/stop for `StreamMonitor`

This ExecPlan is a living document. The sections `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective` must be kept up to date as work proceeds. This plan must be maintained in accordance with `.agent/PLANS.md` at the repository root.

This is plan 2 of 2. It depends entirely on plan 1, `docs/coding-agent/plans/01-streammonitor-crd-migration.md`, which must be fully complete and validated (all of that plan's "Concrete Steps" pass) before this plan begins. Do not start this plan otherwise — there is no database code left in the codebase by the time plan 1 finishes, and this plan assumes that is already true.

## Purpose / Big Picture

By the end of plan 1, every monitored YouTube livestream is a `StreamMonitor` Kubernetes custom resource, and creating one (`POST /api/v1/monitors`) always creates a worker Pod immediately — even if the caller already knows the stream will not start for hours or days. That wastes a running Pod for the entire wait, and it still requires whoever wants monitoring to start "on time" to make the `POST` call at exactly the right moment.

After this change, a caller can register a stream slot ahead of time — the stream's URL, plus a scheduled start time and a scheduled end time — without an idle worker Pod being created immediately. Concretely: `POST /api/v1/monitors` with a `scheduled_start_time` far in the future returns a monitor in `"scheduled"` status, and `kubectl get pods` shows no Pod created for it. As the scheduled start time approaches (within a configurable lead time, default 10 minutes), the Gateway itself promotes the reservation to an active, Pod-backed monitor automatically — no further API call needed. If a reservation's `scheduled_end_time` (plus a tolerance) passes without the stream ever going live, the Gateway automatically marks it `stopped` and sends a webhook explaining why, so a forgotten or cancelled reservation does not sit around forever. The system's existing behavior — a worker detecting, on its own, that a live stream has actually ended, and shutting itself down — is completely untouched; this plan only changes what happens *before* a Pod exists.

## Progress

- [ ] Not started. Blocked on plan 1 (`docs/coding-agent/plans/01-streammonitor-crd-migration.md`) being complete.

## Surprises & Discoveries

(none yet — update this section as implementation proceeds)

## Decision Log

- Decision: The trigger for "start monitoring automatically" is an external system calling the existing create-monitor API ahead of time with a scheduled start/end time — not automatic discovery of upcoming streams from a YouTube channel (no YouTube Data API integration, no channel scraping).
  Rationale: user decision — the system that knows about upcoming stream slots ("配信枠") already exists outside this project and will push that information in; this project does not need to (and was explicitly asked not to) go find it.
  Date/Author: user decision, recorded 2026-08-21.

- Decision: The existing `POST /api/v1/monitors` endpoint and `MonitorConfig` are extended (a new `scheduled_end_time` field, and new deferred-creation behavior triggered by an existing field, `scheduled_start_time`) rather than introducing a separate "reservation" API/resource kind.
  Rationale: minimizes new API surface for API consumers to learn, and reuses 100% of the existing validation, rate-limiting, and `StreamMonitor` machinery plan 1 already built — a reservation is simply a `StreamMonitor` in an earlier phase (`scheduled`) of the same lifecycle a monitor always had, not a fundamentally different kind of object.
  Date/Author: plan authored 2026-08-21.

- Decision: `model.MonitorStatus.IsActive()` is changed to include `StatusScheduled`.
  Rationale: a reservation with no Pod yet must still count against `MAX_MONITORS` and must still block a duplicate `POST` for the same `stream_url` — otherwise a caller could register unlimited reservations with no resource ever "spent," defeating the purpose of `MAX_MONITORS`, and two reservations (or a reservation and a live monitor) could silently target the same stream.
  Date/Author: plan authored 2026-08-21.

- Decision: The "abandoned reservation" safety-net tolerance reuses the existing `Reconciler.timeout` (`ReconcileTimeout` config) rather than introducing a new configuration value.
  Rationale: avoids configuration sprawl for a second timeout whose purpose (how long past a deadline to wait before acting) is conceptually the same kind of value as the existing reconcile timeout. This reuse is called out explicitly in code comments since the config name does not make the second purpose obvious on its own.
  Date/Author: plan authored 2026-08-21, judgment call — revisit if the two timeouts ever need genuinely different values in practice.

- Decision: The scheduled-promotion loop reuses the existing `ReconcileInterval` ticker cadence rather than introducing a third configurable interval.
  Rationale: same rationale as above — avoid config sprawl; the promotion check and the drift-reconciliation check are both cheap, infrequent, informer-cache-driven scans and there is no operational reason to run them at different cadences today.
  Date/Author: plan authored 2026-08-21, judgment call.

## Context and Orientation

This plan assumes the reader has plan 1 (`docs/coding-agent/plans/01-streammonitor-crd-migration.md`) complete and validated. In particular, by the time this plan starts, the following already exist and are not re-explained here in full — see plan 1's "Context and Orientation" section for the full background:

- `internal/model` (package `model`): pure domain types, including `MonitorStatus` (enum `initializing`, `waiting`, `monitoring`, `scheduled`, `completed`, `stopped`, `error` — the `scheduled` value already exists as a constant from plan 1, but nothing produces it yet and `IsActive()` does not yet include it), `MonitorConfig` (already has a `ScheduledStartTime *time.Time` field, used today only to decide when to send a "the stream is late" webhook, not to defer Pod creation), `Monitor`, `MonitorStats`.
- `internal/k8s/apis/streamtracker/v1alpha1`: the hand-written `StreamMonitor` CRD Go types. `StreamMonitorSpec.ScheduledEndTime *metav1.Time` already exists in this struct and in the CRD's OpenAPI schema (`helm/stream-monitor/crds/streammonitor-crd.yaml`) — plan 1 shipped it early specifically so this plan would not need a second manual `kubectl apply` of the CRD (Helm's `crds/` directory is not touched by `helm upgrade`, only by the first `helm install` — see plan 1's Decision Log).
- `internal/k8s/store` (package `store`): the informer-cache-backed `Store` type that replaced `*db.MonitorRepository`, with methods `Create`, `GetByID`, `GetWithStats`, `List`, `UpdateStatus`, `UpdateStatusWithCondition`, `UpdatePodName`, `UpdateStats`, `Delete`, `GetActiveMonitors`, `CountActiveMonitors`, `UpdateMonitor` — all already reading from/writing to `StreamMonitor` custom resources.
- `internal/api/handlers.go`'s `CreateMonitor` handler: today always calls `h.reconciler.CreateMonitorPod` synchronously after `h.store.Create` succeeds.
- `internal/k8s/reconcile.go`'s `Reconciler`: already has a `RunPeriodic(ctx, interval)` ticker loop calling `ReconcileStartup`, which scans `store.GetActiveMonitors` to detect and fix drift (missing/zombie/orphaned Pods), using the field `Reconciler.timeout` (populated from `cfg.ReconcileTimeout`) as a general-purpose timeout value.
- `internal/worker/worker.go`: the worker's own state machine, which already detects (via `yt-dlp`) when a live stream's manifest has ended and drives itself to `StatusCompleted`. This plan does not touch this file at all.

One more piece of terminology specific to this plan: a "reservation" is not a new Kubernetes kind — it is simply a `StreamMonitor` whose `status.phase` is `"scheduled"`, meaning "this monitor's identity and configuration exist, but no worker Pod has been created for it yet."

## Plan of Work

**Goal at the end of this plan:** a caller can `POST /api/v1/monitors` with a `scheduled_start_time` far in the future (and a new `scheduled_end_time`) and get back a monitor in `"scheduled"` status with **no worker Pod created yet** — proven by `kubectl get pods` showing nothing for that monitor. As the scheduled start time approaches (within a configurable lead time, default 10 minutes), the Gateway itself promotes the reservation to an active, Pod-backed monitor automatically, with no further API call required. If a reservation's `scheduled_end_time` (plus a tolerance) passes without the stream ever going live, the Gateway automatically marks it `stopped` and sends a webhook explaining why.

**1. Extend the config and request types.**

- `internal/model/model.go`: add `ScheduledEndTime *time.Time \`json:"scheduled_end_time,omitempty"\`` to `MonitorConfig`.
- `internal/k8s/apis/streamtracker/v1alpha1/types.go`: `StreamMonitorSpec.ScheduledEndTime` already exists (from plan 1) — no change needed here; only confirm the store's `convert.go` round-trips it (it will, automatically, since it is JSON-tag-driven).
- `internal/api/handlers.go`: add `ScheduledEndTime *time.Time \`json:"scheduled_end_time,omitempty"\`` to `MonitorConfigRequest`, and the corresponding copy line in `applyConfigOverrides`.
- `internal/config/config.go`: add `ScheduleLeadTime time.Duration` to `GatewayConfig`, loaded via `getEnvDuration("SCHEDULE_LEAD_TIME", 10*time.Minute)` in `LoadGatewayConfig`. Reuse the existing `ReconcileTimeout` config as the "abandoned reservation" tolerance rather than adding a new config field, per the Decision Log — document this reuse plainly in a code comment since the name doesn't obviously suggest this second purpose.

**2. Extend `model.MonitorStatus.IsActive()`.**

Change `IsActive()` to `return s == StatusInitializing || s == StatusWaiting || s == StatusMonitoring || s == StatusScheduled`. This is a deliberate, necessary change: a `"scheduled"` reservation must count against `MAX_MONITORS` and must block a duplicate `POST` for the same `stream_url`, exactly as an already-running monitor does — otherwise a caller could stack unlimited reservations with no worker Pod ever created to "use up" the limit, defeating the whole point of `MAX_MONITORS`.

**3. Change `CreateMonitor`'s control flow for deferred creation.**

In `internal/api/handlers.go`'s `CreateMonitor`, after `config := applyConfigOverrides(...)` and before calling `h.store.Create`, add:

    now := time.Now()
    initialPhase := model.StatusInitializing
    deferPodCreation := config.ScheduledStartTime != nil && config.ScheduledStartTime.Sub(now) > h.scheduleLeadTime
    if deferPodCreation {
    	initialPhase = model.StatusScheduled
    }

Pass `InitialPhase: initialPhase` into `store.CreateMonitorParams`. Skip the `h.reconciler.CreateMonitorPod` call (and the pod-creation-failure error handling around it) entirely when `deferPodCreation` is true — respond `201` with `Status: "scheduled"` in that case. When `deferPodCreation` is false, behavior is byte-for-byte identical to plan 1's behavior (including for every existing caller that never sets `scheduled_start_time`, or sets one already close to now — this is the backward-compatibility guarantee for this plan). `Handler` gains a `scheduleLeadTime time.Duration` field, threaded through `NewHandler`'s parameter list and its call site in `cmd/gateway/main.go`.

**4. Add the promotion loop.**

Create `internal/k8s/schedule.go`:

    package k8s

    type SchedulePromoter struct {
    	store             *store.Store
    	reconciler        *Reconciler
    	leadTime          time.Duration
    	endTimeTolerance  time.Duration
    	internalAPIKey, webhookSigningKey, secretsName, internalKeyName, signingKeyName string
    }

    func NewSchedulePromoter(s *store.Store, r *Reconciler, leadTime, endTimeTolerance time.Duration, internalAPIKey, webhookSigningKey, secretsName, internalKeyName, signingKeyName string) *SchedulePromoter

    // RunPeriodic runs tick() on a ticker until ctx is cancelled — same shape
    // as Reconciler.RunPeriodic, reuse that pattern rather than inventing a
    // new one.
    func (p *SchedulePromoter) RunPeriodic(ctx context.Context, interval time.Duration)

    func (p *SchedulePromoter) tick(ctx context.Context)

`tick` lists all monitors in `status.phase == "scheduled"` from the informer cache's phase index (`store.ListScheduled(ctx)` — add this small method to `Store`, it is just an indexer lookup like `GetActiveMonitors`/`CountActiveMonitors` already are). For each one whose `spec.scheduledStartTime` is now within `leadTime`: call `store.UpdateStatusWithCondition(ctx, id, model.StatusScheduled, model.StatusInitializing)`; if that returns `(true, nil)` — meaning this call won the race to promote it — call `reconciler.CreateMonitorPod(ctx, monitor, ...)`, the exact same method the synchronous `CreateMonitor` path already uses, so Pod-creation logic is written once and shared by both the immediate and deferred paths.

Wire it up in `cmd/gateway/main.go`: `schedulePromoter := k8s.NewSchedulePromoter(monitorStore, reconciler, cfg.ScheduleLeadTime, cfg.ReconcileTimeout, ...)`, `go schedulePromoter.RunPeriodic(promoterCtx, cfg.ReconcileInterval)` (reuse `ReconcileInterval`, per the Decision Log), with its own cancel func wired into the graceful-shutdown sequence alongside `reconcileCancel`/`watcherCancel`.

**5. Add the abandoned-reservation safety net.**

Extend `Reconciler.ReconcileStartup` (`internal/k8s/reconcile.go`) with one more pass, alongside the existing missing/zombie/orphaned-pod passes: for every active monitor (from the same `GetActiveMonitors` call already being made) whose `Config.ScheduledEndTime != nil` and `time.Now().After(ScheduledEndTime.Add(r.timeout))` (reusing the `Reconciler.timeout` field, i.e. `ReconcileTimeout`, as the tolerance — per step 1's config-reuse decision), delete its Pod if one exists (`k8sClient.DeleteWorkerPod`) and call `UpdateStatusWithCondition(ctx, id, currentPhase, model.StatusStopped)`. On a successful transition, send a webhook — reuse `sendErrorWebhook`'s webhook-send machinery (the goroutine + zap logging, minus the audit-write half plan 1 already deleted) with a reason field such as `"reservation_abandoned"` explaining that the scheduled end time passed without the stream ever going live (or that monitoring ran past the scheduled end time without the worker completing normally).

**6. Confirm what does *not* change.**

State this explicitly in code review / commit message for this plan: `internal/worker/worker.go`'s existing state machine — the code that already detects, via `yt-dlp`, that a live stream's manifest has ended, and drives the worker to report `StatusCompleted` and exit — is completely untouched. This plan only ever changes what happens *before* a worker Pod exists (deferred creation) and adds a safety net for reservations whose Pod *never* gets created because the stream never showed up. The primary "auto stop when the stream actually ends" mechanism the user already has today keeps working exactly as it does now.

## Concrete Steps

Run all commands from the repository root, after plan 1's environment (a `kind` cluster with the CRD and chart installed, or a fresh equivalent setup) is available.

1. `go test ./internal/k8s/...` — new tests for `SchedulePromoter.tick` against a fake dynamic client (`k8s.io/client-go/dynamic/fake`) with a seeded `scheduled` object, asserting the phase transition and Pod-creation call happen only once the scheduled start time is within the lead time, not before.
2. Rebuild and reinstall (repeat the `docker build`/`kind load`/`helm install` sequence from plan 1's Concrete Steps) with `--set gateway.env.SCHEDULE_LEAD_TIME=10m` (or the chart's equivalent way of setting this env var — check `values.yaml`/`configmap.yaml` for the actual mechanism at implementation time).
3. `curl ... -d '{"stream_url":"...","callback_url":"...","config":{"scheduled_start_time":"<now + 1 hour, RFC3339>","scheduled_end_time":"<now + 2 hours, RFC3339>"}}'` — expect `201` with `"status":"scheduled"`. `kubectl get pods -l monitor-id=<id>` returns empty — this is the concrete, observable proof that no idle Pod was created for a far-future reservation.
4. `curl ... -d '{"stream_url":"...(different url)...","callback_url":"...","config":{"scheduled_start_time":"<now + 5 minutes>"}}'` (inside the default 10-minute lead window) — expect `"status":"initializing"` and a Pod created immediately, proving the backward-compatible fallback path for near-term schedules.
5. For a fast local promotion check, install with a short lead time (e.g. `--set ... SCHEDULE_LEAD_TIME=30s`) and a short reconcile interval, create a monitor with `scheduled_start_time` 20 seconds in the future, then `kubectl get streammonitor <id> -w` and observe `Phase` flip from `scheduled` to `initializing` within one tick, with a Pod appearing at the same time.
6. Abandoned-reservation check: create a monitor with `scheduled_end_time` a few seconds in the future and a short reconcile timeout/tolerance, wait one reconcile tick, and observe (via `kubectl get streammonitor <id> -o yaml`) `status.phase` become `stopped`; if a webhook receiver is configured (the repository's existing `cmd/webhook-demo` binary is suitable for this), confirm it received a webhook explaining the reservation was abandoned.

## Validation and Acceptance

This plan is accepted when every numbered step in "Concrete Steps" produces the stated observation — specifically, that a far-future reservation creates no Pod, a near-future one behaves exactly as before, the promotion happens automatically without any further API call, and an abandoned reservation is automatically marked `stopped` with a webhook explaining why. `go build ./...` and `go test ./...` continue to succeed.

## Idempotence and Recovery

Every step in "Concrete Steps" can be re-run safely, for the same reasons given in plan 1's "Idempotence and Recovery" section: `helm install`/`upgrade`/`uninstall` and `kubectl` operations here are all ordinary, idempotent lifecycle operations, and the `curl` calls exercise the system's normal public API, which already fails safely (`409` on duplicates) rather than corrupting state. There is no data migration step to roll back, by design — every state transition described here (scheduled → initializing, scheduled/waiting/monitoring → stopped) is a single CRD status update, safely re-driven by the next reconcile tick if a previous attempt was interrupted.

## Artifacts and Notes

(To be filled in during implementation with real `kubectl`/`curl` transcripts proving each Concrete Step, per PLANS.md's requirement to capture evidence.)

## Interfaces and Dependencies

`internal/model.MonitorConfig` gains `ScheduledEndTime *time.Time`; `internal/model.MonitorStatus.IsActive()` is modified as described above.

`internal/api.MonitorConfigRequest` gains `ScheduledEndTime *time.Time`; `internal/api.Handler` gains a `scheduleLeadTime time.Duration` field.

`internal/k8s.Store` (package `store`) gains `ListScheduled(ctx context.Context) ([]*model.Monitor, error)`.

`internal/k8s` (package `k8s`, existing) gains `SchedulePromoter` with the constructor and method signatures given in step 4 above.

`internal/config.GatewayConfig` gains `ScheduleLeadTime time.Duration`.

No new Go module dependencies are introduced by this plan (the fake dynamic client used in tests, `k8s.io/client-go/dynamic/fake`, is already part of the `k8s.io/client-go` module already required as of plan 1).
</content>
