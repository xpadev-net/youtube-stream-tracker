package k8s

import (
	"context"
	"encoding/json"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/watch"

	"go.uber.org/zap"

	"github.com/xpadev-net/youtube-stream-tracker/internal/db"
	"github.com/xpadev-net/youtube-stream-tracker/internal/log"
	"github.com/xpadev-net/youtube-stream-tracker/internal/webhook"
)

// PodWatcher watches Kubernetes worker pods for failures and sends webhooks.
type PodWatcher struct {
	k8sClient     *Client
	repo          *db.MonitorRepository
	webhookSender *webhook.Sender
}

// NewPodWatcher creates a new PodWatcher.
func NewPodWatcher(k8sClient *Client, repo *db.MonitorRepository, webhookSender *webhook.Sender) *PodWatcher {
	return &PodWatcher{
		k8sClient:     k8sClient,
		repo:          repo,
		webhookSender: webhookSender,
	}
}

// Run starts the pod watch loop. It blocks until the context is cancelled.
func (w *PodWatcher) Run(ctx context.Context) {
	log.Info("starting pod failure watcher")

	for {
		if ctx.Err() != nil {
			log.Info("pod failure watcher stopped")
			return
		}

		if err := w.watchLoop(ctx); err != nil {
			log.Warn("pod watch loop error, restarting", zap.Error(err))
		}

		// Wait before reconnecting
		select {
		case <-ctx.Done():
			log.Info("pod failure watcher stopped")
			return
		case <-time.After(1 * time.Second):
		}
	}
}

// watchLoop performs a single list-then-watch cycle.
func (w *PodWatcher) watchLoop(ctx context.Context) error {
	// List current pods to get the resource version
	podList, err := w.k8sClient.listWorkerPodList(ctx)
	if err != nil {
		return err
	}

	resourceVersion := podList.ResourceVersion

	// Process any already-failed pods from the list
	for i := range podList.Items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		w.handlePodEvent(ctx, &podList.Items[i])
	}

	// Start watching from the list's resource version
	watcher, err := w.k8sClient.WatchWorkerPods(ctx, resourceVersion)
	if err != nil {
		return err
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-watcher.ResultChan():
			if !ok {
				log.Warn("pod watch channel closed")
				return nil
			}

			if event.Type != watch.Modified {
				continue
			}

			pod, ok := event.Object.(*corev1.Pod)
			if !ok {
				continue
			}

			w.handlePodEvent(ctx, pod)
		}
	}
}

// handlePodEvent processes a pod event and sends a webhook if the pod has failed.
func (w *PodWatcher) handlePodEvent(ctx context.Context, pod *corev1.Pod) {
	if pod.Status.Phase != corev1.PodFailed {
		return
	}

	monitorID := GetPodMonitorID(pod)
	if monitorID == "" {
		return
	}

	monitor, err := w.repo.GetByID(ctx, monitorID)
	if err != nil {
		log.Warn("failed to get monitor for failed pod",
			zap.String("monitor_id", monitorID),
			zap.String("pod_name", pod.Name),
			zap.Error(err),
		)
		return
	}

	// Skip if monitor is already in a terminal state
	if !monitor.Status.IsActive() {
		return
	}

	// Atomically update status to error
	updated, err := w.repo.UpdateStatusWithCondition(ctx, monitorID, monitor.Status, db.StatusError)
	if err != nil {
		log.Error("failed to update monitor status for failed pod",
			zap.String("monitor_id", monitorID),
			zap.Error(err),
		)
		return
	}

	if !updated {
		// Another process (e.g. reconciler) already handled this
		return
	}

	// Extract failure info from pod
	exitCode, reason, message := extractPodFailureInfo(pod)

	log.Info("worker pod failed, sending webhook",
		zap.String("monitor_id", monitorID),
		zap.String("pod_name", pod.Name),
		zap.Int32("exit_code", exitCode),
		zap.String("reason", reason),
	)

	// Send webhook to the monitor's callback URL
	w.sendFailureWebhook(ctx, monitor, pod.Name, exitCode, reason, message)

	// Clean up the failed pod
	if err := w.k8sClient.DeleteWorkerPod(ctx, monitorID); err != nil {
		log.Error("failed to delete failed worker pod",
			zap.String("monitor_id", monitorID),
			zap.Error(err),
		)
	}
}

// sendFailureWebhook sends a monitor.error webhook and records the event in the DB.
func (w *PodWatcher) sendFailureWebhook(ctx context.Context, monitor *db.Monitor, podName string, exitCode int32, reason, message string) {
	if w.webhookSender == nil || monitor.CallbackURL == "" {
		return
	}

	data := map[string]interface{}{
		"reason":    "pod_failure",
		"exit_code": exitCode,
		"message":   message,
		"pod_name":  podName,
	}
	if reason != "" {
		data["termination_reason"] = reason
	}

	payload := &webhook.Payload{
		EventType: webhook.EventMonitorError,
		MonitorID: monitor.ID,
		StreamURL: monitor.StreamURL,
		Timestamp: time.Now(),
		Data:      data,
		Metadata:  monitor.Metadata,
	}

	sendCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result := w.webhookSender.Send(sendCtx, monitor.CallbackURL, payload)

	// Record event in DB for audit trail
	whStatus := db.WebhookStatusSent
	var whError *string
	var sentAt *time.Time
	if result.Success {
		now := time.Now()
		sentAt = &now
	} else {
		whStatus = db.WebhookStatusFailed
		whError = &result.Error
		log.Warn("failed to send pod failure webhook",
			zap.String("monitor_id", monitor.ID),
			zap.String("error", result.Error),
		)
	}

	payloadJSON, _ := json.Marshal(data)
	event := &db.MonitorEvent{
		MonitorID:        monitor.ID,
		EventType:        string(webhook.EventMonitorError),
		Payload:          payloadJSON,
		WebhookStatus:    whStatus,
		WebhookAttempts:  result.Attempts,
		WebhookLastError: whError,
		SentAt:           sentAt,
	}

	if err := w.repo.CreateEvent(ctx, event); err != nil {
		log.Warn("failed to record pod failure webhook event",
			zap.String("monitor_id", monitor.ID),
			zap.Error(err),
		)
	}
}

// extractPodFailureInfo extracts exit code, reason, and message from a failed pod.
func extractPodFailureInfo(pod *corev1.Pod) (exitCode int32, reason, message string) {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Terminated != nil {
			return cs.State.Terminated.ExitCode, cs.State.Terminated.Reason, cs.State.Terminated.Message
		}
	}
	// Fallback: check init container statuses
	for _, cs := range pod.Status.InitContainerStatuses {
		if cs.State.Terminated != nil {
			return cs.State.Terminated.ExitCode, cs.State.Terminated.Reason, cs.State.Terminated.Message
		}
	}
	return -1, "Unknown", "no terminated container found"
}
