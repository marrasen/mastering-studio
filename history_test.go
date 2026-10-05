package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/input"
)

// savedApp returns measuredApp's album saved in its file, with a folder
// of drafts.
func savedApp(t *testing.T) *app {
	t.Helper()
	a := measuredApp(t)
	a.drafts = t.TempDir()
	if err := a.saveNow(); err != nil {
		t.Fatal(err)
	}
	return a
}

// gainIn returns the gain of the first track as the project at path
// keeps it.
func gainIn(t *testing.T, path string) float32 {
	t.Helper()
	p, ok := readProject(path)
	if !ok {
		t.Fatalf("no project at %s", path)
	}
	return p.Tracks[0].Edit.Gain
}

func TestAChangeGoesToTheDraftUntilSaved(t *testing.T) {
	a := savedApp(t)
	tr := a.Tracks[0]
	a.handle(SetEdit{ID: tr.ID, Edit: Edit{Gain: -3}, Seq: 1})
	if !a.Unsaved {
		t.Fatal("changed, the album is not marked unsaved")
	}
	a.save()
	if g := gainIn(t, a.file); g != 0 {
		t.Fatalf("kept as it changed, the project's file took the gain, %v", g)
	}
	if g := gainIn(t, a.draftPath()); g != -3 {
		t.Fatalf("the draft holds a gain of %v, want -3", g)
	}
	// Opened again, the draft is taken up, not saved.
	b := newApp(a.ctx, newDeck(audio.NewMixer()), a.file)
	b.drafts = a.drafts
	b.load()
	if !b.Unsaved || b.Tracks[0].Edit.Gain != -3 {
		t.Fatalf("opened again, the album has a gain of %v, unsaved %v", b.Tracks[0].Edit.Gain, b.Unsaved)
	}
	if err := b.saveNow(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(b.draftPath()); !os.IsNotExist(err) || gainIn(t, b.file) != -3 || b.Unsaved {
		t.Fatal("saved, the draft stays, or the file has not the gain")
	}
}

func TestMeasuresAndListeningAreNoChange(t *testing.T) {
	a := savedApp(t)
	a.handle(Pick{ID: a.Tracks[1].ID})
	a.handle(SetMatch{On: true})
	a.handle(SetView{View: ViewGram})
	a.handle(CalcLoudness{})
	if a.Unsaved || a.UndoLabel != "" {
		t.Fatalf("picking, matching and viewing left the album unsaved %v, with %q to undo", a.Unsaved, a.UndoLabel)
	}
	a.save()
	if p, _ := readProject(a.file); p.Current != 1 || !p.Match {
		t.Fatal("what changes no work is not kept in the project's file")
	}
	if _, err := os.Stat(a.draftPath()); !os.IsNotExist(err) {
		t.Fatal("with no change, a draft was written")
	}
}

func TestDiscardingLetsTheDraftGo(t *testing.T) {
	a := savedApp(t)
	a.handle(SetEdit{ID: a.Tracks[0].ID, Edit: Edit{Gain: -3}, Seq: 1})
	a.save()
	if !a.answered(CloseDiscard) {
		t.Fatal("discarding leaves the window open")
	}
	a.save()
	if _, err := os.Stat(a.draftPath()); !os.IsNotExist(err) || gainIn(t, a.file) != 0 {
		t.Fatal("discarded, the change is kept")
	}
	if a.answered(CloseCancel) {
		t.Fatal("cancelling closes the window")
	}
}

func TestUndoAndRedoAChangeAndItsGesture(t *testing.T) {
	a := savedApp(t)
	tr := a.Tracks[0]
	// A fade dragged: many changes, one step.
	for i := range 10 {
		a.handle(SetEdit{ID: tr.ID, Edit: Edit{FadeOut: Fade{Length: time.Duration(i+1) * 100 * time.Millisecond}}, Seq: i + 1})
	}
	a.handle(SetGap{Gap: 3 * time.Second})
	if len(a.undo) != 2 || a.UndoLabel != "the silence before every track" {
		t.Fatalf("a fade dragged and the gap set made %d steps, the last %q", len(a.undo), a.UndoLabel)
	}
	a.handle(Undo{})
	a.handle(Undo{})
	if a.Gap != time.Second || a.Tracks[0].Edit.FadeOut.Length != 0 {
		t.Fatalf("undone, the gap is %v and the fade %v", a.Gap, a.Tracks[0].Edit.FadeOut.Length)
	}
	if a.Unsaved || a.Undone != "Undid the fade out on "+tr.Title {
		t.Fatalf("undone back to as saved, unsaved %v, saying %q", a.Unsaved, a.Undone)
	}
	// The measure as it was with the edit comes back with it: fresh.
	if a.Tracks[0].Stale || !a.Tracks[0].Measured {
		t.Fatal("undone, the track needs measuring again")
	}
	a.handle(Redo{})
	if a.Tracks[0].Edit.FadeOut.Length != time.Second || !a.Unsaved || a.RedoLabel != "the silence before every track" {
		t.Fatalf("redone, the fade is %v, unsaved %v, %q to redo", a.Tracks[0].Edit.FadeOut.Length, a.Unsaved, a.RedoLabel)
	}
	// A change after an undo leaves nothing to redo.
	a.handle(SetTarget{LUFS: -10})
	if a.RedoLabel != "" {
		t.Fatalf("after a new change, %q is left to redo", a.RedoLabel)
	}
}

func TestUndoingARemovalBringsTheTrackBack(t *testing.T) {
	a := savedApp(t)
	gone := a.Tracks[1]
	a.handle(RemoveTrack{ID: gone.ID})
	a.handle(Undo{})
	if len(a.Tracks) != 3 || a.Tracks[1].ID != gone.ID || a.Tracks[1].File != gone.File || !a.Tracks[1].Measured {
		t.Fatalf("undone, the tracks are %d, the second %d", len(a.Tracks), a.Tracks[1].ID)
	}
	if a.Current != gone.ID {
		t.Fatal("undone, the track brought back is not shown")
	}
}

func TestAReferenceChangesNoAlbum(t *testing.T) {
	a := savedApp(t)
	a.handle(AddReferences{Paths: []string{a.Tracks[0].File}})
	settle(t, a, func() bool { return a.References[0].Measured })
	a.handle(SetEdit{ID: a.References[0].ID, Edit: Edit{Gain: -2}, Seq: 1})
	if a.Unsaved || len(a.undo) != 0 {
		t.Fatal("a reference changed marks the album changed")
	}
}

func TestCtrlZUndoesAndCtrlSSaves(t *testing.T) {
	w, _, run := stage(t, album())
	for _, k := range []input.KeyPress{
		{Key: input.KeyZ, Mods: input.ModControl},
		{Key: input.KeyZ, Mods: input.ModControl | input.ModShift},
		{Key: input.KeyS, Mods: input.ModControl},
		{Key: input.KeyS, Mods: input.ModSuper | input.ModShift},
	} {
		w.Input(k)
	}
	run(1)
	_, rest := edits(w)
	want := []any{Undo{}, Redo{}, SaveAlbum{}, SaveAlbumAs{}}
	if len(rest) != len(want) {
		t.Fatalf("the keys sent %v", rest)
	}
	for i := range want {
		if rest[i] != want[i] {
			t.Fatalf("the keys sent %v, want %v", rest, want)
		}
	}
}

func TestADraftIsOfItsProject(t *testing.T) {
	a := &app{drafts: "/d"}
	a.file = filepath.Join(t.TempDir(), "One"+albumExt)
	one := a.draftPath()
	a.file = filepath.Join(t.TempDir(), "Two"+albumExt)
	if one == a.draftPath() || filepath.Dir(one) != "/d" {
		t.Fatalf("drafts at %s and %s", one, a.draftPath())
	}
}

func TestUndoingAPluginRemovedBringsItBackAsSet(t *testing.T) {
	a := chainApp(t, audio.NewMixer())
	tr := a.Tracks[0]
	s := tr.Chain[0]
	state := a.states[s.ID]
	a.handle(RemovePlugin{Track: tr.ID, Slot: s.ID})
	if len(a.Tracks[0].Chain) != 0 {
		t.Fatal("the plugin stayed")
	}
	a.handle(Undo{})
	if c := a.Tracks[0].Chain; len(c) != 1 || c[0].ID != s.ID || len(a.states[s.ID]) == 0 ||
		!bytes.Equal(a.states[s.ID], state) {
		t.Fatalf("undone, the chain is %+v", c)
	}
	// Under it, the chain as it was set up: copied to every track.
	if a.UndoLabel != "copying the chain of "+tr.Title || a.RedoLabel != "removing "+s.Name+" on "+tr.Title {
		t.Fatalf("undone, %q is left to undo and %q to redo", a.UndoLabel, a.RedoLabel)
	}
}
