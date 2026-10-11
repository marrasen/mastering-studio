package main

import (
	"bytes"
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/mastering-studio/plug"
	"github.com/marrasen/mastering-studio/plug/eq"
	"github.com/marrasen/mastering-studio/plug/trim"
)

func TestAPatchTheEQComesWithLoadsAsAStepToUndo(t *testing.T) {
	a, tr, _ := goApp(t)
	a.handle(AddPlugin{Track: tr.ID, Choice: PluginChoice{Path: goPath, Class: eq.Descriptor.ID, Name: "EQ"}})
	s := tr.Chain[1]
	a.rackOf(tr)
	e := a.goEngineOf(tr, s.ID)
	if e == nil {
		t.Fatal("the EQ is not loaded")
	}
	eqp, ok := e.p.(interface{ Bands() []audio.Band })
	if !ok {
		t.Fatal("the EQ loaded has no bands")
	}
	bands := func() int { return len(eqp.Bands()) }
	steps := len(a.undo)
	a.handle(LoadPatch{Track: tr.ID, Slot: s.ID, Name: "Vocal presence", Own: true})
	if bands() != 4 {
		t.Fatalf("Vocal presence loaded gives the EQ %d bands, want 4", bands())
	}
	set, _ := readGoSettings(a.states[s.ID])
	want := eq.Descriptor.Patches[slices.IndexFunc(eq.Descriptor.Patches, func(p plug.Patch) bool { return p.Name == "Vocal presence" })]
	if !bytes.Equal(set.State, want.State) {
		t.Error("the slot keeps another state than the patch's")
	}
	if len(a.undo) != steps+1 || a.UndoLabel != "loading the patch Vocal presence into EQ on "+tr.Title {
		t.Fatalf("the load made %d steps, the last called %q", len(a.undo)-steps, a.UndoLabel)
	}
	a.handle(Undo{})
	if bands() != 0 {
		t.Fatalf("undone, the EQ has %d bands, want none", bands())
	}
	// A patch the plugin has not is nothing.
	a.handle(LoadPatch{Track: tr.ID, Slot: s.ID, Name: "Nothing like it", Own: true})
	if bands() != 0 || a.Note != "" {
		t.Fatalf("a patch of no name loaded %d bands, and said %q", bands(), a.Note)
	}
}

func TestAPatchIsSavedLoadedElsewhereAndDeleted(t *testing.T) {
	a, tr, s := goApp(t)
	a.patchesFile = filepath.Join(t.TempDir(), "patches.json")
	a.handle(SetGoParam{Track: tr.ID, Slot: s.ID, ID: trim.GainID, Value: -3, Gesture: 1})
	a.handle(SavePatch{Track: tr.ID, Slot: s.ID, Name: " Quiet "})
	if !slices.Equal(a.Patches, []PatchName{{Class: trim.Descriptor.ID, Name: "Quiet"}}) {
		t.Fatalf("saved, the patches are %v", a.Patches)
	}
	a.handle(SetGoParam{Track: tr.ID, Slot: s.ID, ID: trim.GainID, Value: 2, Gesture: 2})
	a.handle(LoadPatch{Track: tr.ID, Slot: s.ID, Name: "Quiet"})
	if kept, loaded := gainOf(t, a, tr, s); kept != -3 || loaded != -3 {
		t.Fatalf("Quiet loaded, the trim is at %v, and plays at %v; want -3", kept, loaded)
	}
	// Another run of the studio finds it.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := newApp(ctx, newDeck(audio.NewMixer()), "")
	b.patchesFile = a.patchesFile
	b.loadPatches()
	if !slices.Equal(b.Patches, a.Patches) {
		t.Fatalf("another run finds the patches %v", b.Patches)
	}
	a.handle(DeletePatch{Class: trim.Descriptor.ID, Name: "Quiet"})
	if len(a.Patches) != 0 {
		t.Fatalf("deleted, the patches are %v", a.Patches)
	}
	b.patches = nil
	b.loadPatches()
	if len(b.patches) != 0 {
		t.Fatalf("deleted, another run still finds %v", b.Patches)
	}
}

func TestThePanesPatchesMenuListsAndLoadsThePatches(t *testing.T) {
	a := goAlbum(0)
	a.Tracks[0].Chain = append(a.Tracks[0].Chain, Slot{ID: 10, Path: goPath, Class: eq.Descriptor.ID, Name: "EQ",
		Values: map[uint32]float64{1: 0}})
	a.Patches = []PatchName{{Class: eq.Descriptor.ID, Name: "Mine"}, {Class: trim.Descriptor.ID, Name: "Not the EQ's"}}
	a.ShowGo, a.ShowGos = 10, 1
	w, r, run := stage(t, a)
	run(60)
	sh := r.pane.sheets[r.goViews[10]]
	if sh == nil {
		t.Fatal("the EQ's sheet is not shown")
	}
	b := boundsOf(t, w, run, sh.patches)
	w.Input(input.PointerMove{Pos: b.Center()})
	w.Input(input.PointerDown{Pos: b.Center(), Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: b.Center(), Button: input.ButtonPrimary})
	run(2)
	labels := make([]string, 0, len(sh.patchMenu.Items()))
	for _, it := range sh.patchMenu.Items() {
		labels = append(labels, it.Label)
	}
	want := []string{"Comes with EQ", "Flat", "Warm", "Bright", "Vocal presence", "Bass boost", "Loudness", "Low cut",
		"Saved", "Mine", "Save patch…", "Delete patch…"}
	if !slices.Equal(labels, want) {
		t.Fatalf("the menu lists %q, want %q", labels, want)
	}
	if in := sh.patchMenu.OnPick(2, nil); in != (LoadPatch{Track: 1, Slot: 10, Name: "Warm", Own: true}) {
		t.Errorf("Warm picked sends %v", in)
	}
	if in := sh.patchMenu.OnPick(9, nil); in != (LoadPatch{Track: 1, Slot: 10, Name: "Mine"}) {
		t.Errorf("Mine picked sends %v", in)
	}
	if in := sh.patchMenu.OnPick(10, nil); in != (NamePatch{Track: 1, Slot: 10}) {
		t.Errorf("Save picked sends %v", in)
	}
}
