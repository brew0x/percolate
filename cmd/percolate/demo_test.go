package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"os"
	"strings"
	"testing"

	"github.com/brew0x/percolate/internal/repo"
	"github.com/brew0x/percolate/internal/scene"
)

const (
	demoFixture  = "../../testdata/fixtures/small.json"
	demoManifest = "../../demo/frames.sha256"
	demoGIF      = "../../demo/percolate.gif"
	demoFrames   = 32
	demoWidth    = 80
	demoHeight   = 24
	gifWidth     = 900
	gifHeight    = 560
)

// demoHashes reads the recorded frames. Each line is "<frame>  <sha256>" for
// the exact plain text percolate render writes for that frame.
func demoHashes(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(demoManifest)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != demoFrames {
		t.Fatalf("manifest has %d frames, want %d", len(lines), demoFrames)
	}
	hashes := make([]string, len(lines))
	for i, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != fmt.Sprintf("%02d", i) || len(fields[1]) != 64 {
			t.Fatalf("bad manifest line %d: %q", i, line)
		}
		if _, err := hex.DecodeString(fields[1]); err != nil {
			t.Fatalf("bad manifest hash %d: %q", i, line)
		}
		hashes[i] = fields[1]
	}
	return hashes
}

// The GIF's encoded bytes vary with the ffmpeg build and font rasterizer, but
// the frames it replays must be the fixture's frames. Prove that from the text
// the generator encoded, not from encoder output.
func TestDemoFramesMatchFixture(t *testing.T) {
	data, err := os.ReadFile(demoFixture)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot repo.Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	built := scene.Build(snapshot)
	want := demoHashes(t)
	for i, digest := range want {
		frame := scene.Frame(built, i, demoWidth, demoHeight).String() + "\n"
		sum := sha256.Sum256([]byte(frame))
		if got := hex.EncodeToString(sum[:]); got != digest {
			t.Fatalf("frame %02d differs from the recorded demo frame", i)
		}
	}
}

// Composited GIF frames must change exactly where the recorded frames change:
// same text means same picture, different text means a different picture. This
// holds across ffmpeg versions even when their palette and dither bytes differ.
func TestDemoGIFReplaysFrames(t *testing.T) {
	f, err := os.Open(demoGIF)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	g, err := gif.DecodeAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Image) != demoFrames || g.Config.Width != gifWidth || g.Config.Height != gifHeight || g.LoopCount != 0 {
		t.Fatalf("demo GIF is %dx%d with %d frames and loop %d", g.Config.Width, g.Config.Height, len(g.Image), g.LoopCount)
	}
	pictures := compositeFrames(t, g)
	hashes := demoHashes(t)
	for i := range pictures {
		for j := i + 1; j < len(pictures); j++ {
			if (hashes[i] == hashes[j]) != bytes.Equal(pictures[i], pictures[j]) {
				t.Fatalf("GIF frames %02d and %02d do not follow the recorded frames", i, j)
			}
		}
	}
}

// compositeFrames paints each GIF frame, honouring disposal, and returns the
// full picture after every frame. ffmpeg writes patches rather than full frames
// after the first one, so raw frame bytes cannot be compared.
func compositeFrames(t *testing.T, g *gif.GIF) [][]byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, g.Config.Width, g.Config.Height))
	background := image.NewUniform(color.Transparent)
	var before *image.RGBA
	out := make([][]byte, len(g.Image))
	for i, frame := range g.Image {
		if i > 0 {
			switch g.Disposal[i-1] {
			case gif.DisposalBackground:
				draw.Draw(canvas, canvas.Bounds(), background, image.Point{}, draw.Src)
			case gif.DisposalPrevious:
				if before != nil {
					draw.Draw(canvas, canvas.Bounds(), before, image.Point{}, draw.Src)
				}
			}
		}
		if g.Disposal[i] == gif.DisposalPrevious {
			before = image.NewRGBA(canvas.Bounds())
			copy(before.Pix, canvas.Pix)
		}
		draw.Draw(canvas, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)
		out[i] = bytes.Clone(canvas.Pix)
	}
	return out
}
