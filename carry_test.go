package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/input"
)

func TestATrackPickedStartsWhereCarrySays(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newApp(ctx, newDeck(audio.NewMixer()), "")
	// Eleven seconds, and six, each with its second of silence.
	long, short := album().Tracks[0], album().Tracks[0]
	short.ID, short.Frames = 2, 5*rate
	for _, c := range []struct {
		carry  Carry
		at     time.Duration
		to     *Track
		want   time.Duration
		reason string
	}{
		{CarryPercent, 8 * time.Second, &short, 8 * time.Second * 6 / 11, "as far through"},
		{CarryTime, 3 * time.Second, &short, 3 * time.Second, "the same moment"},
		{CarryTime, 8 * time.Second, &short, 0, "past the shorter track's end: its start"},
		{CarryStart, 8 * time.Second, &short, 0, "its start"},
		{CarryTime, 8 * time.Second, &long, 8 * time.Second, "the same moment of a track as long"},
	} {
		a.Carry = c.carry
		if got := a.carried(&long, c.to, c.at); (got - c.want).Abs() > time.Millisecond {
			t.Errorf("%s: from %v, it starts at %v, want %v", c.reason, c.at, got, c.want)
		}
	}
}

func TestPickingAShorterTrackPastItsEndPlaysItFromItsStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mix := audio.NewMixer()
	a := newApp(ctx, newDeck(mix), "")
	for _, p := range writeAlbum(t) {
		a.add(p, "")
	}
	settle(t, a, func() bool { return a.Tracks[0].Scanned && a.Tracks[2].Scanned })
	a.Carry = CarryTime
	a.handle(Pick{ID: a.Tracks[2].ID})
	a.handle(TogglePlay{})
	// 4.4 s into the third, 4.6 s long: past the first's end, at 4 s.
	a.handle(SeekTo{At: 4400 * time.Millisecond})
	a.handle(Pick{ID: a.Tracks[0].ID})
	at, _, id := a.d.position()
	if id != a.Tracks[0].ID || at > 100*time.Millisecond || !a.Playing {
		t.Fatalf("picked, the first track plays at %v (track %d, playing %v), want from its start", at, id, a.Playing)
	}
}

func TestTheCarryButtonTurnsThroughItsWaysAndIsKept(t *testing.T) {
	w, r, run := stage(t, album())
	b := boundsOf(t, w, run, r.trans.carry)
	at := b.Center()
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	run(1)
	if _, rest := edits(w); len(rest) != 1 || rest[0] != (SetCarry{Carry: CarryTime}) {
		t.Fatalf("the button sent %v", rest)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newApp(ctx, newDeck(audio.NewMixer()), "")
	a.settingsFile = filepath.Join(t.TempDir(), "settings.json")
	a.handle(SetCarry{Carry: CarryStart})
	if got := readSettings(a.settingsFile).Carry; got != CarryStart {
		t.Fatalf("the settings keep %v", got)
	}
	a.handle(SetCarry{Carry: CarryStart + 1})
	if a.Carry != CarryPercent {
		t.Fatalf("past the last way, the button turns to %v, want as far through", a.Carry)
	}
}

func TestTheCarryTipSaysWhatTheButtonIsOn(t *testing.T) {
	a := album()
	w, r, run := stage(t, a)
	if r.trans.carryTo.Text != carryLooks[CarryPercent].tip {
		t.Fatalf("the button's tip reads %q", r.trans.carryTo.Text)
	}
	// The application's answer to a press: the next way, which the tip,
	// up or not, turns to.
	a.Carry = CarryTime
	if err := w.Client().Update("album", a); err != nil {
		t.Fatal(err)
	}
	run(1)
	if r.trans.carryTo.Text != carryLooks[CarryTime].tip {
		t.Fatalf("pressed, the button's tip reads %q", r.trans.carryTo.Text)
	}
}
