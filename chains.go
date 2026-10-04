package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
)

// The application's half of the tracks' chains: their plugins, loaded
// for the tracks heard, and the states the album keeps.

// rateOf returns the rate track t's file plays at.
func rateOf(t *Track) int {
	if t.Scanned && t.Format.SampleRate > 0 {
		return t.Format.SampleRate
	}
	_, format, closer, err := openTrack(t.File)
	if err != nil {
		return 44100
	}
	closer()
	return format.SampleRate
}

// rackOf returns track t's chain, loaded, loading it the first time.
func (a *app) rackOf(t *Track) *rack {
	if r := a.racks[t.ID]; r != nil {
		return r
	}
	r, failed := newRack(t.Chain, a.states, rateOf(t), false)
	for i := range t.Chain {
		s := &t.Chain[i]
		s.Failed = ""
		if err := failed[s.ID]; err != nil {
			s.Failed = err.Error()
			a.Note = fmt.Sprintf("%s: %v", s.Name, err)
		}
		if lp := r.find(s.ID); lp != nil {
			s.Latency = lp.p.Latency()
		}
	}
	// Only the track heard runs its chain.
	if !a.Playing || a.Current != t.ID {
		r.setActive(false)
	}
	a.racks[t.ID] = r
	return r
}

// dropRack lets track id's chain go, its plugins' states read first.
func (a *app) dropRack(id int) {
	r := a.racks[id]
	if r == nil {
		return
	}
	a.readRack(id)
	delete(a.racks, id)
	r.close()
}

// closeRacks lets every chain loaded go, their states as they are.
func (a *app) closeRacks() {
	for id, r := range a.racks {
		delete(a.racks, id)
		r.close()
	}
}

// shutdown ends the program's work with plugins as it ends: what runs
// in the background stops, the chains loaded are let go, and, once
// nothing runs in them, the plugins' modules are unloaded. A module
// unloaded under a plugin at work, or the process ending under one,
// crashes it.
func (a *app) shutdown() {
	a.stop()
	a.d.stop()
	stopped := make(chan struct{})
	go func() {
		a.work.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		// Still at work: the modules stay loaded, for the process to
		// end with.
		a.closeRacks()
		return
	}
	a.closeRacks()
	closeModules()
}

// readRack reads the states of track id's plugins, loaded, and returns
// whether one changed.
func (a *app) readRack(id int) bool {
	r := a.racks[id]
	if r == nil {
		return false
	}
	r.flush()
	r.mu.Lock()
	ps := slices.Clone(r.plugins)
	r.mu.Unlock()
	changed := false
	for _, lp := range ps {
		st, err := lp.p.State()
		if err != nil || bytes.Equal(st, a.states[lp.slot]) {
			continue
		}
		a.states[lp.slot] = st
		changed = true
	}
	return changed
}

// readStates reads the states of every plugin loaded.
func (a *app) readStates() {
	for id := range a.racks {
		a.readRack(id)
	}
}

// chainOf returns track t's chain and its plugins' states, as they are
// now, for a copy to measure or export with.
func (a *app) chainOf(t *Track) (chain []Slot, states map[int][]byte) {
	if len(t.Chain) == 0 {
		return nil, nil
	}
	a.readRack(t.ID)
	states = map[int][]byte{}
	for _, s := range t.Chain {
		states[s.ID] = a.states[s.ID]
	}
	return slices.Clone(t.Chain), states
}

// pluginSettle is how long a chain rests before it is measured again:
// longer than an edit's, for a plugin set by hand changes for a while.
const pluginSettle = 1500 * time.Millisecond

// watchPlugins follows the plugins loaded: which editors are open, and
// whose state changed, to keep and measure again. It returns whether
// anything changed.
func (a *app) watchPlugins() bool {
	changed := false
	for id, r := range a.racks {
		t := a.track(id)
		if t == nil {
			continue
		}
		r.mu.Lock()
		ps := slices.Clone(r.plugins)
		r.mu.Unlock()
		read := false
		for _, lp := range ps {
			i := slices.IndexFunc(t.Chain, func(s Slot) bool { return s.ID == lp.slot })
			if i < 0 {
				continue
			}
			s := &t.Chain[i]
			open, edits := lp.p.EditorOpen(), lp.p.Edits()
			if open || edits != lp.edits {
				read = true
			}
			lp.edits = edits
			if lat := lp.p.Latency(); open != s.Open || lat != s.Latency {
				s.Open, s.Latency = open, lat
				r.changed()
				changed = true
			}
		}
		if read && a.readRack(id) {
			a.dirty = true
			a.remeasureChain(id)
			changed = true
		}
	}
	return changed
}

// remeasureChain measures track id again once its chain has rested.
func (a *app) remeasureChain(id int) {
	a.remeasure(id)
	a.settle[id] = time.Now().Add(pluginSettle)
}

// slot returns track id's slot of ID sid, and its place, or nil.
func (a *app) slot(id, sid int) (t *Track, at int) {
	t = a.track(id)
	if t == nil {
		return nil, -1
	}
	return t, slices.IndexFunc(t.Chain, func(s Slot) bool { return s.ID == sid })
}

func (a *app) handleChain(in gunim.Intent) {
	switch in := in.(type) {
	case AddPlugin:
		t := a.track(in.Track)
		if t == nil {
			return
		}
		a.slots++
		c := in.Choice
		s := Slot{ID: a.slots, Path: c.Path, Class: c.Class, Name: c.Name, Vendor: c.Vendor}
		if r := a.racks[t.ID]; r != nil {
			lp, err := newPlugin(s, nil, rateOf(t), false)
			if err != nil {
				a.Note = fmt.Sprintf("%s: %v", c.Name, err)
				return
			}
			r.mu.Lock()
			r.plugins = append(r.plugins, lp)
			r.gen++
			r.mu.Unlock()
			s.Latency = lp.p.Latency()
			if st, err := lp.p.State(); err == nil {
				a.states[s.ID] = st
			}
		}
		t.Chain = append(t.Chain, s)
		a.dirty = true
		a.remeasureChain(t.ID)
		// A plugin added is to be set: its editor opens.
		a.handleChain(ShowEditor{Track: t.ID, Slot: s.ID})
	case RemovePlugin:
		t, i := a.slot(in.Track, in.Slot)
		if i < 0 {
			return
		}
		t.Chain = slices.Delete(t.Chain, i, i+1)
		delete(a.states, in.Slot)
		if r := a.racks[t.ID]; r != nil {
			r.mu.Lock()
			var gone *livePlugin
			r.plugins = slices.DeleteFunc(r.plugins, func(lp *livePlugin) bool {
				if lp.slot == in.Slot {
					gone = lp
					return true
				}
				return false
			})
			r.gen++
			r.mu.Unlock()
			if gone != nil {
				gone.p.Close()
			}
		}
		a.dirty = true
		a.remeasureChain(t.ID)
	case MovePlugin:
		t := a.track(in.Track)
		if t == nil || in.From < 0 || in.From >= len(t.Chain) || in.To < 0 || in.To >= len(t.Chain) || in.From == in.To {
			return
		}
		s := t.Chain[in.From]
		t.Chain = slices.Insert(slices.Delete(t.Chain, in.From, in.From+1), in.To, s)
		if r := a.racks[t.ID]; r != nil {
			r.mu.Lock()
			order := map[int]int{}
			for k, s := range t.Chain {
				order[s.ID] = k
			}
			slices.SortStableFunc(r.plugins, func(x, y *livePlugin) int { return order[x.slot] - order[y.slot] })
			r.gen++
			r.mu.Unlock()
		}
		a.dirty = true
		a.remeasureChain(t.ID)
	case SetBypass:
		t, i := a.slot(in.Track, in.Slot)
		if i < 0 {
			return
		}
		t.Chain[i].Bypass = in.On
		if r := a.racks[t.ID]; r != nil {
			if lp := r.find(in.Slot); lp != nil {
				r.mu.Lock()
				lp.setBypass(in.On)
				r.gen++
				r.mu.Unlock()
			}
		}
		a.dirty = true
		a.remeasureChain(t.ID)
	case ShowEditor:
		t, i := a.slot(in.Track, in.Slot)
		if i < 0 {
			return
		}
		lp := a.rackOf(t).find(in.Slot)
		if lp == nil {
			return
		}
		if err := lp.p.OpenEditor(fmt.Sprintf("%s · %s", t.Chain[i].Name, t.Title)); err != nil {
			a.Note = err.Error()
			return
		}
		t.Chain[i].Open = true
	case CopyChain:
		from := a.track(in.From)
		if from == nil {
			return
		}
		chain, states := a.chainOf(from)
		at, _, _ := a.d.position()
		to := in.To
		if len(to) == 0 {
			for _, t := range a.Tracks {
				if t.ID != from.ID {
					to = append(to, t.ID)
				}
			}
		}
		for _, id := range to {
			t := a.track(id)
			if t == nil || id == from.ID {
				continue
			}
			a.dropRack(id)
			for _, s := range t.Chain {
				delete(a.states, s.ID)
			}
			t.Chain = nil
			for _, s := range chain {
				a.slots++
				c := s
				c.ID, c.Open, c.Failed = a.slots, false, ""
				t.Chain = append(t.Chain, c)
				a.states[c.ID] = bytes.Clone(states[s.ID])
			}
			a.remeasureChain(id)
			if a.Current == id && a.Playing {
				// It plays on through its new chain.
				a.play(at, 15*time.Millisecond)
			}
		}
		a.dirty = true
	}
}

// replace gives track id the file at path, keeping its title, edit and
// chain: read and measured anew, and, while it plays, played on from
// the same moment.
func (a *app) replace(id int, path string) {
	t := a.track(id)
	if t == nil || path == "" || path == t.File {
		return
	}
	_, format, closer, err := openTrack(path)
	if err != nil {
		a.Note = fmt.Sprintf("%s: %v", filepath.Base(path), err)
		return
	}
	closer()
	// A chain runs at its file's rate: another rate loads it anew.
	if format.SampleRate != rateOf(t) {
		a.dropRack(id)
	}
	t.File = path
	t.Scanned, t.Wave, t.Frames, t.Format = false, nil, 0, format
	t.Exported, t.Out = "", Measure{}
	a.scan(id, path)
	a.remeasure(id)
	a.dirty = true
	if a.Current == id && a.d.done() != nil {
		at, _, _ := a.d.position()
		if err := a.d.play(t.ID, t.File, a.Gap, t.Edit, a.rackOf(t), at, !a.Playing, 15*time.Millisecond); err != nil {
			a.Note = err.Error()
		}
	}
}

// queuedKey is what a track queued to play next was queued as, so it
// is queued anew only once that changes.
type queuedKey struct {
	id   int
	file string
	edit Edit
	gap  time.Duration
	rack *rack
}

// queueNext has the deck play the track after the one playing once it
// ends, in album play, and nothing otherwise.
func (a *app) queueNext() {
	var want queuedKey
	if i := a.place(a.Current); a.AlbumPlay && a.d.done() != nil && i >= 0 && i+1 < len(a.Tracks) {
		n := &a.Tracks[i+1]
		want = queuedKey{id: n.ID, file: n.File, edit: n.Edit, gap: a.Gap, rack: a.rackOf(n)}
	}
	if want == a.queued && (want.id == 0 || a.d.hasNext()) {
		return
	}
	a.queued = want
	if want.id == 0 {
		a.d.unqueue()
		return
	}
	if err := a.d.queue(want.id, want.file, want.gap, want.edit, want.rack); err != nil {
		a.Note = err.Error()
		a.queued = queuedKey{}
	}
}

// turned takes the deck's turn to track id, queued.
func (a *app) turned(id int) {
	if a.track(id) == nil {
		return
	}
	a.Current = id
	a.queued = queuedKey{}
	a.dirty = true
	a.applyLevel()
}

// measureAlbum measures the album's loudness and its range, every
// track's measures together, once every track is measured.
func (a *app) measureAlbum() {
	a.Loudness = Measure{}
	var blocks, shorts []float64
	for _, t := range a.Tracks {
		if !t.Measured {
			return
		}
		blocks = append(blocks, t.Measure.blocks...)
		shorts = append(shorts, t.Measure.shorts...)
		a.Loudness.Length += t.Measure.Length
		a.Loudness.TruePeak = max(a.Loudness.TruePeak, t.Measure.TruePeak)
	}
	if l, ok := audio.Integrated(blocks); ok {
		a.Loudness.LUFS, a.Loudness.Loud = float32(l), true
	}
	if low, high, ok := audio.LoudnessRange(shorts); ok {
		a.Loudness.Low, a.Loudness.High, a.Loudness.LRA, a.Loudness.Ranged = float32(low), float32(high), float32(high-low), true
	}
}
