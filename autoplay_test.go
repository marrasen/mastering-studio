package main

import (
	"testing"
	"time"

	"github.com/marrasen/gunim/audio"
)

func TestAQueuedTracksChainRunsThoughItsLastPlayFadesOut(t *testing.T) {
	path := writeTrack(t, 0, time.Second, 0, 0.3)
	mix := audio.NewMixer()
	d := newDeck(mix)
	t.Cleanup(d.stop)
	one, two := newRackOf(nil), newRackOf(nil)
	buf := make([]float32, 2*512)
	mixFor := func(blocks int, until func() bool) {
		t.Helper()
		for range blocks {
			clear(buf)
			mix.Mix(buf)
			// The deck's goroutines look every few milliseconds.
			time.Sleep(time.Millisecond)
			if until != nil && until() {
				return
			}
		}
	}
	// The second track plays; then the first, the second fading out,
	// and the second queued after it, as album play does.
	if err := d.play(2, path, 0, Edit{}, two, 0, false, 0); err != nil {
		t.Fatal(err)
	}
	mixFor(10, nil)
	if err := d.play(1, path, 0, Edit{}, one, 0, false, 25*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := d.queue(2, path, 0, Edit{}, two); err != nil {
		t.Fatal(err)
	}
	// Long past the fade out.
	mixFor(30, nil)
	if !two.isActive() || !one.isActive() {
		t.Fatalf("with the second queued after the first, the chains run: first %v, second %v", one.isActive(), two.isActive())
	}
	// The first plays to its end, and the second on from it.
	mixFor(400, func() bool { _, _, id := d.position(); return id == 2 })
	if _, _, id := d.position(); id != 2 {
		t.Fatal("the deck never turned to the second track")
	}
	if one.isActive() || !two.isActive() {
		t.Fatalf("turned to the second, the chains run: first %v, second %v", one.isActive(), two.isActive())
	}
}

func TestATrackUnqueuedStopsItsChainUnlessItPlays(t *testing.T) {
	path := writeTrack(t, 0, time.Second, 0, 0.3)
	d := newDeck(audio.NewMixer())
	t.Cleanup(d.stop)
	one, two := newRackOf(nil), newRackOf(nil)
	if err := d.play(1, path, 0, Edit{}, one, 0, false, 0); err != nil {
		t.Fatal(err)
	}
	if err := d.queue(2, path, 0, Edit{}, two); err != nil {
		t.Fatal(err)
	}
	d.unqueue()
	if two.isActive() || !one.isActive() {
		t.Fatalf("unqueued, the chains run: first %v, second %v", one.isActive(), two.isActive())
	}
	// The track playing queued after itself, as a one-track album
	// looped: unqueued, its chain plays on.
	if err := d.queue(1, path, 0, Edit{}, one); err != nil {
		t.Fatal(err)
	}
	d.unqueue()
	if !one.isActive() {
		t.Fatal("unqueued, the chain of the track playing stopped")
	}
}
