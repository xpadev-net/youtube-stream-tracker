package k8s

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestExtractPodFailureInfo_TerminatedContainer(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "monitor",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 137,
							Reason:   "OOMKilled",
							Message:  "Container exceeded memory limit",
						},
					},
				},
			},
		},
	}

	exitCode, reason, message := extractPodFailureInfo(pod)

	if exitCode != 137 {
		t.Errorf("exitCode = %d, want 137", exitCode)
	}
	if reason != "OOMKilled" {
		t.Errorf("reason = %q, want OOMKilled", reason)
	}
	if message != "Container exceeded memory limit" {
		t.Errorf("message = %q, want Container exceeded memory limit", message)
	}
}

func TestExtractPodFailureInfo_ExitCode1(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "monitor",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 1,
							Reason:   "Error",
							Message:  "",
						},
					},
				},
			},
		},
	}

	exitCode, reason, message := extractPodFailureInfo(pod)

	if exitCode != 1 {
		t.Errorf("exitCode = %d, want 1", exitCode)
	}
	if reason != "Error" {
		t.Errorf("reason = %q, want Error", reason)
	}
	if message != "" {
		t.Errorf("message = %q, want empty", message)
	}
}

func TestExtractPodFailureInfo_NoTerminatedContainer(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "monitor",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason: "CrashLoopBackOff",
						},
					},
				},
			},
		},
	}

	exitCode, reason, message := extractPodFailureInfo(pod)

	if exitCode != -1 {
		t.Errorf("exitCode = %d, want -1", exitCode)
	}
	if reason != "Unknown" {
		t.Errorf("reason = %q, want Unknown", reason)
	}
	if message != "no terminated container found" {
		t.Errorf("message = %q, want 'no terminated container found'", message)
	}
}

func TestExtractPodFailureInfo_InitContainerFailure(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase:             corev1.PodFailed,
			ContainerStatuses: []corev1.ContainerStatus{},
			InitContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "init",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 2,
							Reason:   "Error",
							Message:  "init failed",
						},
					},
				},
			},
		},
	}

	exitCode, reason, message := extractPodFailureInfo(pod)

	if exitCode != 2 {
		t.Errorf("exitCode = %d, want 2", exitCode)
	}
	if reason != "Error" {
		t.Errorf("reason = %q, want Error", reason)
	}
	if message != "init failed" {
		t.Errorf("message = %q, want 'init failed'", message)
	}
}

func TestHandlePodEvent_RunningPodSkipped(t *testing.T) {
	// A PodWatcher with nil dependencies should not panic on a running pod
	// because handlePodEvent returns early when phase != Failed.
	watcher := &PodWatcher{}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "stream-monitor-mon-123",
			Labels: map[string]string{
				LabelApp:       LabelAppValue,
				LabelMonitorID: "mon-123",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	// This should return immediately without panicking (no DB or K8s calls)
	watcher.handlePodEvent(nil, pod)
}

func TestHandlePodEvent_NoMonitorIDSkipped(t *testing.T) {
	watcher := &PodWatcher{}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "stream-monitor-unknown",
			Labels: map[string]string{},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
		},
	}

	// Should return early because monitorID is empty
	watcher.handlePodEvent(nil, pod)
}
