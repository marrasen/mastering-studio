package main

import (
	"bytes"
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/braid"
	"github.com/marrasen/gunim/audio/plug/eq"
	"github.com/marrasen/gunim/audio/vst3"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
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
	a.handle(SetGoParam{Track: tr.ID, Slot: s.ID, ID: braid.GainID, Value: -3, Gesture: 1})
	a.handle(SavePatch{Track: tr.ID, Slot: s.ID, Name: " Quiet "})
	if !slices.Equal(a.Patches, []PatchName{{Class: braid.Descriptor.ID, Name: "Quiet"}}) {
		t.Fatalf("saved, the patches are %v", a.Patches)
	}
	a.handle(SetGoParam{Track: tr.ID, Slot: s.ID, ID: braid.GainID, Value: 2, Gesture: 2})
	a.handle(LoadPatch{Track: tr.ID, Slot: s.ID, Name: "Quiet"})
	if kept, loaded := gainOf(t, a, tr, s); kept != -3 || loaded != -3 {
		t.Fatalf("Quiet loaded, the braid is at %v, and plays at %v; want -3", kept, loaded)
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
	a.handle(DeletePatch{Class: braid.Descriptor.ID, Name: "Quiet"})
	if len(a.Patches) != 0 {
		t.Fatalf("deleted, the patches are %v", a.Patches)
	}
	b.patches = nil
	b.loadPatches()
	if len(b.patches) != 0 {
		t.Fatalf("deleted, another run still finds %v", b.Patches)
	}
}

func TestTheSheetsBrowserListsLoadsKeepsAndDeletesThePatches(t *testing.T) {
	a := goAlbum(0)
	a.Tracks[0].Chain = append(a.Tracks[0].Chain, Slot{ID: 10, Path: goPath, Class: eq.Descriptor.ID, Name: "EQ",
		Values: map[uint32]float64{1: 0}})
	a.Patches = []PatchName{{Class: eq.Descriptor.ID, Name: "Mine"}, {Class: braid.Descriptor.ID, Name: "Not the EQ's"}}
	a.ShowGo, a.ShowGos = 10, 1
	w, r, run := stage(t, a)
	run(60)
	sh := r.pane.sheets[r.goViews[10]]
	if sh == nil {
		t.Fatal("the EQ's sheet is not shown")
	}
	if sh.browsing || boundsOfSize(t, w, run, sh.browser) != (geom.Size{}) {
		t.Fatal("the browser shows before it is asked to")
	}
	click(t, w, run, sh.browse)
	run(30)
	if _, rest := edits(w); !slices.Contains(rest, gunim.Intent(BrowsePresets{On: true})) {
		t.Fatalf("the browser's button sent %v", rest)
	}
	if !sh.browsing || boundsOfSize(t, w, run, sh.browser).W < 100 {
		t.Fatal("asked to, the browser does not show")
	}
	var names []string
	for _, p := range sh.src.Presets() {
		names = append(names, p.Bank+"/"+p.Group+"/"+p.Name)
	}
	if len(names) != 1+len(eq.Descriptor.Patches) || names[0] != "User/EQ/Mine" || names[1] != "Factory/EQ/Flat" {
		t.Fatalf("the browser lists %q", names)
	}
	pick := func(i int) gunim.Intent {
		ps := sh.src.Presets()
		gunim.RegisterPatch(w, "album", func(_ *root, _ struct{}, u *gunim.UI) { sh.src.Pick(ps[i], u) })
		if err := w.Client().Patch("album", struct{}{}); err != nil {
			t.Fatal(err)
		}
		run(1)
		_, rest := edits(w)
		if len(rest) != 1 {
			t.Fatalf("a pick sent %v", rest)
		}
		return rest[0]
	}
	if in := pick(2); in != (LoadPatch{Track: 1, Slot: 10, Name: "Warm", Own: true}) {
		t.Errorf("Warm picked sends %v", in)
	}
	if in := pick(0); in != (LoadPatch{Track: 1, Slot: 10, Name: "Mine"}) {
		t.Errorf("Mine picked sends %v", in)
	}
	// The patch kept that plays can be deleted; one the EQ comes with
	// cannot.
	idx := slices.IndexFunc(a.Tracks[0].Chain, func(s Slot) bool { return s.ID == 10 })
	a.Tracks[0].Chain[idx].Preset = PresetRef{Bank: "Factory", Group: "EQ", Name: "Warm"}
	a.Tracks[0].Chain[idx].Playing = true
	_ = w.Client().Publish(albumTopic, a)
	run(5)
	if cur, ok := sh.src.Current(); !ok || cur.Name != "Warm" || !sh.drop.Disabled {
		t.Fatalf("Warm playing, the browser's current is %v %v, and delete is enabled %v", cur, ok, !sh.drop.Disabled)
	}
	a.Tracks[0].Chain[idx].Preset = PresetRef{Bank: "User", Group: "EQ", Name: "Mine"}
	_ = w.Client().Publish(albumTopic, a)
	run(5)
	if sh.drop.Disabled {
		t.Fatal("Mine playing, it cannot be deleted")
	}
	click(t, w, run, sh.drop)
	if _, rest := edits(w); !slices.Contains(rest, gunim.Intent(DeletePatch{Class: eq.Descriptor.ID, Name: "Mine"})) {
		t.Fatalf("delete sent %v", rest)
	}
	// Settings typed into the field are kept as a patch.
	gunim.RegisterPatch(w, "album", func(_ *root, _ struct{}, u *gunim.UI) {
		if in := sh.keep.OnCommit(" Airy ", u); in != (SavePatch{Track: 1, Slot: 10, Name: " Airy "}) {
			t.Errorf("keeping sends %v", in)
		}
		if in := sh.keep.OnCommit("  ", u); in != nil {
			t.Errorf("keeping no name sends %v", in)
		}
	})
	_ = w.Client().Patch("album", struct{}{})
	run(1)
}

// click clicks n in the window.
func click(t *testing.T, w *gunim.Window, run func(int), n gunim.Node) {
	t.Helper()
	at := boundsOf(t, w, run, n).Center()
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	run(2)
}

// boundsOfSize is the size n stands at in the window.
func boundsOfSize(t *testing.T, w *gunim.Window, run func(int), n gunim.Node) geom.Size {
	t.Helper()
	return boundsOf(t, w, run, n).Size()
}

func TestThePatchLoadedShowsAsPlayingUntilTheSettingsChange(t *testing.T) {
	a, tr, s := goApp(t)
	a.patchesFile = filepath.Join(t.TempDir(), "patches.json")
	playing := func() (PresetRef, bool) {
		a.showGoSettings()
		// Undone, the album's tracks are anew.
		now, i := a.slot(tr.ID, s.ID)
		return now.Chain[i].Preset, now.Chain[i].Playing
	}
	if _, on := playing(); on {
		t.Fatal("added, the braid plays a patch")
	}
	a.handle(SetGoParam{Track: tr.ID, Slot: s.ID, ID: braid.GainID, Value: -3, Gesture: 1})
	a.handle(SavePatch{Track: tr.ID, Slot: s.ID, Name: "Quiet"})
	if p, on := playing(); !on || p != (PresetRef{Bank: vst3.UserBank, Group: "Braid", Name: "Quiet"}) {
		t.Fatalf("kept, the braid plays %v (%v)", p, on)
	}
	a.handle(SetGoParam{Track: tr.ID, Slot: s.ID, ID: braid.GainID, Value: 2, Gesture: 2})
	if _, on := playing(); on {
		t.Fatal("changed, the braid still plays the patch")
	}
	a.handle(LoadPatch{Track: tr.ID, Slot: s.ID, Name: "Quiet"})
	if p, on := playing(); !on || p.Name != "Quiet" || p.Bank != vst3.UserBank {
		t.Fatalf("loaded, the braid plays %v (%v)", p, on)
	}
	a.handle(Undo{})
	if _, on := playing(); on {
		t.Fatal("the load undone, the braid still plays the patch")
	}
}

func TestAPatchTheEQComesWithShowsInItsGroup(t *testing.T) {
	a, tr, _ := goApp(t)
	a.handle(AddPlugin{Track: tr.ID, Choice: PluginChoice{Path: goPath, Class: eq.Descriptor.ID, Name: "EQ"}})
	s := tr.Chain[1]
	a.rackOf(tr)
	a.handle(LoadPatch{Track: tr.ID, Slot: s.ID, Name: "Warm", Own: true})
	a.showGoSettings()
	want := PresetRef{Bank: vst3.FactoryBank, Group: patchGroup(eq.Descriptor.ID, ""), Name: "Warm"}
	if k := slices.IndexFunc(eq.Descriptor.Patches, func(p plug.Patch) bool { return p.Name == "Warm" }); eq.Descriptor.Patches[k].Category != "" {
		want.Group = eq.Descriptor.Patches[k].Category
	}
	if got := tr.Chain[1]; !got.Playing || got.Preset != want {
		t.Fatalf("Warm loaded, the EQ plays %v (%v), want %v", got.Preset, got.Playing, want)
	}
}
