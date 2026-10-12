package main

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/marrasen/gunim/audio/plug/trim"

	"github.com/marrasen/gunim/audio"
)

func TestAChainIsKeptAsAPresetLoadedAndDeleted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newApp(ctx, newDeck(audio.NewMixer()), "")
	a.presetsFile = filepath.Join(t.TempDir(), "presets.json")
	a.Tracks = []Track{{ID: 1, Title: "One", Chain: []Slot{{ID: 3, Name: "EQ", Label: "Low cut"}, {ID: 4, Name: "Limiter", Bypass: true}}},
		{ID: 2, Title: "Two"}}
	a.slots = 4
	a.states[3], a.states[4] = []byte("eq"), []byte("lim")
	a.handle(SavePreset{Track: 1, Name: " Master "})
	if a.Tracks[0].Preset != "Master" || !slices.Equal(a.Presets, []string{"Master"}) {
		t.Fatalf("saved, the track's preset is %q, the presets %v", a.Tracks[0].Preset, a.Presets)
	}
	a.handle(LoadPreset{Track: 2, Name: "Master"})
	two := a.Tracks[1]
	if len(two.Chain) != 2 || two.Chain[0].Label != "Low cut" || !two.Chain[1].Bypass || two.Preset != "Master" ||
		two.Chain[0].ID == 3 || string(a.states[two.Chain[0].ID]) != "eq" {
		t.Fatalf("loaded, the second track's chain is %+v, preset %q", two.Chain, two.Preset)
	}
	if a.UndoLabel != "loading the preset Master on Two" {
		t.Fatalf("loading a preset is undone as %q", a.UndoLabel)
	}
	// Saved again, as its own preset, with its change.
	a.states[two.Chain[0].ID] = []byte("eq2")
	a.handle(SavePreset{Track: 2})
	if p := a.presets[a.preset("Master")]; string(p.Chain[0].State) != "eq2" || len(a.presets) != 1 {
		t.Fatalf("saved as its own preset, the preset holds %q, of %d", p.Chain[0].State, len(a.presets))
	}
	// Kept for the next run.
	b := newApp(ctx, newDeck(audio.NewMixer()), "")
	b.presetsFile = a.presetsFile
	b.loadPresets()
	if !slices.Equal(b.Presets, []string{"Master"}) {
		t.Fatalf("the next run has the presets %v", b.Presets)
	}
	a.handle(DeletePreset{Name: "Master"})
	if len(a.Presets) != 0 || a.DeletedPreset != "Master" || a.Deletes != 1 {
		t.Fatalf("deleted, the presets are %v", a.Presets)
	}
	a.handle(RestorePreset{})
	if !slices.Equal(a.Presets, []string{"Master"}) {
		t.Fatalf("brought back, the presets are %v", a.Presets)
	}
}

func TestSessionBsTrackIsKeptWithTheAlbum(t *testing.T) {
	a := savedApp(t)
	a.handle(SwitchSide{})
	a.handle(Pick{ID: a.Tracks[2].ID})
	a.handle(SetLooping{On: true})
	a.handle(SwitchSide{})
	if err := a.saveNow(); err != nil {
		t.Fatal(err)
	}
	b := newApp(a.ctx, newDeck(audio.NewMixer()), a.file)
	b.load()
	if b.Current != b.Tracks[0].ID || b.Away.Current != b.Tracks[2].ID || !b.Away.Looping || b.Away.Playing {
		t.Fatalf("opened again, A has %d and B %+v, want the first and the third, looping, waiting", b.Current, b.Away)
	}
	// B on a reference, kept as one.
	a.handle(AddReferences{Paths: []string{a.Tracks[0].File}})
	a.Away.Current = a.References[0].ID
	if err := a.saveNow(); err != nil {
		t.Fatal(err)
	}
	c := newApp(a.ctx, newDeck(audio.NewMixer()), a.file)
	c.References = a.References
	c.load()
	if c.Away.Current != a.References[0].ID {
		t.Fatalf("opened again, B has %d, want the reference", c.Away.Current)
	}
}

func TestSwitchingSidesAloneKeepsSessionBsTrack(t *testing.T) {
	a := savedApp(t)
	a.handle(AddReferences{Paths: []string{a.Tracks[0].File}})
	a.dirty = false
	// Switched to B, which takes up the first reference of itself.
	a.handle(SwitchSide{})
	a.save()
	b := newApp(a.ctx, newDeck(audio.NewMixer()), a.file)
	b.References = a.References
	b.load()
	if b.Away.Current != a.References[0].ID || b.Unsaved {
		t.Fatalf("opened again, B has %d, unsaved %v, want the reference, saved", b.Away.Current, b.Unsaved)
	}
}

func TestATracksChainBStartsEmptyAndEachSideKeepsItsOwn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newApp(ctx, newDeck(audio.NewMixer()), "")
	a.Tracks = []Track{{ID: 1, Title: "One", Preset: "Loud", Chain: []Slot{{ID: 3, Name: "EQ", Label: "More bass"}}}}
	a.slots = 3
	a.states[3] = []byte("a")
	chain := func() (names []string, states []string) {
		for _, s := range a.Tracks[0].Chain {
			names = append(names, s.title())
			states = append(states, string(a.states[s.ID]))
		}
		return names, states
	}
	// To B: empty, a side of its own.
	a.handle(SwitchChain{Track: 1, B: true})
	if names, _ := chain(); !a.Tracks[0].OnB || len(names) != 0 || a.Tracks[0].Preset != "" {
		t.Fatalf("switched to B, the chain is %v, on B %v, preset %q; want empty", names, a.Tracks[0].OnB, a.Tracks[0].Preset)
	}
	if a.UndoLabel != "switching to chain B on One" {
		t.Errorf("switching is undone as %q", a.UndoLabel)
	}
	// A plugin added to B: A has none of it.
	a.Tracks[0].Chain = append(a.Tracks[0].Chain, Slot{ID: 99, Name: "Trim"})
	a.states[99] = []byte("trim")
	a.handle(SwitchChain{Track: 1, B: false})
	if names, states := chain(); a.Tracks[0].OnB || !slices.Equal(names, []string{"More bass"}) || !slices.Equal(states, []string{"a"}) ||
		a.Tracks[0].Preset != "Loud" {
		t.Fatalf("back on A, the chain is %v %v, preset %q", names, states, a.Tracks[0].Preset)
	}
	a.handle(SwitchChain{Track: 1, B: true})
	if names, states := chain(); !slices.Equal(names, []string{"Trim"}) || !slices.Equal(states, []string{"trim"}) {
		t.Fatalf("on B again, the chain is %v %v", names, states)
	}
	// Copied to A: back on A, the same as B.
	a.handle(CopyChainSide{Track: 1})
	if a.UndoLabel != "copying chain B to A on One" {
		t.Errorf("copying is undone as %q", a.UndoLabel)
	}
	a.handle(SwitchChain{Track: 1, B: false})
	if names, states := chain(); !slices.Equal(names, []string{"Trim"}) || !slices.Equal(states, []string{"trim"}) {
		t.Fatalf("B copied to A, A is %v %v", names, states)
	}
}

func TestBothChainsAreKeptWithTheAlbum(t *testing.T) {
	a := savedApp(t)
	id := a.Tracks[0].ID
	a.handle(SwitchChain{Track: id, B: true})
	a.handle(AddPlugin{Track: id, Choice: PluginChoice{Path: goPath, Class: "marrasen.trim", Name: "Trim"}})
	if err := a.saveNow(); err != nil {
		t.Fatal(err)
	}
	b := newApp(a.ctx, newDeck(audio.NewMixer()), a.file)
	b.load()
	t1 := b.Tracks[0]
	if !t1.OnB || !t1.OtherSet || len(t1.Chain) != len(a.Tracks[0].Chain) || len(t1.Other) != len(a.Tracks[0].Other) {
		t.Fatalf("opened again, on B %v, set %v, B has %d plugins and A %d, want %d and %d", t1.OnB, t1.OtherSet,
			len(t1.Chain), len(t1.Other), len(a.Tracks[0].Chain), len(a.Tracks[0].Other))
	}
}

func TestSwitchingChainsWhilePlayingKeepsTheChainLoaded(t *testing.T) {
	a := savedApp(t)
	id := a.Tracks[0].ID
	a.handle(AddPlugin{Track: id, Choice: PluginChoice{Path: goPath, Class: "marrasen.trim", Name: "Trim"}})
	r := a.rackOf(&a.Tracks[0])
	a.handle(SwitchChain{Track: id, B: true})
	if a.racks[id] != r || len(r.list()) != 0 {
		t.Fatalf("switched to B, empty, the track's chain loaded is %p, was %p, with %d plugins", a.racks[id], r, len(r.list()))
	}
	a.handle(SwitchChain{Track: id, B: false})
	if a.racks[id] != r || len(r.list()) != 1 || r.list()[0].slot != a.Tracks[0].Chain[0].ID {
		t.Fatalf("back on A, the track's chain loaded is %p, was %p, with %d plugins", a.racks[id], r, len(r.list()))
	}
}

func TestSwitchingChainsKeepsEachSidesLiveSettings(t *testing.T) {
	a := savedApp(t)
	id := a.Tracks[0].ID
	a.handle(AddPlugin{Track: id, Choice: PluginChoice{Path: goPath, Class: "marrasen.trim", Name: "Trim"}})
	a.handle(TogglePlay{})
	r := a.rackOf(&a.Tracks[0])
	// gain is the trim's gain as the chain plays it, as the app keeps
	// it, and as the views are shown it.
	gain := func() (live, kept, shown float64) {
		t.Helper()
		s := a.Tracks[0].Chain[0]
		lp := r.find(s.ID)
		if lp == nil {
			t.Fatal("the trim is not in the chain playing")
		}
		e, ok := lp.p.(*goEngine)
		if !ok {
			t.Fatal("the trim is not a Go plugin")
		}
		set, err := readGoSettings(a.states[s.ID])
		if err != nil {
			t.Fatal(err)
		}
		a.showGoSettings()
		return e.param(trim.GainID).value(), set.Params[trim.GainID], a.Tracks[0].Chain[0].Values[trim.GainID]
	}
	setGain := func(db float64) {
		a.handle(SetGoParam{Track: id, Slot: a.Tracks[0].Chain[0].ID, ID: trim.GainID, Value: db, Gesture: 1})
	}
	setGain(-4)
	// B, empty: a trim of its own, set apart.
	a.handle(SwitchChain{Track: id, B: true})
	if len(a.Tracks[0].Chain) != 0 {
		t.Fatalf("B has %d plugins, want none of A's", len(a.Tracks[0].Chain))
	}
	a.handle(AddPlugin{Track: id, Choice: PluginChoice{Path: goPath, Class: "marrasen.trim", Name: "Trim"}})
	setGain(-8)
	a.handle(SwitchChain{Track: id, B: false})
	if live, kept, shown := gain(); live != -4 || kept != -4 || shown != -4 {
		t.Fatalf("back on A, it plays %v, keeps %v, shows %v; want A's -4", live, kept, shown)
	}
	a.handle(SwitchChain{Track: id, B: true})
	if live, kept, shown := gain(); live != -8 || kept != -8 || shown != -8 {
		t.Fatalf("back on B, it plays %v, keeps %v, shows %v; want B's -8", live, kept, shown)
	}
	// Bypassed on B, and back: A is not, B still is.
	a.handle(SetBypass{Track: id, Slot: a.Tracks[0].Chain[0].ID, On: true})
	a.handle(SwitchChain{Track: id, B: false})
	if a.Tracks[0].Chain[0].Bypass || r.find(a.Tracks[0].Chain[0].ID).bypass {
		t.Error("B's bypass came over to A")
	}
	a.handle(SwitchChain{Track: id, B: true})
	if !a.Tracks[0].Chain[0].Bypass || !r.find(a.Tracks[0].Chain[0].ID).bypass {
		t.Error("B's bypass was lost going to A and back")
	}
}
