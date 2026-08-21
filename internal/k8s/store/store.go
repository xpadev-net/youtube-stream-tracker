// Package store provides the informer-backed replacement for the old
// *db.MonitorRepository (Postgres-backed). A Store reads a StreamMonitor
// custom resource's spec/status directly from Kubernetes (writes) and from
// a local in-process cache kept in sync by a Kubernetes "informer" (reads).
//
// An "informer" (from k8s.io/client-go/tools/cache) is a component that
// performs an initial List of every matching object, then opens a Watch to
// receive further changes, and keeps a local, thread-safe copy of every
// object up to date as changes stream in. Reading from the informer's
// local copy (via its "indexer") is fast — no network round trip to the
// Kubernetes API server — at the cost of results being up to one watch
// event behind the true state, typically well under a second. Writes
// (Create/Update/Delete) always go directly to the API server, never
// through the cache.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/xpadev-net/youtube-stream-tracker/internal/k8s/apis/streamtracker/v1alpha1"
	"github.com/xpadev-net/youtube-stream-tracker/internal/model"
)

var (
	ErrMonitorNotFound  = errors.New("monitor not found")
	ErrDuplicateMonitor = errors.New("duplicate monitor for stream URL")
	ErrMonitorNotActive = errors.New("monitor is not in an active state")
)

// LabelStreamURLHash is the label key holding StreamURLHash(spec.streamURL),
// set on every StreamMonitor object at creation time so that the informer
// can index and look up monitors by their stream URL without scanning every
// object (mirrors the role the old Postgres unique index played).
const LabelStreamURLHash = "streamtracker.xpadev.net/stream-url-hash"

// indexStreamURLHash and indexPhase are the names of the two indexes
// registered on the informer (see NewStore).
const (
	indexStreamURLHash = "streamURLHash"
	indexPhase         = "phase"
)

// Store is the informer-backed replacement for the old
// *db.MonitorRepository. All reads other than Delete's implicit read go
// through the informer's local cache; all writes go directly to the
// Kubernetes API server via the dynamic client.
type Store struct {
	dyn       dynamic.Interface
	namespace string
	informer  cache.SharedIndexInformer
}

// NewStore creates a Store backed by dyn for StreamMonitor objects in the
// given namespace. The returned Store is not usable for reads until Run has
// been started in a goroutine and WaitForCacheSync has returned true.
func NewStore(dyn dynamic.Interface, namespace string) *Store {
	resource := dyn.Resource(v1alpha1.GVR).Namespace(namespace)

	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return resource.List(context.Background(), options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return resource.Watch(context.Background(), options)
		},
	}

	informer := cache.NewSharedIndexInformer(lw, &unstructured.Unstructured{}, 0, cache.Indexers{
		indexStreamURLHash: streamURLHashIndexFunc,
		indexPhase:         phaseIndexFunc,
	})

	return &Store{
		dyn:       dyn,
		namespace: namespace,
		informer:  informer,
	}
}

func streamURLHashIndexFunc(obj interface{}) ([]string, error) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil, nil
	}
	hash := u.GetLabels()[LabelStreamURLHash]
	if hash == "" {
		return nil, nil
	}
	return []string{hash}, nil
}

func phaseIndexFunc(obj interface{}) ([]string, error) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil, nil
	}
	phase, found, err := unstructured.NestedString(u.Object, "status", "phase")
	if err != nil || !found || phase == "" {
		return nil, nil
	}
	return []string{phase}, nil
}

// Run starts the informer's list-then-watch loop; blocks until ctx is done.
// Must be started (in a goroutine) before the Store is used for reads.
func (s *Store) Run(ctx context.Context) {
	s.informer.Run(ctx.Done())
}

// WaitForCacheSync blocks until the informer's initial List has completed,
// or ctx is cancelled (returns false in that case).
func (s *Store) WaitForCacheSync(ctx context.Context) bool {
	return cache.WaitForCacheSync(ctx.Done(), s.informer.HasSynced)
}

// StreamURLHash returns a short, stable, non-reversible identifier for a
// stream URL, used as a label value (Kubernetes label values cannot contain
// arbitrary characters like a raw URL can) to index monitors by stream URL.
func StreamURLHash(streamURL string) string {
	sum := sha256.Sum256([]byte(streamURL))
	return hex.EncodeToString(sum[:])[:16]
}

// CreateMonitorParams contains parameters for creating a monitor.
type CreateMonitorParams struct {
	ID           string
	StreamURL    string
	CallbackURL  string
	Config       model.MonitorConfig
	Metadata     json.RawMessage
	InitialPhase model.MonitorStatus
}

// Create creates a new StreamMonitor object for the given parameters,
// rejecting the request with ErrDuplicateMonitor if an active monitor for
// the same stream URL already exists (per the informer's — possibly
// slightly stale — view of the world).
func (s *Store) Create(ctx context.Context, p CreateMonitorParams) (*model.Monitor, error) {
	hash := StreamURLHash(p.StreamURL)

	existing, err := s.informer.GetIndexer().ByIndex(indexStreamURLHash, hash)
	if err != nil {
		return nil, fmt.Errorf("check duplicate stream URL: %w", err)
	}
	for _, obj := range existing {
		u, ok := obj.(*unstructured.Unstructured)
		if !ok {
			continue
		}
		phase, found, _ := unstructured.NestedString(u.Object, "status", "phase")
		if found && model.MonitorStatus(phase).IsActive() {
			return nil, ErrDuplicateMonitor
		}
	}

	sm := &v1alpha1.StreamMonitor{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
			Kind:       v1alpha1.Kind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      p.ID,
			Namespace: s.namespace,
			Labels: map[string]string{
				LabelStreamURLHash: hash,
			},
		},
		Spec: StreamMonitorSpecFromConfig(p.StreamURL, p.CallbackURL, p.Config),
	}
	if len(p.Metadata) > 0 {
		sm.Spec.Metadata = &runtime.RawExtension{Raw: p.Metadata}
	}

	obj, err := toUnstructured(sm)
	if err != nil {
		return nil, fmt.Errorf("convert to unstructured: %w", err)
	}

	created, err := s.dyn.Resource(v1alpha1.GVR).Namespace(s.namespace).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			return nil, ErrDuplicateMonitor
		}
		return nil, fmt.Errorf("create StreamMonitor: %w", err)
	}

	// The status subresource means the API server always creates new
	// objects with an empty status, silently discarding any status fields
	// present in the Create payload above — status can only be set
	// afterward, through the status subresource. This second call is
	// required, not optional.
	if err := unstructured.SetNestedField(created.Object, string(p.InitialPhase), "status", "phase"); err != nil {
		return nil, fmt.Errorf("set initial phase: %w", err)
	}
	updated, err := s.dyn.Resource(v1alpha1.GVR).Namespace(s.namespace).UpdateStatus(ctx, created, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("set initial status: %w", err)
	}

	result, err := fromUnstructured(updated)
	if err != nil {
		return nil, fmt.Errorf("convert from unstructured: %w", err)
	}
	return toMonitor(result), nil
}

// StreamMonitorSpecFromConfig builds a StreamMonitorSpec from the given
// stream/callback URLs and monitor config. Exported for use by callers that
// need to build a spec directly (e.g. plan 2's scheduled-reservation path).
func StreamMonitorSpecFromConfig(streamURL, callbackURL string, cfg model.MonitorConfig) v1alpha1.StreamMonitorSpec {
	spec := v1alpha1.StreamMonitorSpec{
		StreamURL:              streamURL,
		CallbackURL:            callbackURL,
		CheckIntervalSec:       cfg.CheckIntervalSec,
		BlackoutThresholdSec:   cfg.BlackoutThresholdSec,
		SilenceThresholdSec:    cfg.SilenceThresholdSec,
		SilenceDBThreshold:     cfg.SilenceDBThreshold,
		StartDelayToleranceSec: cfg.StartDelayToleranceSec,
	}
	spec.ScheduledStartTime = metav1TimePtr(cfg.ScheduledStartTime)
	return spec
}

// getFromCache reads a single StreamMonitor from the informer's local cache
// by ID (object name), returning ErrMonitorNotFound if absent.
func (s *Store) getFromCache(id string) (*v1alpha1.StreamMonitor, error) {
	key := s.namespace + "/" + id
	obj, exists, err := s.informer.GetIndexer().GetByKey(key)
	if err != nil {
		return nil, fmt.Errorf("get from cache: %w", err)
	}
	if !exists {
		return nil, ErrMonitorNotFound
	}
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("unexpected cache object type %T", obj)
	}
	return fromUnstructured(u)
}

// getLive reads a single StreamMonitor directly from the Kubernetes API
// server (not the cache), returning ErrMonitorNotFound if absent. Used by
// writes that need a fresh resourceVersion.
func (s *Store) getLive(ctx context.Context, id string) (*unstructured.Unstructured, error) {
	u, err := s.dyn.Resource(v1alpha1.GVR).Namespace(s.namespace).Get(ctx, id, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, ErrMonitorNotFound
		}
		return nil, fmt.Errorf("get StreamMonitor: %w", err)
	}
	return u, nil
}

// GetByID retrieves a monitor by ID from the informer's local cache.
func (s *Store) GetByID(ctx context.Context, id string) (*model.Monitor, error) {
	sm, err := s.getFromCache(id)
	if err != nil {
		return nil, err
	}
	return toMonitor(sm), nil
}

// GetWithStats retrieves a monitor with its statistics from the informer's
// local cache.
func (s *Store) GetWithStats(ctx context.Context, id string) (*model.MonitorWithStats, error) {
	sm, err := s.getFromCache(id)
	if err != nil {
		return nil, err
	}
	return &model.MonitorWithStats{
		Monitor: *toMonitor(sm),
		Stats:   toStats(sm),
	}, nil
}

// ListParams contains parameters for listing monitors.
type ListParams struct {
	Status *model.MonitorStatus
	Limit  int
	Offset int
}

// List retrieves monitors with optional filtering from the informer's
// local cache, newest first (matching the old Postgres query's
// ORDER BY created_at DESC).
func (s *Store) List(ctx context.Context, p ListParams) ([]*model.Monitor, int, error) {
	if p.Limit <= 0 {
		p.Limit = 50
	}
	if p.Limit > 100 {
		p.Limit = 100
	}

	var objs []interface{}
	if p.Status != nil {
		matched, err := s.informer.GetIndexer().ByIndex(indexPhase, string(*p.Status))
		if err != nil {
			return nil, 0, fmt.Errorf("list by phase: %w", err)
		}
		objs = matched
	} else {
		objs = s.informer.GetIndexer().List()
	}

	monitors := make([]*model.Monitor, 0, len(objs))
	for _, obj := range objs {
		u, ok := obj.(*unstructured.Unstructured)
		if !ok {
			continue
		}
		sm, err := fromUnstructured(u)
		if err != nil {
			continue
		}
		monitors = append(monitors, toMonitor(sm))
	}

	sort.Slice(monitors, func(i, j int) bool {
		return monitors[i].CreatedAt.After(monitors[j].CreatedAt)
	})

	total := len(monitors)

	start := p.Offset
	if start > total {
		start = total
	}
	end := start + p.Limit
	if end > total {
		end = total
	}

	return monitors[start:end], total, nil
}

// UpdateStatus unconditionally sets status.phase for the given monitor.
func (s *Store) UpdateStatus(ctx context.Context, id string, status model.MonitorStatus) error {
	live, err := s.getLive(ctx, id)
	if err != nil {
		return err
	}
	if err := unstructured.SetNestedField(live.Object, string(status), "status", "phase"); err != nil {
		return fmt.Errorf("set phase: %w", err)
	}
	if _, err := s.dyn.Resource(v1alpha1.GVR).Namespace(s.namespace).UpdateStatus(ctx, live, metav1.UpdateOptions{}); err != nil {
		if k8serrors.IsNotFound(err) {
			return ErrMonitorNotFound
		}
		return fmt.Errorf("update status: %w", err)
	}
	return nil
}

// UpdateStatusWithCondition updates status.phase to next only if the
// object's current status.phase equals current, using a fresh live read
// (not the cache) so the comparison and the resourceVersion used for the
// write are consistent. Returns (false, nil) — not an error — both when the
// live phase didn't match current, and when the write itself lost a
// concurrent-update race (HTTP 409 Conflict): in both cases, something else
// already handled this transition and the caller should simply skip it,
// matching the semantics the old `UPDATE ... WHERE status = $x` SQL gave
// internal/k8s/reconcile.go and internal/k8s/watcher.go.
func (s *Store) UpdateStatusWithCondition(ctx context.Context, id string, current, next model.MonitorStatus) (bool, error) {
	live, err := s.getLive(ctx, id)
	if err != nil {
		if errors.Is(err, ErrMonitorNotFound) {
			return false, nil
		}
		return false, err
	}

	phase, found, err := unstructured.NestedString(live.Object, "status", "phase")
	if err != nil {
		return false, fmt.Errorf("read current phase: %w", err)
	}
	if !found || model.MonitorStatus(phase) != current {
		return false, nil
	}

	if err := unstructured.SetNestedField(live.Object, string(next), "status", "phase"); err != nil {
		return false, fmt.Errorf("set phase: %w", err)
	}

	if _, err := s.dyn.Resource(v1alpha1.GVR).Namespace(s.namespace).UpdateStatus(ctx, live, metav1.UpdateOptions{}); err != nil {
		if k8serrors.IsConflict(err) || k8serrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("update status: %w", err)
	}

	return true, nil
}

// UpdatePodName sets status.podName for the given monitor.
func (s *Store) UpdatePodName(ctx context.Context, id string, podName string) error {
	live, err := s.getLive(ctx, id)
	if err != nil {
		return err
	}
	if err := unstructured.SetNestedField(live.Object, podName, "status", "podName"); err != nil {
		return fmt.Errorf("set pod name: %w", err)
	}
	if _, err := s.dyn.Resource(v1alpha1.GVR).Namespace(s.namespace).UpdateStatus(ctx, live, metav1.UpdateOptions{}); err != nil {
		if k8serrors.IsNotFound(err) {
			return ErrMonitorNotFound
		}
		return fmt.Errorf("update pod name: %w", err)
	}
	return nil
}

// UpdateStats sets the status fields tracked by model.MonitorStats for
// stats.MonitorID.
func (s *Store) UpdateStats(ctx context.Context, stats *model.MonitorStats) error {
	live, err := s.getLive(ctx, stats.MonitorID)
	if err != nil {
		return err
	}

	if err := unstructured.SetNestedField(live.Object, int64(stats.TotalSegments), "status", "totalSegments"); err != nil {
		return fmt.Errorf("set totalSegments: %w", err)
	}
	if err := unstructured.SetNestedField(live.Object, int64(stats.BlackoutEvents), "status", "blackoutEvents"); err != nil {
		return fmt.Errorf("set blackoutEvents: %w", err)
	}
	if err := unstructured.SetNestedField(live.Object, int64(stats.SilenceEvents), "status", "silenceEvents"); err != nil {
		return fmt.Errorf("set silenceEvents: %w", err)
	}
	if err := unstructured.SetNestedField(live.Object, string(stats.VideoHealth), "status", "videoHealth"); err != nil {
		return fmt.Errorf("set videoHealth: %w", err)
	}
	if err := unstructured.SetNestedField(live.Object, string(stats.AudioHealth), "status", "audioHealth"); err != nil {
		return fmt.Errorf("set audioHealth: %w", err)
	}
	if err := unstructured.SetNestedField(live.Object, string(stats.StreamStatus), "status", "streamStatus"); err != nil {
		return fmt.Errorf("set streamStatus: %w", err)
	}
	if stats.LastCheckAt != nil {
		ts := metav1.NewTime(*stats.LastCheckAt)
		b, err := json.Marshal(ts)
		if err != nil {
			return fmt.Errorf("marshal lastCheckAt: %w", err)
		}
		var raw string
		if err := json.Unmarshal(b, &raw); err != nil {
			return fmt.Errorf("unmarshal lastCheckAt: %w", err)
		}
		if err := unstructured.SetNestedField(live.Object, raw, "status", "lastCheckAt"); err != nil {
			return fmt.Errorf("set lastCheckAt: %w", err)
		}
	}

	if _, err := s.dyn.Resource(v1alpha1.GVR).Namespace(s.namespace).UpdateStatus(ctx, live, metav1.UpdateOptions{}); err != nil {
		if k8serrors.IsNotFound(err) {
			return ErrMonitorNotFound
		}
		return fmt.Errorf("update stats: %w", err)
	}
	return nil
}

// Delete removes the StreamMonitor object with the given ID. This is a
// write and always goes live, never through the cache.
func (s *Store) Delete(ctx context.Context, id string) error {
	err := s.dyn.Resource(v1alpha1.GVR).Namespace(s.namespace).Delete(ctx, id, metav1.DeleteOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return ErrMonitorNotFound
		}
		return fmt.Errorf("delete StreamMonitor: %w", err)
	}
	return nil
}

// GetActiveMonitors returns all monitors with active status
// (initializing, waiting, monitoring) from the informer's local cache.
func (s *Store) GetActiveMonitors(ctx context.Context) ([]*model.Monitor, error) {
	var monitors []*model.Monitor
	for _, status := range []model.MonitorStatus{model.StatusInitializing, model.StatusWaiting, model.StatusMonitoring} {
		objs, err := s.informer.GetIndexer().ByIndex(indexPhase, string(status))
		if err != nil {
			return nil, fmt.Errorf("list by phase %s: %w", status, err)
		}
		for _, obj := range objs {
			u, ok := obj.(*unstructured.Unstructured)
			if !ok {
				continue
			}
			sm, err := fromUnstructured(u)
			if err != nil {
				continue
			}
			monitors = append(monitors, toMonitor(sm))
		}
	}
	sort.Slice(monitors, func(i, j int) bool {
		return monitors[i].CreatedAt.Before(monitors[j].CreatedAt)
	})
	return monitors, nil
}

// CountActiveMonitors returns the count of active monitors.
func (s *Store) CountActiveMonitors(ctx context.Context) (int, error) {
	count := 0
	for _, status := range []model.MonitorStatus{model.StatusInitializing, model.StatusWaiting, model.StatusMonitoring} {
		objs, err := s.informer.GetIndexer().ByIndex(indexPhase, string(status))
		if err != nil {
			return 0, fmt.Errorf("list by phase %s: %w", status, err)
		}
		count += len(objs)
	}
	return count, nil
}

// UpdateMonitorParams contains parameters for updating a monitor.
type UpdateMonitorParams struct {
	CallbackURL *string
	Config      *model.MonitorConfig
}

// UpdateMonitor updates an active monitor's spec.callbackURL and/or spec
// config fields. Returns ErrMonitorNotFound if the monitor doesn't exist,
// or ErrMonitorNotActive if it exists but isn't in an active state.
func (s *Store) UpdateMonitor(ctx context.Context, id string, p UpdateMonitorParams) (*model.Monitor, error) {
	live, err := s.getLive(ctx, id)
	if err != nil {
		return nil, err
	}

	phase, found, err := unstructured.NestedString(live.Object, "status", "phase")
	if err != nil {
		return nil, fmt.Errorf("read phase: %w", err)
	}
	if !found || !model.MonitorStatus(phase).IsActive() {
		return nil, ErrMonitorNotActive
	}

	if p.CallbackURL != nil {
		if err := unstructured.SetNestedField(live.Object, *p.CallbackURL, "spec", "callbackURL"); err != nil {
			return nil, fmt.Errorf("set callbackURL: %w", err)
		}
	}
	if p.Config != nil {
		if err := unstructured.SetNestedField(live.Object, int64(p.Config.CheckIntervalSec), "spec", "checkIntervalSec"); err != nil {
			return nil, fmt.Errorf("set checkIntervalSec: %w", err)
		}
		if err := unstructured.SetNestedField(live.Object, int64(p.Config.BlackoutThresholdSec), "spec", "blackoutThresholdSec"); err != nil {
			return nil, fmt.Errorf("set blackoutThresholdSec: %w", err)
		}
		if err := unstructured.SetNestedField(live.Object, int64(p.Config.SilenceThresholdSec), "spec", "silenceThresholdSec"); err != nil {
			return nil, fmt.Errorf("set silenceThresholdSec: %w", err)
		}
		if err := unstructured.SetNestedField(live.Object, p.Config.SilenceDBThreshold, "spec", "silenceDBThreshold"); err != nil {
			return nil, fmt.Errorf("set silenceDBThreshold: %w", err)
		}
		if err := unstructured.SetNestedField(live.Object, int64(p.Config.StartDelayToleranceSec), "spec", "startDelayToleranceSec"); err != nil {
			return nil, fmt.Errorf("set startDelayToleranceSec: %w", err)
		}
		if p.Config.ScheduledStartTime != nil {
			mt := metav1.NewTime(*p.Config.ScheduledStartTime)
			b, err := json.Marshal(mt)
			if err != nil {
				return nil, fmt.Errorf("marshal scheduledStartTime: %w", err)
			}
			var raw string
			if err := json.Unmarshal(b, &raw); err != nil {
				return nil, fmt.Errorf("unmarshal scheduledStartTime: %w", err)
			}
			if err := unstructured.SetNestedField(live.Object, raw, "spec", "scheduledStartTime"); err != nil {
				return nil, fmt.Errorf("set scheduledStartTime: %w", err)
			}
		}
	}

	updated, err := s.dyn.Resource(v1alpha1.GVR).Namespace(s.namespace).Update(ctx, live, metav1.UpdateOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, ErrMonitorNotFound
		}
		return nil, fmt.Errorf("update StreamMonitor: %w", err)
	}

	sm, err := fromUnstructured(updated)
	if err != nil {
		return nil, fmt.Errorf("convert from unstructured: %w", err)
	}
	return toMonitor(sm), nil
}
