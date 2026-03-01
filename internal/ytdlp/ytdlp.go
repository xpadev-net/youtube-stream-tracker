package ytdlp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// UnixTime wraps time.Time to support unmarshalling from a Unix timestamp (number)
// as returned by yt-dlp's release_timestamp field.
type UnixTime struct {
	time.Time
}

// UnmarshalJSON handles both Unix timestamp integers and RFC3339 strings.
func (u *UnixTime) UnmarshalJSON(data []byte) error {
	// Try as number (Unix timestamp)
	var ts float64
	if err := json.Unmarshal(data, &ts); err == nil {
		u.Time = time.Unix(int64(ts), 0).UTC()
		return nil
	}

	// Fall back to standard time string
	var t time.Time
	if err := json.Unmarshal(data, &t); err != nil {
		return err
	}
	u.Time = t
	return nil
}

// StreamInfo contains information about a YouTube stream.
type StreamInfo struct {
	IsLive          bool      `json:"is_live"`
	LiveStatus      string    `json:"live_status"` // "is_live", "is_upcoming", "was_live", "not_live"
	ReleaseTime     *UnixTime `json:"release_timestamp,omitempty"`
	ManifestURL     string    `json:"url,omitempty"`
	Title           string    `json:"title,omitempty"`
	ChannelID       string    `json:"channel_id,omitempty"`
	Duration        *float64  `json:"duration,omitempty"`
	ViewCount       *int      `json:"view_count,omitempty"`
	Formats         []Format  `json:"formats,omitempty"`
	RequestedFormat *Format   `json:"requested_format,omitempty"`
}

// Format represents a video/audio format.
type Format struct {
	FormatID   string  `json:"format_id"`
	URL        string  `json:"url"`
	Ext        string  `json:"ext"`
	Protocol   string  `json:"protocol"`
	Width      *int    `json:"width,omitempty"`
	Height     *int    `json:"height,omitempty"`
	VCodec     string  `json:"vcodec,omitempty"`
	ACodec     string  `json:"acodec,omitempty"`
	Tbr        float64 `json:"tbr,omitempty"`
	Resolution string  `json:"resolution,omitempty"`
}

// Client wraps yt-dlp command execution.
type Client struct {
	ytdlpPath      string
	streamlinkPath string
	httpProxy      string
	httpsProxy     string
}

// NewClient creates a new yt-dlp client.
func NewClient(ytdlpPath, streamlinkPath, httpProxy, httpsProxy string) *Client {
	if ytdlpPath == "" {
		ytdlpPath = "yt-dlp"
	}
	if streamlinkPath == "" {
		streamlinkPath = "streamlink"
	}
	return &Client{
		ytdlpPath:      ytdlpPath,
		streamlinkPath: streamlinkPath,
		httpProxy:      httpProxy,
		httpsProxy:     httpsProxy,
	}
}

// GetStreamInfo retrieves stream information using yt-dlp.
func (c *Client) GetStreamInfo(ctx context.Context, streamURL string) (*StreamInfo, error) {
	args := []string{
		"--dump-json",
		"--no-playlist",
		"--no-warnings",
	}

	if c.httpProxy != "" {
		args = append(args, "--proxy", c.httpProxy)
	}

	args = append(args, streamURL)

	cmd := exec.CommandContext(ctx, c.ytdlpPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp failed: %w (stderr: %s)", err, stderr.String())
	}

	var info StreamInfo
	if err := json.Unmarshal(stdout.Bytes(), &info); err != nil {
		return nil, fmt.Errorf("parse yt-dlp output: %w", err)
	}

	// Determine is_live from live_status if not set
	if info.LiveStatus == "is_live" {
		info.IsLive = true
	}

	return &info, nil
}

// GetManifestURL retrieves the manifest URL for a live stream.
func (c *Client) GetManifestURL(ctx context.Context, streamURL string) (string, error) {
	args := []string{
		"--get-url",
		"--format", "best[protocol^=http]",
		"--no-playlist",
		"--no-warnings",
		"--quiet",
	}

	if c.httpProxy != "" {
		args = append(args, "--proxy", c.httpProxy)
	}

	args = append(args, streamURL)

	cmd := exec.CommandContext(ctx, c.ytdlpPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		// Try streamlink as fallback
		return c.getManifestURLWithStreamlink(ctx, streamURL)
	}

	url := strings.TrimSpace(stdout.String())
	if url == "" {
		return c.getManifestURLWithStreamlink(ctx, streamURL)
	}

	return url, nil
}

// getManifestURLWithStreamlink is a fallback using streamlink.
func (c *Client) getManifestURLWithStreamlink(ctx context.Context, streamURL string) (string, error) {
	args := []string{
		"--stream-url",
		streamURL,
		"best",
	}

	cmd := exec.CommandContext(ctx, c.streamlinkPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("streamlink failed: %w (stderr: %s)", err, stderr.String())
	}

	url := strings.TrimSpace(stdout.String())
	if url == "" {
		return "", fmt.Errorf("no manifest URL returned")
	}

	return url, nil
}

// IsStreamLive checks if the stream is currently live.
func (c *Client) IsStreamLive(ctx context.Context, streamURL string) (bool, *StreamInfo, error) {
	info, err := c.GetStreamInfo(ctx, streamURL)
	if err != nil {
		return false, nil, err
	}

	isLive := info.IsLive || info.LiveStatus == "is_live"
	return isLive, info, nil
}
