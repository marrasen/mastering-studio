package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/driver"
)

// Listening A and B: two sessions of listening, each with a track, a
// moment of it, playing or not, and looping or not. One is heard; a
// switch leaves it where it is and takes up the other where it was
// left, or, as Background has it, where it would be had it played on.
// Either may play a track of the album or a reference: tracks to
// compare the album with, the same in every project.

// Side is one of the two sessions of listening.
type Side int

const (
	SideA Side = iota
	SideB
)

// Session is a session of listening as it was left: its track, the
// moment of it, in the render's time, whether it played and looped,
// and when it was left.
type Session struct {
	Current int
	At      time.Duration
	Playing bool
	Looping bool
	Left    time.Time
}

type (
	// SwitchSide switches to the other session of listening.
	SwitchSide struct{}
	// SetBackground sets whether the session not heard plays on.
	SetBackground struct{ On bool }
	// AddReferences adds files to the references, at their end.
	AddReferences struct{ Paths []string }
	// ChooseReferences asks, with the system's dialog, for references to
	// add.
	ChooseReferences struct{}
)

// find returns the track with ID id, of the album or the references,
// or nil.
func (s *Album) find(id int) *Track {
	for _, ts := range [][]Track{s.Tracks, s.References} {
		for i := range ts {
			if ts[i].ID == id {
				return &ts[i]
			}
		}
	}
	return nil
}

// listOf returns the tracks track id is among: the album's, or the
// references.
func (s *Album) listOf(id int) []Track {
	if indexOf(s.References, id) >= 0 {
		return s.References
	}
	return s.Tracks
}

// isRef says track id is a reference.
func (s *Album) isRef(id int) bool { return indexOf(s.References, id) >= 0 }

// awayAt is the session not heard as it is at now: where it was left,
// or, playing on in the background, as far on as now is from then,
// round its loop, or on into the tracks after it as the album plays on,
// or stopped at its end.
func (s *Album) awayAt(now time.Time) Session {
	w := s.Away
	if !s.Background || !w.Playing || w.Current == 0 {
		return w
	}
	run := max(0, now.Sub(w.Left))
	w.Left = now
	for {
		t := s.find(w.Current)
		if t == nil {
			w.Playing = false
			return w
		}
		gap := s.gapOf(t)
		if w.Looping && t.Loop != nil {
			// The loop in the render's time, as the deck plays it.
			in := max(0, gap+t.Loop.In-t.Edit.Start)
			out := max(0, gap+t.Loop.Out-t.Edit.Start)
			if out > in && w.At < out {
				if w.At += run; w.At >= out {
					w.At = in + (w.At-in)%(out-in)
				}
				return w
			}
		}
		end := lengthOf(*t, gap)
		if w.At+run < end {
			w.At += run
			return w
		}
		run -= end - w.At
		list := s.listOf(w.Current)
		i := indexOf(list, w.Current)
		if !s.AlbumPlay || i < 0 || i+1 >= len(list) {
			w.Playing, w.At = false, 0
			return w
		}
		w.Current, w.At = list[i+1].ID, 0
	}
}

// sideA returns the track session A has, and whether it loops.
func (a *app) sideA() (current int, looping bool) {
	if a.Side == SideA {
		return a.Current, a.Looping
	}
	return a.Away.Current, a.Away.Looping
}

// switchSide leaves the session heard where it is, and takes up the
// other: where it was left, or would be had it played on. A session
// never used takes up the first reference, or the track picked.
func (a *app) switchSide() {
	now := time.Now()
	at, _, id := a.d.position()
	if id != a.Current {
		at = 0
	}
	there := a.awayAt(now)
	if a.track(there.Current) == nil {
		there = Session{Current: a.Current}
		if len(a.References) > 0 {
			there.Current = a.References[0].ID
		}
	}
	a.Away = Session{Current: a.Current, At: at, Playing: a.Playing, Looping: a.Looping, Left: now}
	a.Side = 1 - a.Side
	a.Current, a.Playing, a.Looping = there.Current, there.Playing, there.Looping
	a.queued = queuedKey{}
	switch {
	case a.track(a.Current) == nil:
		a.d.stop()
		a.Playing = false
	case a.Playing:
		a.play(there.At, 25*time.Millisecond)
	default:
		a.cue(there.At)
	}
	a.applyLevel()
}

// cue makes the track picked ready to play from at, paused.
func (a *app) cue(at time.Duration) {
	t := a.track(a.Current)
	if t == nil {
		return
	}
	if err := a.d.play(t.ID, t.File, a.gapOf(t), t.Edit, a.rackOf(t), at, true, 25*time.Millisecond); err != nil {
		a.Note = err.Error()
		return
	}
	a.Starts++
}

// remove takes track id off the album, or the references. The session
// heard picks the track after it, and one not heard is left with none.
func (a *app) remove(id int) {
	ts := &a.Tracks
	if a.isRef(id) {
		ts = &a.References
	}
	i := indexOf(*ts, id)
	if i < 0 {
		return
	}
	a.dropRack(id)
	for _, s := range (*ts)[i].Chain {
		delete(a.states, s.ID)
	}
	*ts = slices.Delete(*ts, i, i+1)
	if ts == &a.Tracks {
		a.measureAlbum()
	}
	if a.Current == id {
		a.d.stop()
		a.Playing = false
		a.Current = 0
		if len(*ts) > 0 {
			a.Current = (*ts)[min(i, len(*ts)-1)].ID
		}
	}
	if a.Away.Current == id {
		a.Away = Session{}
	}
	a.dirty = true
}

// handleAB handles the intents of the sessions and the references.
func (a *app) handleAB(in gunim.Intent) bool {
	switch in := in.(type) {
	case SwitchSide:
		a.switchSide()
	case SetBackground:
		// Turned on or off, the session not heard runs on from now, or
		// stays where it got to.
		a.Away = a.awayAt(time.Now())
		a.Background = in.On
		a.writeSettings()
	case AddReferences:
		for _, f := range soundFiles(in.Paths) {
			a.addTo(true, f, "", Edit{})
		}
	case ChooseReferences:
		go func() {
			paths, err := a.choose(driver.ChooseOptions{Title: "Add reference tracks", Multiple: true,
				Filters: []driver.FileFilter{{Name: "Sound", Patterns: []string{"*.wav", "*.flac", "*.mp3", "*.ogg"}}}})
			if err == nil && len(paths) > 0 {
				a.chosenRefs <- paths
			}
		}()
	default:
		return false
	}
	return true
}

// keptRefs are the references as kept beside the settings, their files
// whole.
type keptRefs struct {
	Tracks []keptTrack
}

// loadRefs takes up the references kept.
func (a *app) loadRefs() {
	if a.refsFile == "" {
		return
	}
	b, err := os.ReadFile(a.refsFile)
	if err != nil {
		return
	}
	var k keptRefs
	if json.Unmarshal(b, &k) != nil {
		return
	}
	for _, t := range k.Tracks {
		a.restore(t, true)
	}
}

// saveRefs keeps the references.
func (a *app) saveRefs() {
	if a.refsFile == "" {
		return
	}
	var k keptRefs
	for i := range a.References {
		t := a.kept(&a.References[i])
		t.File, t.At = a.References[i].File, ""
		k.Tracks = append(k.Tracks, t)
	}
	b, err := json.MarshalIndent(k, "", "\t")
	if err == nil {
		err = os.MkdirAll(filepath.Dir(a.refsFile), 0o755)
	}
	if err == nil {
		tmp := a.refsFile + ".tmp"
		if err = os.WriteFile(tmp, b, 0o644); err == nil {
			err = os.Rename(tmp, a.refsFile)
		}
	}
	if err != nil {
		log.Printf("mastering: keeping the references: %v", err)
	}
}
