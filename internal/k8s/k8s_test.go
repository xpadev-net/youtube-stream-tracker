package k8s

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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
	}

	if params.MonitorID != "mon-123" {
		t.Errorf("CreatePodParams.MonitorID = %v, want mon-123", params.MonitorID)
	}

	if params.StreamURL != "https://www.youtube.com/watch?v=test" {
		t.Errorf("CreatePodParams.StreamURL = %v, want https://www.youtube.com/watch?v=test", params.StreamURL)
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

func TestBuildOwnerReference(t *testing.T) {
	uid := types.UID("test-uid-12345")
	ref := BuildOwnerReference("my-deployment", uid)

	if ref.APIVersion != "apps/v1" {
		t.Errorf("APIVersion = %v, want apps/v1", ref.APIVersion)
	}
	if ref.Kind != "Deployment" {
		t.Errorf("Kind = %v, want Deployment", ref.Kind)
	}
	if ref.Name != "my-deployment" {
		t.Errorf("Name = %v, want my-deployment", ref.Name)
	}
	if ref.UID != uid {
		t.Errorf("UID = %v, want %v", ref.UID, uid)
	}
	if ref.BlockOwnerDeletion == nil || !*ref.BlockOwnerDeletion {
		t.Error("BlockOwnerDeletion should be true")
	}
	if ref.Controller != nil && *ref.Controller {
		t.Error("Controller should not be true")
	}
}

func TestBuildOwnerReferences_NilWhenNoOwnerRef(t *testing.T) {
	client := &Client{}

	refs := client.buildOwnerReferences()
	if refs != nil {
		t.Errorf("buildOwnerReferences() = %v, want nil", refs)
	}
}

func TestBuildOwnerReferences_WithOwnerRef(t *testing.T) {
	uid := types.UID("deploy-uid-abc")
	ref := BuildOwnerReference("stream-monitor-gateway", uid)

	client := &Client{}
	client.SetOwnerReference(ref)

	refs := client.buildOwnerReferences()
	if len(refs) != 1 {
		t.Fatalf("buildOwnerReferences() returned %d refs, want 1", len(refs))
	}

	got := refs[0]
	if got.Name != "stream-monitor-gateway" {
		t.Errorf("OwnerReference.Name = %v, want stream-monitor-gateway", got.Name)
	}
	if got.UID != uid {
		t.Errorf("OwnerReference.UID = %v, want %v", got.UID, uid)
	}
	if got.APIVersion != "apps/v1" {
		t.Errorf("OwnerReference.APIVersion = %v, want apps/v1", got.APIVersion)
	}
	if got.Kind != "Deployment" {
		t.Errorf("OwnerReference.Kind = %v, want Deployment", got.Kind)
	}
}

func TestFindOwnerReference(t *testing.T) {
	refs := []metav1.OwnerReference{
		{
			APIVersion: "v1",
			Kind:       "Pod",
			Name:       "some-pod",
			UID:        "pod-uid",
		},
		{
			APIVersion: "apps/v1",
			Kind:       "ReplicaSet",
			Name:       "some-rs",
			UID:        "rs-uid",
		},
		{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
			Name:       "some-deploy",
			UID:        "deploy-uid",
		},
	}

	// Find ReplicaSet
	rs := findOwnerReference(refs, "ReplicaSet")
	if rs == nil {
		t.Fatal("findOwnerReference(ReplicaSet) returned nil")
	}
	if rs.Name != "some-rs" {
		t.Errorf("findOwnerReference(ReplicaSet).Name = %v, want some-rs", rs.Name)
	}

	// Find Deployment
	deploy := findOwnerReference(refs, "Deployment")
	if deploy == nil {
		t.Fatal("findOwnerReference(Deployment) returned nil")
	}
	if deploy.Name != "some-deploy" {
		t.Errorf("findOwnerReference(Deployment).Name = %v, want some-deploy", deploy.Name)
	}

	// Find nonexistent kind
	notFound := findOwnerReference(refs, "StatefulSet")
	if notFound != nil {
		t.Errorf("findOwnerReference(StatefulSet) = %v, want nil", notFound)
	}

	// Empty refs
	empty := findOwnerReference(nil, "Deployment")
	if empty != nil {
		t.Errorf("findOwnerReference(nil) = %v, want nil", empty)
	}
}

// Note: Full integration tests for Kubernetes client would require:
// - A test Kubernetes cluster (e.g., kind)
// - Mock Kubernetes API server
// - Test fixtures for Pod creation/deletion
// These are beyond the scope of basic unit tests and should be part of
// integration test suite with proper test infrastructure.
