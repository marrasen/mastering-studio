package main

import (
	"bytes"
	"fmt"
	"slices"
	"time"

	"github.com/marrasen/gunim"
)

// Undo and redo: the album's changes, one history for it all, each
// step the album as it was before a change, and named for it, as "fade
// out on Undertow". The steps of one gesture, as a drag or a plugin's
// knob turned, make one. Playing, picking, how the album is listened to
// and viewed, and the references, which are of every album, are not
// changes; nor are measures, but a step undone brings back the album's
// measures as they were with it, so no track needs measuring again.

type (
	// Undo takes the last change back.
	Undo struct{}
	// Redo makes the last change taken back again.
	Redo struct{}
)

// history is the album's changes: undo the steps to take back, the
// last last, and redo those taken back. stateID names the album as it
// is, each change a new name, and savedID the album as saved, so the
// album holds no change not saved where the two are the same.
type history struct {
	undo, redo []step
	lastChange time.Time
	stateID    int
	savedID    int
	seq        int
}

// step is a change: what it is called, the track it changed, the key
// that joins the changes of one gesture, the name of the album before
// it, and the album before it.
type step struct {
	label string
	track int
	key   string
	id    int
	snap  snapshot
}

// keptSteps is how many changes the history keeps.
const keptSteps = 200

// gesture is how soon a change with the key of the last joins it.
const gesture = 1500 * time.Millisecond

// snapshot is the album as a change finds it: its tracks, the states of
// their plugins, and its settings.
type snapshot struct {
	tracks    []Track
	states    map[int][]byte
	release   Release
	gap       time.Duration
	target    float32
	bits      int
	dither    bool
	exportDir string
	wav, mp3  bool
	mp3Rate   int
	report    bool
}

// cloneTracks copies ts, and what of theirs a change changes in place.
func cloneTracks(ts []Track) []Track {
	out := slices.Clone(ts)
	for i := range out {
		out[i].Chain = slices.Clone(out[i].Chain)
		out[i].Marks = slices.Clone(out[i].Marks)
	}
	return out
}

func (a *app) snapshot() snapshot {
	s := snapshot{tracks: cloneTracks(a.Tracks), states: map[int][]byte{}, release: a.Release, gap: a.Gap,
		target: a.Target, bits: a.Bits, dither: a.Dither, exportDir: a.ExportDir, wav: a.ExportWAV, mp3: a.ExportMP3,
		mp3Rate: a.MP3Rate, report: a.ExportReport}
	// A state is replaced as it changes, never written over: it is kept
	// as it is.
	for _, t := range a.Tracks {
		for _, sl := range t.Chain {
			s.states[sl.ID] = a.states[sl.ID]
		}
	}
	return s
}

// record takes a step of the album as it is, before a change called
// label, of track, or 0, joining the last where key is its key and it
// came a moment ago.
func (a *app) record(label string, track int, key string) {
	a.recordSnap(a.snapshot(), label, track, key)
}

// recordSnap takes s, the album before a change, as a step.
func (a *app) recordSnap(s snapshot, label string, track int, key string) {
	now := time.Now()
	n := len(a.undo)
	joins := n > 0 && key != "" && a.undo[n-1].key == key && now.Sub(a.lastChange) < gesture && len(a.redo) == 0
	if !joins {
		a.undo = append(a.undo, step{label: label, track: track, key: key, id: a.stateID, snap: s})
		if len(a.undo) > keptSteps {
			a.undo = slices.Delete(a.undo, 0, len(a.undo)-keptSteps)
		}
	}
	a.redo = nil
	a.lastChange = now
	a.seq++
	a.stateID = a.seq
	a.changed()
}

// changed tells of the album changed as the history has it.
func (a *app) changed() {
	a.Unsaved = a.stateID != a.savedID
	a.dirty = true
	a.UndoLabel, a.RedoLabel = "", ""
	if n := len(a.undo); n > 0 {
		a.UndoLabel = a.undo[n-1].label
	}
	if n := len(a.redo); n > 0 {
		a.RedoLabel = a.redo[n-1].label
	}
}

// undoStep takes the last change back, or, redo, makes the last taken
// back again.
func (a *app) undoStep(redo bool) {
	from, to, done := &a.undo, &a.redo, "Undid "
	if redo {
		from, to, done = &a.redo, &a.undo, "Redid "
	}
	n := len(*from)
	if n == 0 {
		return
	}
	s := (*from)[n-1]
	*from = (*from)[:n-1]
	*to = append(*to, step{label: s.label, track: s.track, key: s.key, id: a.stateID, snap: a.snapshot()})
	a.revert(s.snap)
	a.stateID = s.id
	// The next change starts a step of its own.
	a.lastChange = time.Time{}
	a.changed()
	a.Undone = done + s.label
	a.Undos++
	// The track changed is shown.
	if s.track != 0 && a.track(s.track) != nil && s.track != a.Current {
		a.handle(Pick{ID: s.track})
	}
}

// revert brings the album back to s. What runs on holds: the tracks'
// reading, measuring and exports, and their plugins, given their states
// as s has them, or loaded anew where the chain is other than it was.
// The track playing plays on as s has it.
func (a *app) revert(s snapshot) {
	at, _, _ := a.d.position()
	old := map[int]Track{}
	for _, t := range a.Tracks {
		old[t.ID] = t
	}
	replay, restart := s.gap != a.Gap && a.Playing, false
	a.Release, a.Gap, a.Target, a.Bits, a.Dither = s.release, s.gap, s.target, s.bits, s.dither
	a.ExportDir, a.ExportWAV, a.ExportMP3, a.MP3Rate, a.ExportReport = s.exportDir, s.wav, s.mp3, s.mp3Rate, s.report
	tracks := cloneTracks(s.tracks)
	for i := range tracks {
		t := &tracks[i]
		o, ok := old[t.ID]
		if !ok {
			// Back from being taken away: as it was, its rack loaded anew.
			a.version[t.ID]++
			if !t.Scanned {
				a.scan(t.ID, t.File)
			}
			continue
		}
		delete(old, t.ID)
		if o.File == t.File {
			t.Scanned, t.Format, t.Frames, t.Wave = o.Scanned, o.Format, o.Frames, o.Wave
			t.SoundStart, t.SoundEnd = o.SoundStart, o.SoundEnd
		} else {
			t.Scanned, t.Wave, t.Frames = false, nil, 0
			a.scan(t.ID, t.File)
		}
		t.Measuring, t.Matching, t.Progress = o.Measuring, o.Matching, o.Progress
		// The window takes the edit as newer than its own.
		t.Seq = o.Seq + 1
		for k := range t.Chain {
			if j := slices.IndexFunc(o.Chain, func(sl Slot) bool { return sl.ID == t.Chain[k].ID }); j >= 0 {
				t.Chain[k].Latency, t.Chain[k].Open, t.Chain[k].Failed = o.Chain[j].Latency, o.Chain[j].Open, o.Chain[j].Failed
			}
		}
		chained := !sameChain(t.Chain, o.Chain) || t.File != o.File
		edited := t.Edit != o.Edit || !sameSilence(t.Silence, o.Silence)
		if chained || edited || !sameStates(t.Chain, s.states, a.states) {
			a.version[t.ID]++
		}
		if chained {
			// Loaded anew, with the states s has.
			a.dropRack(t.ID)
		}
		if t.ID == a.Current && a.Playing {
			restart = restart || chained
			replay = replay || edited
		}
	}
	// The tracks s has not: taken away again.
	for id := range old {
		a.dropRack(id)
	}
	a.Tracks = tracks
	// The plugins' states, set on the plugins loaded.
	for id, st := range s.states {
		if bytes.Equal(a.states[id], st) {
			continue
		}
		a.states[id] = st
		for _, r := range a.racks {
			if lp := r.find(id); lp != nil {
				// Set, and read back as the plugin writes it, so the
				// watch on the plugins takes it for no change of the
				// user's.
				r.mu.Lock()
				if lp.p.SetState(st) == nil {
					if back, err := lp.p.State(); err == nil {
						a.states[id] = back
					}
				}
				lp.edits = lp.p.Edits()
				r.mu.Unlock()
			}
		}
	}
	if a.track(a.Current) == nil {
		a.d.stop()
		a.Playing, a.Current = false, 0
		if len(a.Tracks) > 0 {
			a.Current = a.Tracks[0].ID
		}
	}
	a.measureAlbum()
	a.applyLevel()
	switch {
	case restart:
		a.play(at, 15*time.Millisecond)
	case replay:
		a.replayEdit()
	}
}

// sameChain says two chains have the same plugins, in the same order,
// each bypassed or not alike.
func sameChain(x, y []Slot) bool {
	return slices.EqualFunc(x, y, func(p, q Slot) bool {
		return p.ID == q.ID && p.Path == q.Path && p.Class == q.Class && p.Bypass == q.Bypass
	})
}

// sameStates says the plugins of chain have the same states in a as in b.
func sameStates(chain []Slot, a, b map[int][]byte) bool {
	for _, s := range chain {
		if !bytes.Equal(a[s.ID], b[s.ID]) {
			return false
		}
	}
	return true
}

func sameSilence(x, y *time.Duration) bool {
	return (x == nil) == (y == nil) && (x == nil || *x == *y)
}

// change names the change intent in makes to the album, for its step:
// what it is called, the track it changes, or 0, and the key that joins
// the changes of a gesture. It returns false for an intent that changes
// no album: one of playing, picking, listening or viewing, or one that
// changes a reference.
func (a *app) change(in gunim.Intent) (label string, track int, key string, ok bool) {
	// of names the change of track id, where it is the album's.
	of := func(what string, id int) (string, int, string, bool) {
		t := a.track(id)
		if t == nil || a.isRef(id) {
			return "", 0, "", false
		}
		return what + " " + t.Title, id, "", true
	}
	slot := func(id, sid int) string {
		if t, i := a.slot(id, sid); i >= 0 {
			return t.Chain[i].title()
		}
		return "a plugin"
	}
	switch in := in.(type) {
	case AddFiles:
		return "adding tracks", 0, "", true
	case RemoveTrack:
		return of("removing", in.ID)
	case MoveTrack:
		if in.Refs || in.From < 0 || in.From >= len(a.Tracks) {
			return "", 0, "", false
		}
		return of("moving", a.Tracks[in.From].ID)
	case RenameTrack:
		return of("renaming", in.ID)
	case SetEdit:
		t := a.track(in.ID)
		if t == nil {
			return "", 0, "", false
		}
		part := editPart(t.Edit, in.Edit)
		label, track, _, ok = of(part+" on", in.ID)
		return label, track, fmt.Sprintf("edit %d %s", in.ID, part), ok
	case SetSilence:
		label, track, _, ok = of("the silence before", in.ID)
		return label, track, fmt.Sprintf("silence %d", in.ID), ok
	case SetNote:
		label, track, _, ok = of("the note on", in.ID)
		return label, track, fmt.Sprintf("note %d", in.ID), ok
	case AddMark:
		return of("a note at a time on", in.Track)
	case SetMark:
		label, track, _, ok = of("a note at a time on", in.Track)
		return label, track, fmt.Sprintf("mark %d %d", in.Track, in.ID), ok
	case RemoveMark:
		return of("removing a note at a time on", in.Track)
	case SetLoop:
		label, track, _, ok = of("the loop on", in.Track)
		return label, track, fmt.Sprintf("loop %d", in.Track), ok
	case ReplaceFile:
		return of("replacing the file of", in.ID)
	case SetGap:
		return "the silence before every track", 0, "gap", true
	case SetTarget:
		return "the target", 0, "target", true
	case SetExport, StartExport:
		return "the export settings", 0, "export", true
	case SetRelease:
		return "the release details", 0, "", true
	case AddPlugin:
		return of("adding "+in.Choice.Name+" on", in.Track)
	case RemovePlugin:
		return of("removing "+slot(in.Track, in.Slot)+" on", in.Track)
	case MovePlugin:
		if t := a.track(in.Track); t != nil && in.From >= 0 && in.From < len(t.Chain) {
			return of("moving "+t.Chain[in.From].title()+" on", in.Track)
		}
	case SetBypass:
		what := "bypassing "
		if !in.On {
			what = "switching on "
		}
		return of(what+slot(in.Track, in.Slot)+" on", in.Track)
	case RenamePlugin:
		return of("naming "+slot(in.Track, in.Slot)+" on", in.Track)
	case CopyChain:
		// Of a reference to the album's tracks, it changes the album.
		label, track, _, ok = of("copying the chain of", in.From)
		if t := a.track(in.From); t != nil && !ok {
			return "copying the chain of " + t.Title, 0, "", true
		}
		return label, track, "", ok
	}
	return "", 0, "", false
}

// editPart names what of edit was changes to become to: the cut, a
// fade, or a gain.
func editPart(was, to Edit) string {
	switch {
	case was.Start != to.Start || was.End != to.End:
		return "the cut"
	case was.FadeIn != to.FadeIn:
		return "the fade in"
	case was.FadeOut != to.FadeOut:
		return "the fade out"
	case was.Gain != to.Gain:
		return "the gain in"
	case was.Out != to.Out:
		return "the gain out"
	}
	return "the edit"
}
