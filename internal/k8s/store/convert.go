package store

import (
	"encoding/json"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/xpadev-net/youtube-stream-tracker/internal/k8s/apis/streamtracker/v1alpha1"
	"github.com/xpadev-net/youtube-stream-tracker/internal/model"
)

// fromUnstructured converts an *unstructured.Unstructured object (as
// returned by the dynamic client or the informer cache) into the
// hand-written v1alpha1.StreamMonitor Go struct, using the JSON tags on
// that struct as the single source of truth for the field mapping.
func fromUnstructured(u *unstructured.Unstructured) (*v1alpha1.StreamMonitor, error) {
	var sm v1alpha1.StreamMonitor
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &sm); err != nil {
		return nil, err
	}
	return &sm, nil
}

// toUnstructured converts a v1alpha1.StreamMonitor Go struct into an
// *unstructured.Unstructured object suitable for the dynamic client.
func toUnstructured(sm *v1alpha1.StreamMonitor) (*unstructured.Unstructured, error) {
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(sm)
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: obj}, nil
}

// toMonitor converts a StreamMonitor's spec/metadata into the pure
// model.Monitor domain type used by the rest of the codebase.
func toMonitor(sm *v1alpha1.StreamMonitor) *model.Monitor {
	m := &model.Monitor{
		ID:          sm.Name,
		UID:         sm.UID,
		StreamURL:   sm.Spec.StreamURL,
		CallbackURL: sm.Spec.CallbackURL,
		Status:      sm.Status.Phase,
		CreatedAt:   sm.CreationTimestamp.Time,
		UpdatedAt:   sm.CreationTimestamp.Time,
		Config: model.MonitorConfig{
			CheckIntervalSec:       sm.Spec.CheckIntervalSec,
			BlackoutThresholdSec:   sm.Spec.BlackoutThresholdSec,
			SilenceThresholdSec:    sm.Spec.SilenceThresholdSec,
			SilenceDBThreshold:     sm.Spec.SilenceDBThreshold,
			StartDelayToleranceSec: sm.Spec.StartDelayToleranceSec,
		},
	}

	if sm.Spec.ScheduledStartTime != nil {
		t := sm.Spec.ScheduledStartTime.Time
		m.Config.ScheduledStartTime = &t
	}

	if sm.Spec.Metadata != nil && len(sm.Spec.Metadata.Raw) > 0 {
		m.Metadata = json.RawMessage(sm.Spec.Metadata.Raw)
	}

	if sm.Status.PodName != "" {
		podName := sm.Status.PodName
		m.PodName = &podName
	}

	return m
}

// toStats converts a StreamMonitor's status into the pure model.MonitorStats
// domain type used by the rest of the codebase.
func toStats(sm *v1alpha1.StreamMonitor) *model.MonitorStats {
	stats := &model.MonitorStats{
		MonitorID:      sm.Name,
		TotalSegments:  sm.Status.TotalSegments,
		BlackoutEvents: sm.Status.BlackoutEvents,
		SilenceEvents:  sm.Status.SilenceEvents,
		VideoHealth:    sm.Status.VideoHealth,
		AudioHealth:    sm.Status.AudioHealth,
		StreamStatus:   sm.Status.StreamStatus,
	}
	if sm.Status.LastCheckAt != nil {
		t := sm.Status.LastCheckAt.Time
		stats.LastCheckAt = &t
	}
	return stats
}

// metav1TimePtr converts a *time.Time (as used by model.MonitorConfig) into
// a *metav1.Time (as used by v1alpha1.StreamMonitorSpec), or nil.
func metav1TimePtr(t *time.Time) *metav1.Time {
	if t == nil {
		return nil
	}
	mt := metav1.NewTime(*t)
	return &mt
}
