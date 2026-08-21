package k8s

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"go.uber.org/zap"

	"github.com/xpadev-net/youtube-stream-tracker/internal/k8s/store"
	"github.com/xpadev-net/youtube-stream-tracker/internal/log"
	"github.com/xpadev-net/youtube-stream-tracker/internal/model"
	"github.com/xpadev-net/youtube-stream-tracker/internal/webhook"
)

// redactURL returns scheme://host/path, stripping query params, fragments, and userinfo.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid-url>"
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.User = nil
	return u.String()
}

// ReconcileResult contains the result of a reconciliation.
type ReconcileResult struct {
	MissingPods  int
	ZombiePods   int
	OrphanedPods int
	Errors       []string
	StartTime    time.Time
	EndTime      time.Time
	TimedOut     bool
}

// Reconciler handles reconciliation between the StreamMonitor store and K8s state.
type Reconciler struct {
	k8sClient                *Client
	repo                     *store.Store
	webhookSender            *webhook.Sender
	reconciliationWebhookURL string
	timeout                  time.Duration
}

// NewReconciler creates a new reconciler.
func NewReconciler(k8sClient *Client, repo *store.Store, webhookSender *webhook.Sender, reconciliationWebhookURL string, timeout time.Duration) *Reconciler {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &Reconciler{
		k8sClient:                k8sClient,
		repo:                     repo,
		webhookSender:            webhookSender,
		reconciliationWebhookURL: reconciliationWebhookURL,
		timeout:                  timeout,
	}
}

// RunPeriodic runs reconciliation on a periodic interval until the context is cancelled.
func (r *Reconciler) RunPeriodic(ctx context.Context, interval time.Duration) {
	log.Info("starting periodic reconciliation", zap.Duration("interval", interval))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("periodic reconciliation stopped")
			return
		case <-ticker.C:
			result, err := r.ReconcileStartup(context.Background())
			if err != nil {
				log.Error("periodic reconciliation failed", zap.Error(err))
				continue
			}
			if result.MissingPods > 0 || result.ZombiePods > 0 || result.OrphanedPods > 0 || len(result.Errors) > 0 {
				log.Info("periodic reconciliation found issues",
					zap.Int("missing_pods", result.MissingPods),
					zap.Int("zombie_pods", result.ZombiePods),
					zap.Int("orphaned_pods", result.OrphanedPods),
					zap.Int("errors", len(result.Errors)),
				)
			}
		}
	}
}

// ReconcileStartup performs reconciliation at Gateway startup.
// This is idempotent and safe to run multiple times.
func (r *Reconciler) ReconcileStartup(ctx context.Context) (*ReconcileResult, error) {
	result := &ReconcileResult{
		StartTime: time.Now(),
	}

	// Create context with timeout
	reconcileCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	log.Info("starting reconciliation",
		zap.Duration("timeout", r.timeout),
	)

	// Get snapshot of store state (all active monitors)
	activeMonitors, err := r.repo.GetActiveMonitors(reconcileCtx)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("get active monitors: %v", err))
		log.Error("failed to get active monitors", zap.Error(err))
		return result, nil // Don't block startup
	}

	// Get snapshot of K8s state (all worker pods)
	pods, err := r.k8sClient.ListWorkerPods(reconcileCtx)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("list worker pods: %v", err))
		log.Error("failed to list worker pods", zap.Error(err))
		return result, nil // Don't block startup
	}

	// Build maps for quick lookup
	storeMonitors := make(map[string]*model.Monitor)
	for _, m := range activeMonitors {
		storeMonitors[m.ID] = m
	}

	podMonitors := make(map[string]bool)
	for _, p := range pods {
		monitorID := GetPodMonitorID(&p)
		if monitorID != "" {
			podMonitors[monitorID] = true
		}
	}

	// Check for context timeout
	select {
	case <-reconcileCtx.Done():
		result.TimedOut = true
		log.Warn("reconciliation timed out")
		return result, nil
	default:
	}

	// Find missing pods: monitors in the store but no pod
	for monitorID, monitor := range storeMonitors {
		if !podMonitors[monitorID] {
			result.MissingPods++
			log.Warn("missing pod for active monitor",
				zap.String("monitor_id", monitorID),
				zap.String("status", string(monitor.Status)),
			)

			// Update monitor status to error
			updated, err := r.repo.UpdateStatusWithCondition(
				reconcileCtx,
				monitorID,
				monitor.Status,
				model.StatusError,
			)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("update status for %s: %v", monitorID, err))
				continue
			}

			if updated {
				// Send monitor.error webhook
				r.sendErrorWebhook(monitor, "reconciliation_mismatch", "Pod not found during reconciliation")
			}
		}
	}

	// Find zombie pods: pods for monitors that are stopped/deleted/error
	for _, p := range pods {
		monitorID := GetPodMonitorID(&p)
		if monitorID == "" {
			continue
		}

		monitor, exists := storeMonitors[monitorID]
		if !exists {
			// Orphaned pod: no corresponding monitor in the store
			result.OrphanedPods++
			log.Warn("orphaned pod found",
				zap.String("pod_name", p.Name),
				zap.String("monitor_id", monitorID),
			)

			// Delete the orphaned pod
			if err := r.k8sClient.DeleteWorkerPod(reconcileCtx, monitorID); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("delete orphaned pod %s: %v", monitorID, err))
			}
			continue
		}

		// Check for zombie pods (status is stopped or error, but pod exists)
		if monitor.Status == model.StatusStopped || monitor.Status == model.StatusError || monitor.Status == model.StatusCompleted {
			result.ZombiePods++
			log.Warn("zombie pod found",
				zap.String("pod_name", p.Name),
				zap.String("monitor_id", monitorID),
				zap.String("status", string(monitor.Status)),
			)

			// Delete the zombie pod
			if err := r.k8sClient.DeleteWorkerPod(reconcileCtx, monitorID); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("delete zombie pod %s: %v", monitorID, err))
			}
		}
	}

	result.EndTime = time.Now()

	log.Info("reconciliation completed",
		zap.Int("missing_pods", result.MissingPods),
		zap.Int("zombie_pods", result.ZombiePods),
		zap.Int("orphaned_pods", result.OrphanedPods),
		zap.Int("errors", len(result.Errors)),
		zap.Duration("duration", result.EndTime.Sub(result.StartTime)),
	)

	return result, nil
}

// sendErrorWebhook sends a monitor.error webhook to both the operator URL
// and the monitor's registered callback URL. Delivery outcome is only
// logged locally (via zap) — there is no audit-log store anymore.
func (r *Reconciler) sendErrorWebhook(monitor *model.Monitor, reason, message string) {
	data := map[string]interface{}{
		"reason":                reason,
		"reconciliation_action": "mark_as_error_missing_pod",
		"previous_status":       string(monitor.Status),
		"observed_state": map[string]interface{}{
			"pod_exists": false,
			"db_status":  string(monitor.Status),
		},
		"error_details": message,
	}

	payload := &webhook.Payload{
		EventType: webhook.EventMonitorError,
		MonitorID: monitor.ID,
		StreamURL: monitor.StreamURL,
		Timestamp: time.Now(),
		Data:      data,
		Metadata:  monitor.Metadata,
	}

	// Send to operator webhook (fire-and-forget)
	if r.webhookSender != nil && r.reconciliationWebhookURL != "" {
		go func() {
			sendCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			result := r.webhookSender.Send(sendCtx, r.reconciliationWebhookURL, payload)
			if !result.Success {
				log.Warn("failed to send error webhook during reconciliation",
					zap.String("monitor_id", monitor.ID),
					zap.String("error", result.Error),
				)
			}
		}()
	}

	if r.webhookSender == nil || monitor.CallbackURL == "" {
		return
	}

	// Send webhook to the monitor's callback URL
	go func() {
		sendCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		result := r.webhookSender.Send(sendCtx, monitor.CallbackURL, payload)
		if result.Success {
			log.Info("reconciliation error webhook delivered",
				zap.String("monitor_id", monitor.ID),
			)
		} else {
			log.Warn("failed to send reconciliation error webhook to callback URL",
				zap.String("monitor_id", monitor.ID),
				zap.String("callback_url", redactURL(monitor.CallbackURL)),
				zap.String("error", result.Error),
			)
		}
	}()
}

// CreateMonitorPod creates a pod for a monitor and updates its podName status.
func (r *Reconciler) CreateMonitorPod(ctx context.Context, monitor *model.Monitor, internalAPIKey, webhookSigningKey, secretsName, internalKey, signingKey string) error {
	gatewayBaseURL, err := r.k8sClient.GetGatewayInternalBaseURL(ctx)
	if err != nil {
		return fmt.Errorf("get gateway internal base URL: %w", err)
	}

	params := CreatePodParams{
		MonitorID:             monitor.ID,
		StreamURL:             monitor.StreamURL,
		CallbackURL:           gatewayBaseURL,
		InternalAPIKey:        internalAPIKey,
		WebhookURL:            monitor.CallbackURL,
		WebhookSigningKey:     webhookSigningKey,
		Config:                &monitor.Config,
		Metadata:              monitor.Metadata,
		SecretsName:           secretsName,
		InternalAPIKeyName:    internalKey,
		WebhookSigningKeyName: signingKey,
		OwnerUID:              monitor.UID,
		OwnerName:             monitor.ID,
	}

	pod, err := r.k8sClient.CreateWorkerPod(ctx, params)
	if err != nil {
		return fmt.Errorf("create worker pod: %w", err)
	}

	// Update podName status
	if err := r.repo.UpdatePodName(ctx, monitor.ID, pod.Name); err != nil {
		log.Error("failed to update podName status",
			zap.String("monitor_id", monitor.ID),
			zap.Error(err),
		)
	}

	return nil
}

// DeleteMonitorPod deletes the pod for a monitor.
func (r *Reconciler) DeleteMonitorPod(ctx context.Context, monitorID string) error {
	return r.k8sClient.DeleteWorkerPod(ctx, monitorID)
}
