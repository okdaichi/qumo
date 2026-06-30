// Package ffpub drives ffmpeg as an RTMP publisher for integration tests.
//
// It launches ffmpeg with a synthetic lavfi source (test pattern + optional
// tone) encoded with OBS-like libx264/AAC settings and publishes to a target
// RTMP URL, managing the subprocess lifecycle. [Publisher.Args] exposes the
// constructed command so parameterization can be asserted without ffmpeg
// installed; [Publisher.Start] runs the real subprocess and is killed when
// its context is cancelled.
package ffpub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ErrFFmpegAbsent is returned by [Publisher.Start] (and reports false from
// [Available]) when ffmpeg is not on PATH. Tests that require ffmpeg should
// skip on this condition.
var ErrFFmpegAbsent = errors.New("ffpub: ffmpeg not found on PATH")

// Available reports whether ffmpeg is on PATH.
func Available() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

// Config parameterizes an ffmpeg RTMP publish session driven by [Publisher].
type Config struct {
	// URL is the RTMP publish target (e.g. "rtmp://127.0.0.1:1935/live/test").
	URL string
	// BFrames enables B-frames in the encode. When false (the streaming
	// default) ffmpeg is launched with -bf 0.
	BFrames bool
	// Audio includes an AAC audio track synthesized from a 440 Hz sine tone.
	Audio bool
	// GOP is the keyframe interval (-g). Must be > 0.
	GOP int
	// Width and Height are the synthesized video dimensions. Must be > 0.
	Width, Height int
	// Framerate is the synthesized video frame rate. Must be > 0.
	Framerate int
	// Duration, if > 0, limits the publish time via ffmpeg's -t flag. If 0,
	// ffmpeg runs until its context is cancelled.
	Duration time.Duration
}

func (c Config) validate() error {
	switch {
	case c.URL == "":
		return errors.New("ffpub: empty URL")
	case !strings.HasPrefix(c.URL, "rtmp:") && !strings.HasPrefix(c.URL, "rtsp:"):
		return fmt.Errorf("ffpub: URL must be rtmp: or rtsp:, got %q", c.URL)
	case c.GOP <= 0:
		return fmt.Errorf("ffpub: GOP must be > 0, got %d", c.GOP)
	case c.Width <= 0 || c.Height <= 0:
		return fmt.Errorf("ffpub: Width/Height must be > 0, got %dx%d", c.Width, c.Height)
	case c.Framerate <= 0:
		return fmt.Errorf("ffpub: Framerate must be > 0, got %d", c.Framerate)
	}
	return nil
}

// args builds the ffmpeg argument vector from a validated config.
func (c Config) args() []string {
	a := []string{"-hide_banner", "-loglevel", "error", "-re"}
	if c.Duration > 0 {
		a = append(a, "-t", strconv.FormatFloat(c.Duration.Seconds(), 'f', -1, 64))
	}

	// Video source: testsrc2 produces a color-bar test pattern.
	a = append(a, "-f", "lavfi", "-i",
		fmt.Sprintf("testsrc2=size=%dx%d:rate=%d", c.Width, c.Height, c.Framerate))
	if c.Audio {
		a = append(a, "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000")
	}

	// Video encode: OBS-like libx264 streaming settings.
	a = append(a, "-c:v", "libx264", "-g", strconv.Itoa(c.GOP))
	if !c.BFrames {
		a = append(a, "-bf", "0")
	}
	a = append(a, "-preset", "veryfast", "-tune", "zerolatency")

	if c.Audio {
		a = append(a, "-c:a", "aac", "-b:a", "128k")
	}

	// Output muxer + transport flags, selected by URL scheme. RTSP must use
	// TCP interleaving — qumo's RTSP ingest only accepts the interleaved
	// transport; ffmpeg defaults to UDP and would be rejected.
	format, extra := outputMuxer(c.URL)
	a = append(a, extra...)
	a = append(a, "-f", format, c.URL)
	return a
}

// outputMuxer returns the ffmpeg output muxer format and any required
// pre-output flags for the publish URL scheme: "rtsp" (with -rtsp_transport
// tcp) or "flv" for RTMP.
func outputMuxer(url string) (format string, extra []string) {
	if strings.HasPrefix(url, "rtsp:") {
		return "rtsp", []string{"-rtsp_transport", "tcp"}
	}
	return "flv", nil
}

// Publisher drives an ffmpeg subprocess publishing synthetic media to an RTMP
// URL. Use [New] to construct one, [Args] to inspect the command without
// running ffmpeg, and [Start]/[Wait] to run it.
type Publisher struct {
	cfg Config
	cmd *exec.Cmd
}

// New returns a [Publisher] for cfg. The config is not validated until
// [Publisher.Args] or [Publisher.Start] is called.
func New(cfg Config) *Publisher {
	return &Publisher{cfg: cfg}
}

// Args returns the ffmpeg argument vector that [Start] would launch, without
// running ffmpeg. It validates the config first. Useful for asserting
// parameterization in environments without ffmpeg installed.
func (p *Publisher) Args() ([]string, error) {
	if err := p.cfg.validate(); err != nil {
		return nil, err
	}
	return p.cfg.args(), nil
}

// Start launches ffmpeg after verifying it is on PATH and the config is valid.
// The subprocess is killed when ctx is cancelled, so cancelling ctx is the
// normal teardown path. The caller should [Wait] to reap the process.
//
// Returns [ErrFFmpegAbsent] if ffmpeg is not installed; tests that require
// ffmpeg should skip on it.
func (p *Publisher) Start(ctx context.Context) error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return ErrFFmpegAbsent
	}
	if err := p.cfg.validate(); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", p.cfg.args()...)
	// Discard ffmpeg stderr so a busy -loglevel error stream does not spam
	// test output; callers needing it can wrap the Publisher later.
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ffpub: start ffmpeg: %w", err)
	}
	p.cmd = cmd
	return nil
}

// Wait blocks until ffmpeg exits and returns its error. For a process killed
// by context cancellation, the error is typically non-nil (the signal); that
// is expected, not a failure. Panics if [Start] was never called.
func (p *Publisher) Wait() error {
	if p.cmd == nil {
		panic("ffpub: Wait before Start")
	}
	return p.cmd.Wait()
}

// Process returns the underlying *os.Process, or nil if not started.
func (p *Publisher) Process() *os.Process {
	if p.cmd == nil {
		return nil
	}
	return p.cmd.Process
}
