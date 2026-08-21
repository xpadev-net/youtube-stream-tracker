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
func (m *StreamMonitor) DeepCopyObject() runtime.Object {
	out := &StreamMonitor{}
	b, _ := json.Marshal(m)
	_ = json.Unmarshal(b, out)
	return out
}

// DeepCopyObject implements runtime.Object via a JSON round-trip; see the
// note on StreamMonitor.DeepCopyObject above.
func (l *StreamMonitorList) DeepCopyObject() runtime.Object {
	out := &StreamMonitorList{}
	b, _ := json.Marshal(l)
	_ = json.Unmarshal(b, out)
	return out
}
