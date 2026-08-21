package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"k8s.io/client-go/dynamic"

	"github.com/xpadev-net/youtube-stream-tracker/internal/api"
	"github.com/xpadev-net/youtube-stream-tracker/internal/config"
	"github.com/xpadev-net/youtube-stream-tracker/internal/httpapi"
	"github.com/xpadev-net/youtube-stream-tracker/internal/k8s"
	"github.com/xpadev-net/youtube-stream-tracker/internal/k8s/store"
	"github.com/xpadev-net/youtube-stream-tracker/internal/log"
	"github.com/xpadev-net/youtube-stream-tracker/internal/webhook"
)

func main() {
	// Initialize logger
	if err := log.InitJSON(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer log.Sync()

	log.Info("starting API Gateway")

	// Load configuration
	cfg, err := config.LoadGatewayConfig()
	if err != nil {
		log.Fatal("failed to load configuration", zap.Error(err))
	}

	log.Info("configuration loaded",
		zap.String("environment", cfg.Environment),
		zap.Int("port", cfg.Port),
		zap.String("namespace", cfg.Namespace),
	)

	ctx := context.Background()

	// Create K8s client
	k8sClient, err := k8s.NewClient(k8s.Config{
		InCluster:      cfg.InCluster,
		KubeConfigPath: cfg.KubeConfigPath,
		Namespace:      cfg.Namespace,
		WorkerImage:    cfg.WorkerImage,
		WorkerImageTag: cfg.WorkerImageTag,
	})
	if err != nil {
		log.Fatal("failed to create k8s client", zap.Error(err))
	}

	// Build the StreamMonitor store: a dynamic client for the CRD, backed
	// by an informer cache. Both the typed clientset above and this
	// dynamic client are built from the same *rest.Config.
	restConfig, err := k8s.BuildRESTConfig(k8s.Config{
		InCluster:      cfg.InCluster,
		KubeConfigPath: cfg.KubeConfigPath,
	})
	if err != nil {
		log.Fatal("failed to build kubernetes rest config", zap.Error(err))
	}
	dynClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		log.Fatal("failed to create dynamic client", zap.Error(err))
	}

	monitorStore := store.NewStore(dynClient, cfg.Namespace)
	storeCtx, storeCancel := context.WithCancel(context.Background())
	defer storeCancel()
	go monitorStore.Run(storeCtx)

	syncCtx, syncCancel := context.WithTimeout(ctx, 30*time.Second)
	if !monitorStore.WaitForCacheSync(syncCtx) {
		log.Fatal("timed out waiting for StreamMonitor cache to sync")
	}
	syncCancel()

	webhookSender := webhook.NewSender(cfg.WebhookSigningKey)
	reconciler := k8s.NewReconciler(k8sClient, monitorStore, webhookSender, cfg.ReconcileWebhookURL, cfg.ReconcileTimeout)

	// Create API handler
	handler := api.NewHandler(
		monitorStore,
		cfg.MaxMonitors,
		reconciler,
		cfg.InternalAPIKey,
		cfg.WebhookSigningKey,
		cfg.GatewaySecretsName,
		cfg.GatewayInternalAPIKeySecretKey,
		cfg.GatewayWebhookSigningKeySecretKey,
	)

	// Run reconciliation on boot if enabled
	if cfg.ReconcileOnBoot {
		log.Info("reconciliation on boot enabled, starting reconciliation")
		result, err := reconciler.ReconcileStartup(ctx)
		if err != nil {
			log.Error("reconciliation failed", zap.Error(err))
		} else {
			log.Info("reconciliation completed",
				zap.Int("missing_pods", result.MissingPods),
				zap.Int("zombie_pods", result.ZombiePods),
				zap.Int("orphaned_pods", result.OrphanedPods),
				zap.Int("errors", len(result.Errors)),
			)
		}
	}

	// Start pod failure watcher for real-time webhook notifications
	podWatcher := k8s.NewPodWatcher(k8sClient, monitorStore, webhookSender)
	watcherCtx, watcherCancel := context.WithCancel(context.Background())
	go podWatcher.Run(watcherCtx)

	// Start periodic reconciliation if interval is configured
	var reconcileCancel context.CancelFunc
	if cfg.ReconcileInterval > 0 {
		var reconcileCtx context.Context
		reconcileCtx, reconcileCancel = context.WithCancel(context.Background())
		go reconciler.RunPeriodic(reconcileCtx, cfg.ReconcileInterval)
	}

	// Set Gin mode based on environment
	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	// Create router
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(requestLogger())

	// Health check endpoints (no auth required)
	router.GET("/healthz", healthzHandler())
	router.GET("/readyz", readyzHandler(monitorStore))

	// External API v1 (API key auth required)
	v1 := router.Group("/api/v1")
	v1.Use(httpapi.APIKeyAuth(cfg.APIKey))
	{
		v1.POST("/monitors", httpapi.RateLimit(25, time.Minute), handler.CreateMonitor)
		v1.GET("/monitors", httpapi.RateLimit(100, time.Minute), handler.ListMonitors)
		v1.GET("/monitors/:monitor_id", httpapi.RateLimit(100, time.Minute), handler.GetMonitor)
		v1.PATCH("/monitors/:monitor_id", httpapi.RateLimit(25, time.Minute), handler.PatchMonitor)
		v1.DELETE("/monitors/:monitor_id", handler.DeleteMonitor)
	}

	// Internal API (internal API key auth required)
	internal := router.Group("/internal/v1")
	internal.Use(httpapi.InternalAPIKeyAuth(cfg.InternalAPIKey))
	{
		internal.PUT("/monitors/:monitor_id/status", handler.UpdateMonitorStatus)
		internal.POST("/monitors/:monitor_id/terminate", handler.TerminateMonitor)
	}

	// Create HTTP server
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      router,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	}

	// Start server in a goroutine
	go func() {
		log.Info("starting HTTP server", zap.Int("port", cfg.Port))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("failed to start server", zap.Error(err))
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutting down server")

	// Stop pod failure watcher
	watcherCancel()

	// Stop periodic reconciliation
	if reconcileCancel != nil {
		reconcileCancel()
	}

	// Graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("server forced to shutdown", zap.Error(err))
	}

	log.Info("server stopped")
}

func healthzHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}

func readyzHandler(monitorStore *store.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		readyCtx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
		defer cancel()
		if !monitorStore.WaitForCacheSync(readyCtx) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready", "error": "StreamMonitor cache not synced"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	}
}

func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		c.Next()

		log.Info("request",
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", time.Since(start)),
			zap.String("client_ip", c.ClientIP()),
		)
	}
}
