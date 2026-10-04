package main

import (
	"bytes"
	"context"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/marrasen/gunim/audio"
)

// testLAME uses the LAME LAME_PATH names, or one found, for a test, or
// skips it.
func testLAME(t *testing.T) {
	t.Helper()
	p := findLAME(os.Getenv("LAME_PATH"))
	if p == "" {
		t.Skip("no LAME: set LAME_PATH")
	}
	was := lamePath
	useLAME(p)
	t.Cleanup(func() { useLAME(was) })
}

func TestLAMEIsToldTheRateAsItTakesIt(t *testing.T) {
	for rate, want := range map[int]string{44100: "44.1", 48000: "48", 96000: "96"} {
		if args := lameArgs(rate, 320, trackTags{}); !slices.Contains(args, want) {
			t.Fatalf("at %d Hz LAME is told %v", rate, args)
		}
	}
}

func TestAnMP3IsEncodedByLAMEAsTheWAVIsWritten(t *testing.T) {
	testLAME(t)
	path := writeTrack(t, 0, 5*time.Second, 0, 0.3)
	base := filepath.Join(t.TempDir(), "01 Night Engine")
	tags := trackTags{Title: "Night Engine", Artist: "The Oscillators", Album: "Night Drive", Year: "2026",
		Genre: "Synthwave", Track: 1, Total: 4}
	m, _, err := exportTrack(context.Background(), exportJob{path: path, base: base, wav: true, mp3: true, bits: 16,
		dither: true, kbps: 320, tags: tags}, func(float32) {})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(base + ".mp3")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, []byte("ID3")) {
		t.Fatal("the MP3 has no ID3 tag")
	}
	// LAME writes the tag's words in UTF-16, as names of any language
	// need.
	wide := func(s string) []byte {
		out := make([]byte, 0, 2*len(s))
		for _, u := range utf16.Encode([]rune(s)) {
			out = append(out, byte(u), byte(u>>8))
		}
		return out
	}
	for _, want := range []string{"Night Engine", "The Oscillators", "Night Drive", "2026", "1/4", "Synthwave"} {
		if !bytes.Contains(b[:min(len(b), 4096)], wide(want)) {
			t.Fatalf("the MP3's tag lacks %q", want)
		}
	}
	// Read back, it is the track: as long, give or take the encoder's
	// padding, and as loud.
	f, err := os.Open(base + ".mp3")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	src, format, err := audio.DecodeNative(f)
	if err != nil {
		t.Fatal(err)
	}
	lm := audio.NewLoudnessMeter(format.SampleRate)
	buf := make([]float32, 2*4096)
	for {
		n, err := src.Read(buf)
		lm.Write(buf[:2*n])
		if err != nil || n == 0 {
			break
		}
	}
	l, _ := lm.Integrated()
	length := float64(src.Len()) / float64(format.SampleRate)
	if math.Abs(length-m.Length.Seconds()) > 0.1 || math.Abs(l-float64(m.LUFS)) > 0.3 {
		t.Fatalf("the MP3 is %.3f s at %.2f LUFS, the WAV %.3f s at %.2f", length, l, m.Length.Seconds(), m.LUFS)
	}
}
