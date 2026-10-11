package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

func TestAClickOnTheStripPlaysTheTrackThereInOneStep(t *testing.T) {
	a := album()
	two := a.Tracks[0]
	two.ID, two.Title = 2, "Two"
	a.Tracks = append(a.Tracks, two)
	w, r, run := stage(t, a)
	b := boundsOf(t, w, run, r.strip)
	for _, c := range []struct {
		x      float32
		id     int
		reason string
	}{
		{0.25, 1, "the track picked"},
		{0.75, 2, "another track"},
	} {
		at := geom.Pt(b.Min.X+b.Size().W*c.x, b.Center().Y)
		w.Input(input.PointerMove{Pos: at})
		w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
		w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
		run(1)
		// Picking and then seeking would play from where picking starts,
		// for as long as the sound is mixed ahead, before the seek.
		_, rest := edits(w)
		if len(rest) != 1 {
			t.Fatalf("a click on %s sent %v, want one PlayAt", c.reason, rest)
		}
		p, ok := rest[0].(PlayAt)
		if !ok || p.ID != c.id || p.At <= 0 || p.At >= 11*time.Second {
			t.Errorf("a click on %s sent %v, want track %d from within it", c.reason, rest[0], c.id)
		}
	}
}

// writeHalves writes a 6-second track whose left channel is at first,
// then at second from 3 s on, over a right channel steady at 0.25: how
// far left is from right says which half plays, whatever the gains.
func writeHalves(t *testing.T, name string, first, second float32) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := audio.NewWAVWriter(f, rate, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]float32, 2*6*rate)
	for i := range 6 * rate {
		buf[2*i], buf[2*i+1] = first, 0.25
		if i >= 3*rate {
			buf[2*i] = second
		}
	}
	if err := w.Write(buf); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPlayingAnotherTrackAtAPlacePlaysNothingFromElsewhere(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mix := audio.NewMixer()
	a := newApp(ctx, newDeck(mix), "")
	// The first track is even, left as right; the second's left is
	// quiet in its first half and loud in its second.
	a.add(writeHalves(t, "a.wav", 0.25, 0.25), "")
	a.add(writeHalves(t, "b.wav", 0.05, 0.5), "")
	settle(t, a, func() bool { return a.Tracks[0].Scanned && a.Tracks[1].Scanned })
	a.handle(SetGap{})
	a.handle(TogglePlay{})
	buf := make([]float32, 2*512)
	for range 20 {
		clear(buf)
		mix.Mix(buf)
	}
	// Into the loud half of the second track, which picking alone would
	// play from its quiet half, a moment in, as the first plays.
	a.handle(PlayAt{ID: a.Tracks[1].ID, At: 4 * time.Second})
	// Fading in, the loud half lifts left over right, from the first
	// block on, as the first track, even, fades out.
	wrong := 0
	for k := range 100 {
		clear(buf)
		mix.Mix(buf)
		right := 0
		for i := 0; i < len(buf); i += 2 {
			switch l, r := buf[i], buf[i+1]; {
			case r < 0.01:
			case l/r < 0.9:
				wrong++
			case l/r > 1.02:
				right++
			}
		}
		if k == 0 && right == 0 {
			t.Error("the first block mixed after holds nothing from the place played")
		}
		time.Sleep(time.Millisecond)
	}
	if wrong > 0 {
		t.Errorf("%d frames came from the second track's quiet half", wrong)
	}
	if id := a.Current; id != a.Tracks[1].ID || !a.Playing {
		t.Errorf("track %d is picked, playing %v; want the second, playing", id, a.Playing)
	}
}
