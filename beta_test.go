package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/widget"
)

func TestBetaUpdatesAreKeptInTheSettings(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newApp(ctx, newDeck(audio.NewMixer()), "")
	a.settingsFile = filepath.Join(t.TempDir(), "settings.json")
	a.handle(SetBeta{On: true})
	if !a.Beta || !readSettings(a.settingsFile).Beta {
		t.Fatal("beta updates taken up are not kept")
	}
	a.handle(SetBeta{On: false})
	if readSettings(a.settingsFile).Beta {
		t.Fatal("beta updates let go are still kept")
	}
}

func TestACPUProfileIsRecordedAndTold(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newApp(ctx, newDeck(audio.NewMixer()), "")
	a.profileDir = t.TempDir()
	a.handle(RecordProfile{})
	if !a.Profiling || a.profiled == nil {
		t.Fatal("the profile does not record")
	}
	// A second asks while it records: still the one.
	first := a.profile
	a.handle(RecordProfile{})
	a.profileDone()
	fi, err := os.Stat(first)
	if err != nil || fi.Size() == 0 || a.Profiling || a.Tolds != 1 || a.Told == "" {
		t.Fatalf("done, the profile is %v (%v), told %q", fi, err, a.Told)
	}
	if ents, _ := os.ReadDir(a.profileDir); len(ents) != 1 {
		t.Fatalf("%d profiles were written", len(ents))
	}
}

func TestTheMenuTicksBetaUpdatesAndRecordsAProfile(t *testing.T) {
	a := album()
	a.Beta = true
	w, r, run := stage(t, a)
	b := boundsOf(t, w, run, r.header)
	at := b.Min.Add(geom.Pt(80, 24))
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	run(10)
	items := r.headerMenu.Items()
	find := func(label string) int {
		return slices.IndexFunc(items, func(it widget.MenuItem) bool { return it.Label == label })
	}
	i := find("Beta updates")
	if i < 0 || !items[i].Checked || find("Record CPU profile (1 min)") < 0 {
		t.Fatalf("the menu offers %v", items)
	}
}
