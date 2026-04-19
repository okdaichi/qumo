package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/okdaichi/gomoqt/moqt"
)

const (
	broadcastPath = "/smoke/test"
	trackName     = "data"
)

func main() {
	pubURL := flag.String("pub", "", "publisher-side relay URL (e.g. moqt://localhost:9002)")
	subURL := flag.String("sub", "", "subscriber-side relay URL (e.g. moqt://localhost:9006)")
	timeout := flag.Duration("timeout", 30*time.Second, "overall test timeout")
	numGroups := flag.Int("groups", 5, "number of groups to send")
	numFrames := flag.Int("frames", 10, "number of frames per group")
	frameSize := flag.Int("framesize", 2048, "frame payload size in bytes")

	flag.Parse()

	if *pubURL == "" || *subURL == "" {
		fmt.Fprintln(os.Stderr, "both -pub and -sub flags are required")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Usage:")
		fmt.Fprintln(os.Stderr, "  smoketest -pub <url> -sub <url>")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Example:")
		fmt.Fprintln(os.Stderr, "  smoketest -pub moqt://localhost:9002 -sub moqt://localhost:9006")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	os.Exit(run(ctx, *pubURL, *subURL, *numGroups, *numFrames, *frameSize))
}

func run(ctx context.Context, pubURL, subURL string, numGroups, numFrames, frameSize int) int {
	testData := generateTestData(numGroups, numFrames, frameSize)
	sentHash := hashAllFlat(testData, numGroups, numFrames)

	tlsConf := &tls.Config{InsecureSkipVerify: true} //nolint:gosec // self-signed certs in Docker topology

	// --- Publisher ---
	pubMux := moqt.NewTrackMux(0)

	pubMux.PublishFunc(ctx, moqt.BroadcastPath(broadcastPath), func(tw *moqt.TrackWriter) {
		defer tw.Close()
		for g := range numGroups {
			gw, err := tw.OpenGroup()
			if err != nil {
				log.Printf("publish: OpenGroup: %v", err)
				return
			}
			for f := range numFrames {
				frame := moqt.NewFrame(frameSize)
				if _, err := frame.Write(testData[g*numFrames+f]); err != nil {
					log.Printf("publish: frame.Write: %v", err)
					_ = gw.Close()
					return
				}
				if err := gw.WriteFrame(frame); err != nil {
					log.Printf("publish: WriteFrame: %v", err)
					_ = gw.Close()
					return
				}
			}
			if err := gw.Close(); err != nil {
				log.Printf("publish: Close group: %v", err)
				return
			}
		}
		log.Printf("publish: sent %d groups × %d frames ✓", numGroups, numFrames)
	})

	pubDialer := &moqt.Dialer{TLSConfig: tlsConf}
	pubSess, err := pubDialer.Dial(ctx, pubURL, pubMux)
	if err != nil {
		log.Printf("publish: dial %s: %v", pubURL, err)
		return 1
	}
	defer pubSess.CloseWithError(moqt.NoError, "done")
	log.Printf("publish: connected to %s", pubURL)

	// Wait for announcement to propagate across relay mesh.
	select {
	case <-time.After(3 * time.Second):
	case <-ctx.Done():
		log.Printf("timeout waiting for propagation")
		return 1
	}

	// --- Subscriber ---
	subMux := moqt.NewTrackMux(0)
	subDialer := &moqt.Dialer{TLSConfig: tlsConf}
	subSess, err := subDialer.Dial(ctx, subURL, subMux)
	if err != nil {
		log.Printf("subscribe: dial %s: %v", subURL, err)
		return 1
	}
	defer subSess.CloseWithError(moqt.NoError, "done")
	log.Printf("subscribe: connected to %s", subURL)

	tr, err := subSess.Subscribe(ctx,
		moqt.BroadcastPath(broadcastPath),
		moqt.TrackName(trackName), nil)
	if err != nil {
		log.Printf("subscribe: Subscribe: %v", err)
		return 1
	}
	defer tr.Close()
	log.Println("subscribe: subscribed, reading groups...")

	// Read all groups/frames and compute hash.
	h := sha256.New()
	groupCount := 0
	frameCount := 0
	buf := moqt.NewFrame(frameSize + 256)

	for groupCount < numGroups {
		gr, err := tr.AcceptGroup(ctx)
		if err != nil {
			log.Printf("subscribe: AcceptGroup: %v", err)
			break
		}
		for frame := range gr.Frames(buf) {
			h.Write(frame.Body())
			frameCount++
		}
		groupCount++
	}

	recvHash := fmt.Sprintf("%x", h.Sum(nil))

	log.Printf("subscribe: received %d groups, %d frames ✓", groupCount, frameCount)

	if recvHash != sentHash {
		fmt.Println("")
		fmt.Printf("❌ FAIL: connectivity check failed — hash mismatch\n   sent=%s\n   recv=%s\n", sentHash, recvHash)
		return 1
	}

	fmt.Println("")
	fmt.Printf("📡 PASS: %d groups × %d frames streamed end-to-end\n   %s → %s\n",
		numGroups, numFrames, pubURL, subURL)
	return 0
}

// generateTestData creates deterministic test payloads.
func generateTestData(numGroups, numFrames, frameSize int) [][]byte {
	data := make([][]byte, numGroups*numFrames)
	for g := range numGroups {
		for f := range numFrames {
			pattern := fmt.Sprintf("group=%d frame=%d ", g, f)
			payload := []byte(strings.Repeat(pattern, (frameSize/len(pattern))+1))[:frameSize]
			data[g*numFrames+f] = payload
		}
	}
	return data
}

// hashAllFlat computes SHA-256 over all payloads in group×frame order.
func hashAllFlat(data [][]byte, numGroups, numFrames int) string {
	h := sha256.New()
	for g := range numGroups {
		for f := range numFrames {
			h.Write(data[g*numFrames+f])
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}
