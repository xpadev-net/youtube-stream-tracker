// Package v1alpha1 hand-defines the Go types for the StreamMonitor custom
// resource (see helm/stream-monitor/crds/streammonitor-crd.yaml for the
// schema installed into the cluster). No controller-gen/deepcopy-gen is
// used anywhere in this repository, so DeepCopyObject below is written by
// hand via a JSON round-trip rather than generated. These types are never
// registered with a runtime.Scheme or passed to a typed REST client: all
// CRD access goes through the dynamic client as unstructured.Unstructured,
// and these structs exist only as a well-documented intermediate
// representation for converting to/from that unstructured form (see
// internal/k8s/store/convert.go).
package v1alpha1

import (
	"encoding/json"
	"fmt"

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

// GVR identifies the StreamMonitor resource for use with the dynamic client.
var GVR = SchemeGroupVersion.WithResource(Plural)

// StreamMonitorSpec is the desired-state (writable by the API's
// create/patch handlers) part of a StreamMonitor object.
type StreamMonitorSpec struct {
	StreamURL            string       `json:"streamURL"`
	CallbackURL          string       `json:"callbackURL"`
	CheckIntervalSec     int          `json:"checkIntervalSec"`
	BlackoutThresholdSec int          `json:"blackoutThresholdSec"`
	SilenceThresholdSec  int          `json:"silenceThresholdSec"`
	SilenceDBThreshold   float64      `json:"silenceDBThreshold"`
	ScheduledStartTime   *metav1.Time `json:"scheduledStartTime,omitempty"`
	// ScheduledEndTime is defined in the CRD schema now (see the Decision
	// Log in docs/coding-agent/plans/01-streammonitor-crd-migration.md on
	// shipping the full schema up front) but is not yet wired up: nothing
	// in internal/k8s/store writes or reads it, because model.MonitorConfig
	// has no corresponding field yet. Plan 2
	// (docs/coding-agent/plans/02-streammonitor-scheduled-reservations.md)
	// adds that field and the read/write paths together.
	ScheduledEndTime       *metav1.Time          `json:"scheduledEndTime,omitempty"`
	StartDelayToleranceSec int                   `json:"startDelayToleranceSec"`
	Metadata               *runtime.RawExtension `json:"metadata,omitempty"`
}

// StreamMonitorStatus is the live-state (writable by the worker's status
// callbacks, via the status subresource) part of a StreamMonitor object.
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

// StreamMonitor is one monitored YouTube livestream, represented as a
// Kubernetes custom resource of kind "StreamMonitor" in the
// "streamtracker.xpadev.net" API group.
type StreamMonitor struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              StreamMonitorSpec   `json:"spec"`
	Status            StreamMonitorStatus `json:"status,omitempty"`
}

// StreamMonitorList is a list of StreamMonitor objects.
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
//
// Marshal/Unmarshal errors are not possible in practice for this plain,
// non-cyclic struct (its only "unusual" field, *runtime.RawExtension, is
// itself JSON-safe by construction), but the runtime.Object interface
// gives DeepCopyObject no way to report an error to its caller, so a
// failure here would otherwise be silently swallowed and hand back an
// incorrect empty copy. Panic instead: it can only mean a bug in this
// struct's shape, not bad input from a cluster or a user.
func (m *StreamMonitor) DeepCopyObject() runtime.Object {
	out := &StreamMonitor{}
	b, err := json.Marshal(m)
	if err != nil {
		panic(fmt.Sprintf("v1alpha1: StreamMonitor.DeepCopyObject: marshal: %v", err))
	}
	if err := json.Unmarshal(b, out); err != nil {
		panic(fmt.Sprintf("v1alpha1: StreamMonitor.DeepCopyObject: unmarshal: %v", err))
	}
	return out
}

// DeepCopyObject implements runtime.Object via a JSON round-trip; see the
// note on StreamMonitor.DeepCopyObject above.
func (l *StreamMonitorList) DeepCopyObject() runtime.Object {
	out := &StreamMonitorList{}
	b, err := json.Marshal(l)
	if err != nil {
		panic(fmt.Sprintf("v1alpha1: StreamMonitorList.DeepCopyObject: marshal: %v", err))
	}
	if err := json.Unmarshal(b, out); err != nil {
		panic(fmt.Sprintf("v1alpha1: StreamMonitorList.DeepCopyObject: unmarshal: %v", err))
	}
	return out
}
