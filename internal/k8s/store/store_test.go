package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/xpadev-net/youtube-stream-tracker/internal/k8s/apis/streamtracker/v1alpha1"
	"github.com/xpadev-net/youtube-stream-tracker/internal/model"
)

// newTestStore builds a Store backed by the fake dynamic client (no real
// cluster involved) and waits for its informer's initial cache sync.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{
		v1alpha1.GVR: "StreamMonitorList",
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind)
	s := NewStore(dyn, "default")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go s.Run(ctx)

	syncCtx, syncCancel := context.WithTimeout(ctx, 5*time.Second)
	defer syncCancel()
	if !s.WaitForCacheSync(syncCtx) {
		t.Fatal("timed out waiting for cache sync")
	}
	return s
}

// waitInCache polls the informer's cache until id appears (the fake
// client's watch delivers events asynchronously, just like a real cluster).
func waitInCache(t *testing.T, s *Store, id string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := s.GetByID(context.Background(), id); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s to appear in cache", id)
}

func TestCreateAndGetByID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m, err := s.Create(ctx, CreateMonitorParams{
		ID:           "mon-1",
		StreamURL:    "https://www.youtube.com/watch?v=abc",
		CallbackURL:  "https://example.com/cb",
		Config:       model.DefaultMonitorConfig(),
		InitialPhase: model.StatusInitializing,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if m.Status != model.StatusInitializing {
		t.Fatalf("Status = %v, want %v", m.Status, model.StatusInitializing)
	}
	if m.StreamURL != "https://www.youtube.com/watch?v=abc" {
		t.Fatalf("StreamURL = %v, want the stream URL passed to Create", m.StreamURL)
	}

	waitInCache(t, s, "mon-1")

	got, err := s.GetByID(ctx, "mon-1")
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.StreamURL != m.StreamURL {
		t.Errorf("StreamURL = %v, want %v", got.StreamURL, m.StreamURL)
	}
	if got.Status != model.StatusInitializing {
		t.Errorf("Status = %v, want %v (the status subresource write from Create must be visible)", got.Status, model.StatusInitializing)
	}
}

func TestGetByIDNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetByID(context.Background(), "does-not-exist")
	if !errors.Is(err, ErrMonitorNotFound) {
		t.Fatalf("GetByID() error = %v, want ErrMonitorNotFound", err)
	}
}

func TestCreateDuplicateStreamURL(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	params := CreateMonitorParams{
		ID:           "mon-1",
		StreamURL:    "https://www.youtube.com/watch?v=dup",
		CallbackURL:  "https://example.com/cb",
		Config:       model.DefaultMonitorConfig(),
		InitialPhase: model.StatusInitializing,
	}
	if _, err := s.Create(ctx, params); err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	waitInCache(t, s, "mon-1")

	params.ID = "mon-2"
	_, err := s.Create(ctx, params)
	if !errors.Is(err, ErrDuplicateMonitor) {
		t.Fatalf("second Create() error = %v, want ErrDuplicateMonitor", err)
	}
}

func TestCreateSameStreamURLAfterTerminal(t *testing.T) {
	// A monitor in a terminal (non-active) phase should not block a new
	// Create for the same stream URL.
	s := newTestStore(t)
	ctx := context.Background()

	params := CreateMonitorParams{
		ID:           "mon-1",
		StreamURL:    "https://www.youtube.com/watch?v=terminal",
		CallbackURL:  "https://example.com/cb",
		Config:       model.DefaultMonitorConfig(),
		InitialPhase: model.StatusInitializing,
	}
	if _, err := s.Create(ctx, params); err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	waitInCache(t, s, "mon-1")

	if err := s.UpdateStatus(ctx, "mon-1", model.StatusCompleted); err != nil {
		t.Fatalf("UpdateStatus() error = %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, err := s.GetByID(ctx, "mon-1")
		if err == nil && got.Status == model.StatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	params.ID = "mon-2"
	if _, err := s.Create(ctx, params); err != nil {
		t.Fatalf("second Create() for same stream URL after terminal status = %v, want nil", err)
	}
}

func TestUpdateStatusWithCondition(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.Create(ctx, CreateMonitorParams{
		ID:           "mon-1",
		StreamURL:    "https://www.youtube.com/watch?v=cas",
		CallbackURL:  "https://example.com/cb",
		Config:       model.DefaultMonitorConfig(),
		InitialPhase: model.StatusInitializing,
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	updated, err := s.UpdateStatusWithCondition(ctx, "mon-1", model.StatusInitializing, model.StatusWaiting)
	if err != nil {
		t.Fatalf("UpdateStatusWithCondition() error = %v", err)
	}
	if !updated {
		t.Fatal("expected update to succeed when current phase matches")
	}

	// Someone else already changed the phase away from "initializing" (it's
	// now "waiting"), so this condition no longer matches: expect a no-op,
	// not an error — mirrors the old `UPDATE ... WHERE status = $x` SQL.
	updated, err = s.UpdateStatusWithCondition(ctx, "mon-1", model.StatusInitializing, model.StatusMonitoring)
	if err != nil {
		t.Fatalf("UpdateStatusWithCondition() error = %v", err)
	}
	if updated {
		t.Fatal("expected update to be skipped due to condition mismatch")
	}

	// UpdateStatusWithCondition writes live; GetByID reads the informer's
	// cache, which may take a moment to observe the write. Poll for it.
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, err := s.GetByID(ctx, "mon-1")
		if err != nil {
			t.Fatalf("GetByID() error = %v", err)
		}
		if got.Status == model.StatusWaiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Status = %v, want %v (the skipped update must not have applied)", got.Status, model.StatusWaiting)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestUpdateStatusWithConditionNotFound(t *testing.T) {
	s := newTestStore(t)
	updated, err := s.UpdateStatusWithCondition(context.Background(), "does-not-exist", model.StatusInitializing, model.StatusWaiting)
	if err != nil {
		t.Fatalf("UpdateStatusWithCondition() error = %v, want nil", err)
	}
	if updated {
		t.Fatal("expected updated=false for a monitor that doesn't exist")
	}
}

func TestDeleteNotFound(t *testing.T) {
	s := newTestStore(t)
	err := s.Delete(context.Background(), "does-not-exist")
	if !errors.Is(err, ErrMonitorNotFound) {
		t.Fatalf("Delete() error = %v, want ErrMonitorNotFound", err)
	}
}
