package main

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// abApp returns an application of writeAlbum's three tracks, and the
// first of them again as a reference, every one measured, playing
// through mix.
func abApp(t *testing.T) (*app, *audio.Mixer) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mix := audio.NewMixer()
	a := newApp(ctx, newDeck(mix), "")
	paths := writeAlbum(t)
	for _, p := range paths {
		a.add(p, "")
	}
	a.handle(AddReferences{Paths: paths[2:]})
	settle(t, a, func() bool {
		for _, tr := range append(a.Tracks, a.References...) {
			if !tr.Measured {
				return false
			}
		}
		return true
	})
	return a, mix
}

// seconds mixes d of sound.
func seconds(mix *audio.Mixer, d time.Duration) {
	mix.Mix(make([]float32, 2*int(d.Seconds()*audio.SampleRate)))
}

func TestEachSideKeepsItsTrackAndPlace(t *testing.T) {
	a, mix := abApp(t)
	one, ref := a.Tracks[0].ID, a.References[0].ID
	a.handle(TogglePlay{})
	seconds(mix, time.Second)
	a.handle(SwitchSide{})
	if a.Side != SideB || a.Current != ref || a.Playing {
		t.Fatalf("B took up track %d, playing %v, want the reference, %d, paused", a.Current, a.Playing, ref)
	}
	if a.Away.Current != one || !a.Away.Playing || (a.Away.At-time.Second).Abs() > 50*time.Millisecond {
		t.Fatalf("A was left as %+v, want track %d playing a second in", a.Away, one)
	}
	a.handle(TogglePlay{})
	seconds(mix, 500*time.Millisecond)
	a.handle(SwitchSide{})
	at, _, id := a.d.position()
	if a.Side != SideA || id != one || !a.Playing || (at-time.Second).Abs() > 50*time.Millisecond {
		t.Fatalf("back on A, track %d plays at %v, playing %v, want track %d where it was left, a second in", id, at, a.Playing, one)
	}
	if a.Away.Current != ref || !a.Away.Playing || (a.Away.At-500*time.Millisecond).Abs() > 50*time.Millisecond {
		t.Fatalf("B was left as %+v, want the reference playing half a second in", a.Away)
	}
}

func TestMatchingLevelsHoldsForBothSides(t *testing.T) {
	a, _ := abApp(t)
	a.handle(SetMatch{On: true})
	a.handle(SwitchSide{})
	ref := a.References[0]
	want := math.Pow(10, float64(a.Target-ref.Measure.LUFS)/20)
	if math.Abs(float64(a.d.match)-want) > 1e-3 {
		t.Fatalf("on B the reference is matched by %.3f, want %.3f", a.d.match, want)
	}
}

func TestTheSideAwayPlaysOnInTheBackground(t *testing.T) {
	al := album()
	two := al.Tracks[0]
	two.ID = 2
	al.Tracks = append(al.Tracks, two)
	// Each track plays a second of silence, then its ten seconds.
	left := time.Now()
	al.Away = Session{Current: 1, At: 2 * time.Second, Playing: true, Left: left}
	if w := al.awayAt(left.Add(3 * time.Second)); w.At != 2*time.Second {
		t.Fatalf("with the background off, B moved to %v", w.At)
	}
	al.Background = true
	if w := al.awayAt(left.Add(3 * time.Second)); w.Current != 1 || w.At != 5*time.Second || !w.Playing {
		t.Fatalf("three seconds on, B is %+v, want five seconds into track 1", w)
	}
	if w := al.awayAt(left.Add(20 * time.Second)); w.Playing || w.At != 0 {
		t.Fatalf("past its end, B is %+v, want stopped", w)
	}
	al.AlbumPlay = true
	if w := al.awayAt(left.Add(10 * time.Second)); w.Current != 2 || w.At != time.Second || !w.Playing {
		t.Fatalf("playing on, B is %+v, want a second into track 2", w)
	}
	// Looping from 2 to 4 s of the file: 3 to 5 s as rendered.
	al.Tracks[0].Loop = &Loop{In: 2 * time.Second, Out: 4 * time.Second}
	al.Away.Looping, al.Away.At = true, 4*time.Second
	if w := al.awayAt(left.Add(3 * time.Second)); w.Current != 1 || w.At != 3*time.Second {
		t.Fatalf("looping, three seconds on, B is %+v, want at 3 s, round the loop", w)
	}
}

func TestReferencesAreKeptForEveryProject(t *testing.T) {
	a, _ := abApp(t)
	dir := t.TempDir()
	a.refsFile = filepath.Join(dir, "references.json")
	a.file = filepath.Join(dir, "One"+albumExt)
	a.Tracks[1].Loop = &Loop{In: time.Second, Out: 2 * time.Second}
	a.handle(SwitchSide{})
	a.dirty = true
	a.save()
	ref := a.References[0]
	if ref.Silence == nil || *ref.Silence != 0 {
		t.Fatal("a reference has silence before it")
	}
	b := newApp(a.ctx, newDeck(audio.NewMixer()), a.file)
	b.refsFile = a.refsFile
	b.loadRefs()
	b.load()
	if len(b.References) != 1 || b.References[0].File != ref.File || !b.References[0].Measured ||
		b.References[0].Silence == nil || *b.References[0].Silence != 0 {
		t.Fatalf("the references came back as %+v", b.References)
	}
	if b.Current != b.Tracks[0].ID {
		t.Fatal("saved on B, the project keeps B's track in place of A's")
	}
	// Another project opened, the references stay, and B on one.
	b.handle(SwitchSide{})
	b.switchTo(filepath.Join(dir, "Two"+albumExt), true)
	if len(b.Tracks) != 0 || len(b.References) != 1 || b.Side != SideA || b.Away.Current != b.References[0].ID {
		t.Fatalf("in a new project, %d tracks, %d references, side %d, B on %d", len(b.Tracks), len(b.References),
			b.Side, b.Away.Current)
	}
}

func TestTheBCardAndXSwitchSidesAndAReferencePlays(t *testing.T) {
	a := album()
	ref := a.Tracks[0]
	ref.ID, ref.Title = 9, "Reference"
	a.References = []Track{ref}
	w, r, run := stage(t, a)
	click := func(at geom.Point) {
		w.Input(input.PointerMove{Pos: at})
		w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
		w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
		run(1)
	}
	bar := boundsOf(t, w, run, r.ab)
	click(bar.Min.Add(r.ab.card(SideA).Center()))
	click(bar.Min.Add(r.ab.card(SideB).Center()))
	w.Input(input.KeyPress{Key: input.KeyX})
	run(1)
	if _, rest := edits(w); len(rest) != 2 || rest[0] != (SwitchSide{}) || rest[1] != (SwitchSide{}) {
		t.Fatalf("a click on A, one on B and X sent %v, want two switches", rest)
	}
	refs := boundsOf(t, w, run, r.refs)
	click(refs.Min.Add(geom.Pt(60, rowH/2)))
	if _, rest := edits(w); len(rest) != 1 || rest[0] != (Pick{ID: 9}) {
		t.Fatalf("a click on the reference sent %v", rest)
	}
}
