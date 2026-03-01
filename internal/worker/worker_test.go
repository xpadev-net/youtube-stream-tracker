package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/xpadev-net/youtube-stream-tracker/internal/config"
	"github.com/xpadev-net/youtube-stream-tracker/internal/db"
	"github.com/xpadev-net/youtube-stream-tracker/internal/ffmpeg"
	"github.com/xpadev-net/youtube-stream-tracker/internal/manifest"
	"github.com/xpadev-net/youtube-stream-tracker/internal/webhook"
	"github.com/xpadev-net/youtube-stream-tracker/internal/ytdlp"
)

type stubYtDlpClient struct {
	isLive bool
	info   *ytdlp.StreamInfo
	err    error
}

func (s *stubYtDlpClient) IsStreamLive(ctx context.Context, streamURL string) (bool, *ytdlp.StreamInfo, error) {
	return s.isLive, s.info, s.err
}

func (s *stubYtDlpClient) GetManifestURL(ctx context.Context, streamURL string) (string, error) {
	return "https://example.com/manifest.m3u8", nil
}

type captureWebhookSender struct {
	calls []*webhook.Payload
	urls  []string
}

func (c *captureWebhookSender) Send(ctx context.Context, url string, payload *webhook.Payload) *webhook.SendResult {
	c.calls = append(c.calls, payload)
	c.urls = append(c.urls, url)
	return &webhook.SendResult{Success: true, Attempts: 1}
}

type failingWebhookSender struct{}

func (f *failingWebhookSender) Send(ctx context.Context, url string, payload *webhook.Payload) *webhook.SendResult {
	return &webhook.SendResult{Success: false, Attempts: 4, Error: "failed"}
}

type spyCallbackClient struct {
	terminateCalled bool
	terminateReason string
}

func (s *spyCallbackClient) ReportStatus(ctx context.Context, monitorID string, status db.MonitorStatus, update *StatusUpdate) error {
	return nil
}

func (s *spyCallbackClient) TerminateMonitor(ctx context.Context, monitorID string, reason string) error {
	s.terminateCalled = true
	s.terminateReason = reason
	return nil
}

func (s *spyCallbackClient) ReportWebhookEvent(ctx context.Context, monitorID string, event *WebhookEventReport) error {
	return nil
}

func TestWaitingModeSendsStreamEnded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := &config.WorkerConfig{
		MonitorID:                  "mon-test",
		StreamURL:                  "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		CallbackURL:                server.URL,
		InternalAPIKey:             "internal-key",
		WebhookURL:                 "http://example.com",
		WebhookSigningKey:          "signing-key",
		WaitingModeInitialInterval: 1 * time.Millisecond,
		WaitingModeDelayedInterval: 1 * time.Millisecond,
		ManifestFetchTimeout:       1 * time.Second,
		ManifestRefreshInterval:    1 * time.Second,
		SegmentFetchTimeout:        1 * time.Second,
		SegmentMaxBytes:            1024,
		AnalysisInterval:           1 * time.Second,
		BlackoutThreshold:          1 * time.Second,
		SilenceThreshold:           1 * time.Second,
		SilenceDBThreshold:         -50,
		DelayThreshold:             1 * time.Second,
		FFmpegPath:                 "ffmpeg",
		FFprobePath:                "ffprobe",
		YtDlpPath:                  "yt-dlp",
		StreamlinkPath:             "streamlink",
	}

	ytdlpClient := &stubYtDlpClient{
		isLive: false,
		info: &ytdlp.StreamInfo{
			LiveStatus: "was_live",
		},
	}
	sender := &captureWebhookSender{}
	callbackClient := NewCallbackClient(server.URL, cfg.InternalAPIKey)
	worker := NewWorkerWithDeps(cfg, ytdlpClient, nil, nil, sender, callbackClient)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if err := worker.waitingMode(ctx); err != nil {
		t.Fatalf("waitingMode returned error: %v", err)
	}

	if len(sender.calls) != 1 {
		t.Fatalf("expected 1 webhook call, got %d", len(sender.calls))
	}

	payload := sender.calls[0]
	if payload.EventType != webhook.EventStreamEnded {
		t.Fatalf("event_type = %v, want %v", payload.EventType, webhook.EventStreamEnded)
	}

	reason, ok := payload.Data["reason"]
	if !ok {
		t.Fatalf("expected reason in payload data")
	}
	if reason != "was_live" {
		t.Fatalf("reason = %v, want was_live", reason)
	}
}

func TestWebhookFailureDeletesJob(t *testing.T) {
	cfg := &config.WorkerConfig{
		MonitorID:                  "mon-test",
		StreamURL:                  "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		CallbackURL:                "http://example.com",
		InternalAPIKey:             "internal-key",
		WebhookURL:                 "http://example.com",
		WebhookSigningKey:          "signing-key",
		WaitingModeInitialInterval: 1 * time.Millisecond,
		WaitingModeDelayedInterval: 1 * time.Millisecond,
		ManifestFetchTimeout:       1 * time.Second,
		ManifestRefreshInterval:    1 * time.Second,
		SegmentFetchTimeout:        1 * time.Second,
		SegmentMaxBytes:            1024,
		AnalysisInterval:           1 * time.Second,
		BlackoutThreshold:          1 * time.Second,
		SilenceThreshold:           1 * time.Second,
		SilenceDBThreshold:         -50,
		DelayThreshold:             1 * time.Second,
		FFmpegPath:                 "ffmpeg",
		FFprobePath:                "ffprobe",
		YtDlpPath:                  "yt-dlp",
		StreamlinkPath:             "streamlink",
	}

	spyCallback := &spyCallbackClient{}
	worker := NewWorkerWithDeps(cfg, &stubYtDlpClient{}, nil, nil, &failingWebhookSender{}, spyCallback)

	worker.sendWebhook(context.Background(), webhook.EventStreamStarted, nil)

	if !spyCallback.terminateCalled {
		t.Fatalf("expected terminate to be called")
	}
	if spyCallback.terminateReason != "webhook_delivery_failed" {
		t.Fatalf("terminate reason = %s, want webhook_delivery_failed", spyCallback.terminateReason)
	}
}

type stubManifestParser struct{}

func (s *stubManifestParser) GetLatestSegment(ctx context.Context, manifestURL string) (*manifest.Segment, error) {
	return &manifest.Segment{
		URL:       "http://example.com/segment.ts",
		Duration:  1.0,
		Sequence:  1,
		MediaType: "hls",
	}, nil
}

func (s *stubManifestParser) IsEndList(ctx context.Context, manifestURL string) (bool, error) {
	return false, nil
}

func (s *stubManifestParser) FetchSegment(ctx context.Context, segmentURL string) ([]byte, error) {
	return []byte("data"), nil
}

type stubAnalyzer struct{}

func (s *stubAnalyzer) EnsureTmpDir() error {
	return nil
}

func (s *stubAnalyzer) CleanupMonitor(monitorID string) error {
	return nil
}

func (s *stubAnalyzer) SaveSegment(monitorID string, data []byte) (string, error) {
	return "/tmp/segment.ts", nil
}

func (s *stubAnalyzer) CleanupSegment(segmentPath string) error {
	return nil
}

func (s *stubAnalyzer) AnalyzeSegment(ctx context.Context, segmentPath string) (*ffmpeg.AnalysisResult, error) {
	return &ffmpeg.AnalysisResult{
		Black:   &ffmpeg.BlackDetectResult{},
		Silence: &ffmpeg.SilenceDetectResult{},
	}, nil
}

type delayedAnalyzer struct {
	delay    time.Duration
	done     chan struct{}
	doneOnce sync.Once
}

func (d *delayedAnalyzer) EnsureTmpDir() error {
	return nil
}

func (d *delayedAnalyzer) CleanupMonitor(monitorID string) error {
	return nil
}

func (d *delayedAnalyzer) SaveSegment(monitorID string, data []byte) (string, error) {
	return "/tmp/segment.ts", nil
}

func (d *delayedAnalyzer) CleanupSegment(segmentPath string) error {
	return nil
}

func (d *delayedAnalyzer) AnalyzeSegment(ctx context.Context, segmentPath string) (*ffmpeg.AnalysisResult, error) {
	time.Sleep(d.delay)
	d.doneOnce.Do(func() {
		if d.done != nil {
			close(d.done)
		}
	})
	return &ffmpeg.AnalysisResult{
		Black:   &ffmpeg.BlackDetectResult{},
		Silence: &ffmpeg.SilenceDetectResult{},
	}, nil
}

func TestGracefulShutdownCompletesAnalysis(t *testing.T) {
	cfg := &config.WorkerConfig{
		MonitorID:                  "mon-test",
		StreamURL:                  "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		CallbackURL:                "http://example.com",
		InternalAPIKey:             "internal-key",
		WebhookURL:                 "http://example.com",
		WebhookSigningKey:          "signing-key",
		WaitingModeInitialInterval: 1 * time.Millisecond,
		WaitingModeDelayedInterval: 1 * time.Millisecond,
		ManifestFetchTimeout:       1 * time.Second,
		ManifestRefreshInterval:    1 * time.Second,
		SegmentFetchTimeout:        1 * time.Second,
		SegmentMaxBytes:            1024,
		AnalysisInterval:           1 * time.Millisecond,
		BlackoutThreshold:          1 * time.Second,
		SilenceThreshold:           1 * time.Second,
		SilenceDBThreshold:         -50,
		DelayThreshold:             1 * time.Second,
		FFmpegPath:                 "ffmpeg",
		FFprobePath:                "ffprobe",
		YtDlpPath:                  "yt-dlp",
		StreamlinkPath:             "streamlink",
	}

	parser := &stubManifestParser{}
	done := make(chan struct{})
	analyzer := &delayedAnalyzer{delay: 20 * time.Millisecond, done: done}
	spyCallback := &spyCallbackClient{}
	worker := NewWorkerWithDeps(cfg, &stubYtDlpClient{}, parser, analyzer, &captureWebhookSender{}, spyCallback)
	worker.setState(StateMonitoring)
	worker.lastLiveCheck = time.Now()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("worker run error: %v", err)
	}

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("analysis did not complete before shutdown")
	}
}

func newTestWorkerForDetection(sender *captureWebhookSender) *Worker {
	cfg := &config.WorkerConfig{
		MonitorID:                  "mon-test",
		StreamURL:                  "https://www.youtube.com/watch?v=test",
		CallbackURL:                "http://example.com",
		InternalAPIKey:             "internal-key",
		WebhookURL:                 "http://example.com",
		WebhookSigningKey:          "signing-key",
		WaitingModeInitialInterval: 1 * time.Millisecond,
		WaitingModeDelayedInterval: 1 * time.Millisecond,
		ManifestFetchTimeout:       1 * time.Second,
		ManifestRefreshInterval:    1 * time.Second,
		SegmentFetchTimeout:        1 * time.Second,
		SegmentMaxBytes:            1024,
		AnalysisInterval:           1 * time.Second,
		BlackoutThreshold:          30 * time.Second,
		SilenceThreshold:           30 * time.Second,
		SilenceDBThreshold:         -50,
		DelayThreshold:             1 * time.Second,
		FFmpegPath:                 "ffmpeg",
		FFprobePath:                "ffprobe",
		YtDlpPath:                  "yt-dlp",
		StreamlinkPath:             "streamlink",
	}
	return NewWorkerWithDeps(cfg, &stubYtDlpClient{}, nil, nil, sender, &spyCallbackClient{})
}

func TestProcessBlackDetection_ImmediateAlert(t *testing.T) {
	sender := &captureWebhookSender{}
	worker := newTestWorkerForDetection(sender)

	result := &ffmpeg.BlackDetectResult{FullyBlack: true, BlackRatio: 1.0}
	worker.processBlackDetection(context.Background(), result, 2.0)

	if len(sender.calls) != 1 {
		t.Fatalf("expected 1 webhook call, got %d", len(sender.calls))
	}
	if sender.calls[0].EventType != webhook.EventAlertBlackout {
		t.Fatalf("event_type = %v, want %v", sender.calls[0].EventType, webhook.EventAlertBlackout)
	}
	if dur, ok := sender.calls[0].Data["duration_sec"].(float64); !ok || dur != 2.0 {
		t.Fatalf("duration_sec = %v, want 2.0", sender.calls[0].Data["duration_sec"])
	}
	if thr, ok := sender.calls[0].Data["threshold_sec"].(int); !ok || thr != 30 {
		t.Fatalf("threshold_sec = %v, want 30", sender.calls[0].Data["threshold_sec"])
	}
	if !worker.blackoutAlertSent {
		t.Fatalf("expected blackoutAlertSent to be true")
	}
	if worker.blackoutEvents != 1 {
		t.Fatalf("blackoutEvents = %d, want 1", worker.blackoutEvents)
	}
}

func TestProcessBlackDetection_NoDuplicateAlert(t *testing.T) {
	sender := &captureWebhookSender{}
	worker := newTestWorkerForDetection(sender)

	result := &ffmpeg.BlackDetectResult{FullyBlack: true, BlackRatio: 1.0}
	worker.processBlackDetection(context.Background(), result, 2.0)
	worker.processBlackDetection(context.Background(), result, 2.0)

	if len(sender.calls) != 1 {
		t.Fatalf("expected 1 webhook call (no duplicate), got %d", len(sender.calls))
	}
	if worker.consecutiveBlack != 4.0 {
		t.Fatalf("consecutiveBlack = %f, want 4.0", worker.consecutiveBlack)
	}
}

func TestProcessSilenceDetection_ImmediateAlert(t *testing.T) {
	sender := &captureWebhookSender{}
	worker := newTestWorkerForDetection(sender)

	result := &ffmpeg.SilenceDetectResult{FullySilent: true, SilenceRatio: 1.0}
	worker.processSilenceDetection(context.Background(), result, 2.0)

	if len(sender.calls) != 1 {
		t.Fatalf("expected 1 webhook call, got %d", len(sender.calls))
	}
	if sender.calls[0].EventType != webhook.EventAlertSilence {
		t.Fatalf("event_type = %v, want %v", sender.calls[0].EventType, webhook.EventAlertSilence)
	}
	if dur, ok := sender.calls[0].Data["duration_sec"].(float64); !ok || dur != 2.0 {
		t.Fatalf("duration_sec = %v, want 2.0", sender.calls[0].Data["duration_sec"])
	}
	if !worker.silenceAlertSent {
		t.Fatalf("expected silenceAlertSent to be true")
	}
	if worker.silenceEvents != 1 {
		t.Fatalf("silenceEvents = %d, want 1", worker.silenceEvents)
	}
}

func TestProcessSilenceDetection_NoDuplicateAlert(t *testing.T) {
	sender := &captureWebhookSender{}
	worker := newTestWorkerForDetection(sender)

	result := &ffmpeg.SilenceDetectResult{FullySilent: true, SilenceRatio: 1.0}
	worker.processSilenceDetection(context.Background(), result, 2.0)
	worker.processSilenceDetection(context.Background(), result, 2.0)

	if len(sender.calls) != 1 {
		t.Fatalf("expected 1 webhook call (no duplicate), got %d", len(sender.calls))
	}
	if worker.consecutiveSilence != 4.0 {
		t.Fatalf("consecutiveSilence = %f, want 4.0", worker.consecutiveSilence)
	}
}

func TestProcessSilenceDetection_RecoveryUnchanged(t *testing.T) {
	sender := &captureWebhookSender{}
	worker := newTestWorkerForDetection(sender)

	// First: trigger immediate silence alert
	silentResult := &ffmpeg.SilenceDetectResult{FullySilent: true, SilenceRatio: 1.0}
	worker.processSilenceDetection(context.Background(), silentResult, 2.0)

	// Then: recovery (non-silent segment)
	clearResult := &ffmpeg.SilenceDetectResult{FullySilent: false}
	worker.processSilenceDetection(context.Background(), clearResult, 2.0)

	if len(sender.calls) != 2 {
		t.Fatalf("expected 2 webhook calls, got %d", len(sender.calls))
	}
	if sender.calls[0].EventType != webhook.EventAlertSilence {
		t.Fatalf("first event_type = %v, want %v", sender.calls[0].EventType, webhook.EventAlertSilence)
	}
	if sender.calls[1].EventType != webhook.EventAlertSilenceRecovered {
		t.Fatalf("second event_type = %v, want %v", sender.calls[1].EventType, webhook.EventAlertSilenceRecovered)
	}
	if worker.silenceAlertSent {
		t.Fatalf("expected silenceAlertSent to be false after recovery")
	}
	if worker.consecutiveSilence != 0 {
		t.Fatalf("consecutiveSilence = %f, want 0", worker.consecutiveSilence)
	}
	if worker.silenceStart != nil {
		t.Fatalf("expected silenceStart to be nil after recovery")
	}
}

func TestProcessBlackDetection_RecoveryUnchanged(t *testing.T) {
	sender := &captureWebhookSender{}
	worker := newTestWorkerForDetection(sender)

	// First: trigger immediate blackout alert
	blackResult := &ffmpeg.BlackDetectResult{FullyBlack: true, BlackRatio: 1.0}
	worker.processBlackDetection(context.Background(), blackResult, 2.0)

	// Then: recovery (non-black segment)
	clearResult := &ffmpeg.BlackDetectResult{FullyBlack: false}
	worker.processBlackDetection(context.Background(), clearResult, 2.0)

	if len(sender.calls) != 2 {
		t.Fatalf("expected 2 webhook calls, got %d", len(sender.calls))
	}
	if sender.calls[0].EventType != webhook.EventAlertBlackout {
		t.Fatalf("first event_type = %v, want %v", sender.calls[0].EventType, webhook.EventAlertBlackout)
	}
	if sender.calls[1].EventType != webhook.EventAlertBlackoutRecovered {
		t.Fatalf("second event_type = %v, want %v", sender.calls[1].EventType, webhook.EventAlertBlackoutRecovered)
	}
	if worker.blackoutAlertSent {
		t.Fatalf("expected blackoutAlertSent to be false after recovery")
	}
	if worker.consecutiveBlack != 0 {
		t.Fatalf("consecutiveBlack = %f, want 0", worker.consecutiveBlack)
	}
	if worker.blackoutStart != nil {
		t.Fatalf("expected blackoutStart to be nil after recovery")
	}
}
