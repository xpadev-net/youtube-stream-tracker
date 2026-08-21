package k8s

import (
	"context"
	"testing"
)

func TestPodNamePrefix(t *testing.T) {
	if PodNamePrefix != "stream-monitor-" {
		t.Errorf("PodNamePrefix = %v, want stream-monitor-", PodNamePrefix)
	}
}

func TestLabelConstants(t *testing.T) {
	if LabelApp != "app" {
		t.Errorf("LabelApp = %v, want app", LabelApp)
	}

	if LabelAppValue != "stream-monitor" {
		t.Errorf("LabelAppValue = %v, want stream-monitor", LabelAppValue)
	}

	if LabelMonitorID != "monitor-id" {
		t.Errorf("LabelMonitorID = %v, want monitor-id", LabelMonitorID)
	}
}

func TestPodNameFormat(t *testing.T) {
	monitorID := "mon-0190a5c8-e4b0-7d8a-9c1d-2e3f4a5b6c7d"
	expectedPodName := PodNamePrefix + monitorID

	if expectedPodName != "stream-monitor-mon-0190a5c8-e4b0-7d8a-9c1d-2e3f4a5b6c7d" {
		t.Errorf("Pod name format = %v, want stream-monitor-mon-0190a5c8-e4b0-7d8a-9c1d-2e3f4a5b6c7d", expectedPodName)
	}
}

// TestCreatePodParams tests the structure of CreatePodParams
func TestCreatePodParams(t *testing.T) {
	params := CreatePodParams{
		MonitorID:         "mon-123",
		StreamURL:         "https://www.youtube.com/watch?v=test",
		CallbackURL:       "http://gateway:8080",
		InternalAPIKey:    "internal-key",
		WebhookURL:        "https://example.com/webhook",
		WebhookSigningKey: "signing-key",
		HTTPProxy:         "",
		HTTPSProxy:        "",
		OwnerUID:          "owner-uid",
		OwnerName:         "mon-123",
	}

	if params.MonitorID != "mon-123" {
		t.Errorf("CreatePodParams.MonitorID = %v, want mon-123", params.MonitorID)
	}

	if params.StreamURL != "https://www.youtube.com/watch?v=test" {
		t.Errorf("CreatePodParams.StreamURL = %v, want https://www.youtube.com/watch?v=test", params.StreamURL)
	}

	if params.OwnerName != "mon-123" {
		t.Errorf("CreatePodParams.OwnerName = %v, want mon-123", params.OwnerName)
	}
}

// TestConfigStructure tests the Config structure
func TestConfigStructure(t *testing.T) {
	cfg := Config{
		InCluster:      false,
		KubeConfigPath: "/path/to/kubeconfig",
		Namespace:      "default",
		WorkerImage:    "stream-monitor-worker",
		WorkerImageTag: "latest",
	}

	if cfg.Namespace != "default" {
		t.Errorf("Config.Namespace = %v, want default", cfg.Namespace)
	}

	if cfg.WorkerImage != "stream-monitor-worker" {
		t.Errorf("Config.WorkerImage = %v, want stream-monitor-worker", cfg.WorkerImage)
	}
}

// TestCreateWorkerPodRejectsEmptyOwnerUID verifies that CreateWorkerPod
// fails fast with a clear error when OwnerUID is empty, instead of letting
// the Kubernetes API server reject the Pod create with a less obvious
// validation error (the API server requires ownerReferences[].uid to be
// non-empty). The zero-value Client (nil clientset) is safe to use here
// because the empty-OwnerUID check happens before any use of c.clientset.
func TestCreateWorkerPodRejectsEmptyOwnerUID(t *testing.T) {
	c := &Client{namespace: "default"}
	_, err := c.CreateWorkerPod(context.Background(), CreatePodParams{
		MonitorID: "mon-123",
		OwnerName: "mon-123",
		OwnerUID:  "",
	})
	if err == nil {
		t.Fatal("CreateWorkerPod() with empty OwnerUID = nil error, want an error")
	}
}

// Note: Full integration tests for Kubernetes client would require:
// - A test Kubernetes cluster (e.g., kind)
// - Mock Kubernetes API server
// - Test fixtures for Pod creation/deletion
// These are beyond the scope of basic unit tests and should be part of
// integration test suite with proper test infrastructure.
