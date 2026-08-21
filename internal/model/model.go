// Package model defines the pure domain types shared by the Gateway and
// Worker processes. These types have no dependency on how a monitor is
// stored (previously Postgres, now the StreamMonitor Kubernetes custom
// resource) — see internal/k8s/store for the store that persists them.
package model

import (
	"encoding/json"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/types"
)

// MonitorStatus represents the status of a monitor.
type MonitorStatus string

const (
	StatusInitializing MonitorStatus = "initializing"
	StatusWaiting      MonitorStatus = "waiting"
	StatusMonitoring   MonitorStatus = "monitoring"
	StatusScheduled    MonitorStatus = "scheduled"
	StatusCompleted    MonitorStatus = "completed"
	StatusStopped      MonitorStatus = "stopped"
	StatusError        MonitorStatus = "error"
)

// IsActive returns true if the status is considered active.
func (s MonitorStatus) IsActive() bool {
	return s == StatusInitializing || s == StatusWaiting || s == StatusMonitoring
}

// HealthStatus represents health status of video/audio.
type HealthStatus string

const (
	HealthOK      HealthStatus = "ok"
	HealthWarning HealthStatus = "warning"
	HealthError   HealthStatus = "error"
	HealthUnknown HealthStatus = "unknown"
)

// StreamStatus represents the status of the stream.
type StreamStatus string

const (
	StreamStatusUnknown   StreamStatus = "unknown"
	StreamStatusScheduled StreamStatus = "scheduled"
	StreamStatusLive      StreamStatus = "live"
	StreamStatusEnded     StreamStatus = "ended"
)

// MonitorConfig holds the monitoring configuration.
type MonitorConfig struct {
	CheckIntervalSec       int        `json:"check_interval_sec"`
	BlackoutThresholdSec   int        `json:"blackout_threshold_sec"`
	SilenceThresholdSec    int        `json:"silence_threshold_sec"`
	SilenceDBThreshold     float64    `json:"silence_db_threshold"`
	ScheduledStartTime     *time.Time `json:"scheduled_start_time,omitempty"`
	StartDelayToleranceSec int        `json:"start_delay_tolerance_sec"`
}

// Validate validates that config values are within acceptable ranges.
func (c MonitorConfig) Validate() error {
	if c.CheckIntervalSec <= 0 {
		return fmt.Errorf("check_interval_sec must be greater than 0")
	}
	if c.BlackoutThresholdSec < 0 {
		return fmt.Errorf("blackout_threshold_sec must be non-negative")
	}
	if c.SilenceThresholdSec < 0 {
		return fmt.Errorf("silence_threshold_sec must be non-negative")
	}
	if c.SilenceDBThreshold > 0 {
		return fmt.Errorf("silence_db_threshold must be non-positive (in dB)")
	}
	if c.StartDelayToleranceSec < 0 {
		return fmt.Errorf("start_delay_tolerance_sec must be non-negative")
	}
	return nil
}

// DefaultMonitorConfig returns the default monitor configuration.
func DefaultMonitorConfig() MonitorConfig {
	return MonitorConfig{
		CheckIntervalSec:       10,
		BlackoutThresholdSec:   30,
		SilenceThresholdSec:    30,
		SilenceDBThreshold:     -50,
		StartDelayToleranceSec: 300,
	}
}

// Monitor represents a monitoring job.
type Monitor struct {
	ID          string          `json:"id"`
	UID         types.UID       `json:"-"`
	StreamURL   string          `json:"stream_url"`
	CallbackURL string          `json:"callback_url"`
	Config      MonitorConfig   `json:"config"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	Status      MonitorStatus   `json:"status"`
	PodName     *string         `json:"pod_name,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	// UpdatedAt currently always equals CreatedAt: internal/k8s/store's
	// conversion from a StreamMonitor object populates both from
	// metadata.creationTimestamp, because the StreamMonitor CRD schema (see
	// helm/stream-monitor/crds/streammonitor-crd.yaml) has no field
	// tracking a live last-modified time the way the old Postgres
	// `monitors.updated_at` column did — the built-in metadata.
	// resourceVersion changes on every write but is an opaque string, not
	// a timestamp. No API response currently serializes this field. If a
	// real update timestamp is needed later, add a status field (e.g.
	// `status.lastUpdatedAt`) to the CRD and set it on every status/spec
	// write path in internal/k8s/store, keeping in mind that changing the
	// CRD schema requires a manual `kubectl apply` on already-installed
	// clusters (Helm's crds/ directory is not touched by `helm upgrade`).
	UpdatedAt time.Time `json:"updated_at"`
}

// MonitorStats represents monitoring statistics.
type MonitorStats struct {
	MonitorID      string       `json:"monitor_id"`
	TotalSegments  int          `json:"total_segments"`
	BlackoutEvents int          `json:"blackout_events"`
	SilenceEvents  int          `json:"silence_events"`
	LastCheckAt    *time.Time   `json:"last_check_at,omitempty"`
	VideoHealth    HealthStatus `json:"video_health"`
	AudioHealth    HealthStatus `json:"audio_health"`
	StreamStatus   StreamStatus `json:"stream_status"`
}

// MonitorWithStats combines monitor and its stats for API responses.
type MonitorWithStats struct {
	Monitor
	Stats *MonitorStats `json:"statistics,omitempty"`
}
