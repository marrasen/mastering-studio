package main

import (
	"math"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio"
)

// A pause fades the sound out over pauseFade, and playing on fades it
// back in, so neither cuts it off.
func TestAPauseFadesTheSoundOutAndBackIn(t *testing.T) {
	path := writeTrack(t, 0, 10*time.Second, 0, 0.3)
	d := newDeck(audio.NewMixer())
	if err := d.play(1, path, 0, Edit{}, &rack{active: true}, 0, false, 0); err != nil {
		t.Fatal(err)
	}
	buf := make([]float32, 2*audio.SampleRate/100)
	// The deck closes the track once its voice has stopped and been
	// mixed, so the track can be removed after, on Windows too.
	t.Cleanup(func() {
		d.stop()
		for range 10 {
			d.mix.Mix(buf)
		}
	})
	// peak mixes 10 ms and returns the loudest sample in it.
	peak := func() float64 {
		d.mix.Mix(buf)
		var p float64
		for _, s := range buf {
			p = max(p, math.Abs(float64(s)))
		}
		return p
	}
	for range 10 {
		peak()
	}
	full := peak()
	if full < 0.1 {
		t.Fatalf("playing, the sound peaks at %.3f", full)
	}
	blocks := int(pauseFade / (10 * time.Millisecond))

	d.setPaused(true)
	out := make([]float64, 0, blocks+3)
	for range blocks + 3 {
		out = append(out, peak())
	}
	// Half way through, the sound is about half as loud: neither cut
	// off nor still at full.
	if mid := out[blocks/2]; mid < 0.3*full || mid > 0.7*full {
		t.Errorf("half way through the pause's fade, the sound peaks at %.3f of %.3f", mid, full)
	}
	if end := out[len(out)-1]; end != 0 {
		t.Errorf("paused, the sound peaks at %.3f", end)
	}

	d.setPaused(false)
	in := make([]float64, 0, blocks+3)
	for range blocks + 3 {
		in = append(in, peak())
	}
	if first := in[0]; first > 0.2*full {
		t.Errorf("playing on, the first 10 ms peak at %.3f of %.3f", first, full)
	}
	if mid := in[blocks/2]; mid < 0.3*full || mid > 0.7*full {
		t.Errorf("half way through playing on, the sound peaks at %.3f of %.3f", mid, full)
	}
	if end := in[len(in)-1]; end < 0.9*full {
		t.Errorf("played on, the sound peaks at %.3f of %.3f", end, full)
	}
}
