package main

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// album returns an album of one track, ten seconds of a file read, with
// its sound from the second second.
func album() Album {
	w := &audioui.Wave{Frames: 10 * rate, Rate: rate}
	for ch := range 2 {
		w.Peak[ch] = make([]float32, audioui.WaveBuckets)
		w.RMS[ch] = make([]float32, audioui.WaveBuckets)
		for b := audioui.WaveBuckets / 5; b < audioui.WaveBuckets; b++ {
			w.Peak[ch][b], w.RMS[ch][b] = 0.5, 0.3
		}
	}
	t := Track{ID: 1, Title: "One", Scanned: true, Format: audio.Format{SampleRate: rate}, Frames: 10 * rate,
		Wave: w, SoundStart: 2 * time.Second, SoundEnd: 10 * time.Second}
	return Album{Tracks: []Track{t}, Gap: time.Second, Target: -14, Bits: 16, Dither: true, Current: 1, Volume: 0.8}
}

// stage mounts the window's view of a, offscreen, and returns the
// window, its root, and a way to step frames.
func stage(t *testing.T, a Album) (*gunim.Window, *root, func(int)) {
	t.Helper()
	d := newDeck(audio.NewMixer())
	var r *root
	w := gunim.NewOffscreen(geom.Sz(1440, 900), nil)
	gunim.RegisterView(w, "album",
		func(Album) *root { r = newRoot(d); return r },
		func(r *root, s Album, u *gunim.UI) { r.show(s, u) })
	if err := w.Client().Mount(gunim.Root, "album", "album", a, albumTopic); err != nil {
		t.Fatal(err)
	}
	_ = w.Client().Focus("album")
	run := func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(30)
	return w, r, run
}

// boundsOf returns where n is in the window.
func boundsOf(t *testing.T, w *gunim.Window, run func(int), n gunim.Node) geom.Rect {
	t.Helper()
	var b geom.Rect
	gunim.RegisterPatch(w, "album", func(_ *root, _ struct{}, u *gunim.UI) { b, _ = u.Bounds(n) })
	if err := w.Client().Patch("album", struct{}{}); err != nil {
		t.Fatal(err)
	}
	run(1)
	return b
}

// edits returns the edits the window sent, the last last.
func edits(w *gunim.Window) (out []SetEdit, rest []gunim.Intent) {
	for len(w.Client().Intents()) > 0 {
		in := (<-w.Client().Intents()).Intent
		if e, ok := in.(SetEdit); ok {
			out = append(out, e)
		} else {
			rest = append(rest, in)
		}
	}
	return out, rest
}

func TestTheCutsStartDragsWithThePointerEveryFrame(t *testing.T) {
	w, r, run := stage(t, album())
	ed := r.editor
	b := boundsOf(t, w, run, ed)
	from := b.Min.Add(geom.Pt(ed.xOf(0), b.Size().H/2))
	w.Input(input.PointerMove{Pos: from})
	w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1})
	run(1)
	for i := 1; i <= 12; i++ {
		to := b.Min.Add(geom.Pt(ed.xOf(float64(i)*0.15), b.Size().H/2))
		w.Input(input.PointerMove{Pos: to})
		run(1)
		got := ed.edit.Start.Seconds()
		if math.Abs(got-float64(i)*0.15) > 0.02 {
			t.Fatalf("step %d: the cut starts at %.3f s, want %.3f, under the pointer", i, got, float64(i)*0.15)
		}
	}
	w.Input(input.PointerUp{Pos: from, Button: input.ButtonPrimary})
	run(1)
	sent, _ := edits(w)
	if len(sent) == 0 || math.Abs(sent[len(sent)-1].Edit.Start.Seconds()-1.8) > 0.02 {
		t.Fatalf("the edits sent end at %v, want the start at 1.8 s", sent)
	}
	for i := 1; i < len(sent); i++ {
		if sent[i].Seq <= sent[i-1].Seq {
			t.Fatal("the edits' sequence numbers do not rise")
		}
	}
}

func TestAFadeHandleSetsTheFadesLength(t *testing.T) {
	a := album()
	a.Tracks[0].Edit = Edit{Start: 2 * time.Second}
	w, r, run := stage(t, a)
	ed := r.editor
	b := boundsOf(t, w, run, ed)
	in, _ := ed.handles()
	from := b.Min.Add(in)
	w.Input(input.PointerMove{Pos: from})
	w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1})
	to := b.Min.Add(geom.Pt(ed.xOf(2.5), in.Y))
	w.Input(input.PointerMove{Pos: to})
	w.Input(input.PointerUp{Pos: to, Button: input.ButtonPrimary})
	run(1)
	if l := ed.edit.FadeIn.Length.Seconds(); math.Abs(l-0.5) > 0.02 {
		t.Fatalf("the fade in is %.3f s, want 0.5", l)
	}
	// Half way through a linear fade, the sound is at half.
	if g := ed.envelope(2.25); math.Abs(g-0.5) > 0.02 {
		t.Fatalf("half way through the fade the gain is %.3f", g)
	}
}

func TestACurveTurnsIntoTheNextAndTheFadeMorphsWithIt(t *testing.T) {
	a := album()
	a.Tracks[0].Edit = Edit{Start: 2 * time.Second, FadeIn: Fade{Length: time.Second}}
	w, r, run := stage(t, a)
	b := boundsOf(t, w, run, r.tools.inCurve)
	w.Input(input.PointerMove{Pos: b.Min.Add(geom.Pt(10, 10))})
	w.Input(input.PointerDown{Pos: b.Min.Add(geom.Pt(10, 10)), Button: input.ButtonPrimary, Clicks: 1})
	run(1)
	ed := r.editor
	if ed.edit.FadeIn.Curve != Natural {
		t.Fatalf("a click made the curve %v, want Natural, the next", curveNames[ed.edit.FadeIn.Curve])
	}
	// The fade's gain at its middle walks from Linear's to Natural's.
	last := ed.envelope(2.5)
	for f := range 30 {
		g := ed.envelope(2.5)
		if g > last+1e-6 {
			t.Fatalf("frame %d: the fade's middle rose from %.4f to %.4f, turning toward Natural's lower", f, last, g)
		}
		last = g
		run(1)
	}
	if want := Natural.at(0.5); math.Abs(last-want) > 0.01 {
		t.Fatalf("after half a second the middle is %.3f, want Natural's %.3f", last, want)
	}
}

func TestNumberKeysPickTracks(t *testing.T) {
	a := album()
	two := a.Tracks[0]
	two.ID, two.Title = 2, "Two"
	a.Tracks = append(a.Tracks, two)
	w, _, run := stage(t, a)
	w.Input(input.KeyPress{Key: input.Key2})
	run(1)
	_, rest := edits(w)
	if len(rest) != 1 || rest[0] != (Pick{ID: 2}) {
		t.Fatalf("2 sent %v, want the second track picked", rest)
	}
}

// writeAlbum writes three tracks of different loudness.
func writeAlbum(t *testing.T) []string {
	t.Helper()
	paths := make([]string, 0, 3)
	for i, amp := range []float64{0.1, 0.3, 0.05} {
		p := writeTrack(t, time.Duration(i)*300*time.Millisecond, 3*time.Second, 0, amp)
		renamed := filepath.Join(filepath.Dir(p), []string{"a.wav", "b.wav", "c.wav"}[i])
		if err := os.Rename(p, renamed); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, renamed)
	}
	return paths
}

// settle runs the application's loop work until cond holds.
func settle(t *testing.T, a *app, cond func() bool) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for !cond() {
		select {
		case s := <-a.scans:
			a.scanned(s)
		case m := <-a.measures:
			a.measured(m)
		case p := <-a.progress:
			a.exportProgress(p)
		case <-time.After(20 * time.Millisecond):
			a.startMeasures()
		case <-deadline:
			t.Fatal("the application never settled")
		}
	}
}

func TestPickingAnotherTrackWhilePlayingKeepsTheMoment(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mix := audio.NewMixer()
	a := newApp(ctx, newDeck(mix), "")
	for _, p := range writeAlbum(t) {
		a.add(p, "", Edit{})
	}
	a.handle(TogglePlay{})
	// Two seconds in.
	mix.Mix(make([]float32, 2*2*audio.SampleRate))
	before, _, _ := a.d.position()
	a.handle(Pick{ID: a.Tracks[1].ID})
	after, _, id := a.d.position()
	if id != a.Tracks[1].ID || (after-before).Abs() > 20*time.Millisecond {
		t.Fatalf("picked at %v, the second track plays at %v (track %d), want the same moment", before, after, id)
	}
}

func TestMatchingLevelsBringsEachTrackToTheTarget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newApp(ctx, newDeck(audio.NewMixer()), "")
	for _, p := range writeAlbum(t) {
		a.add(p, "", Edit{})
	}
	settle(t, a, func() bool { return a.Tracks[2].Measured && a.Tracks[0].Measured })
	a.handle(SetMatch{On: true})
	want := math.Pow(10, float64(a.Target-a.Tracks[0].Measure.LUFS)/20)
	if math.Abs(float64(a.d.match)-want) > 1e-3 {
		t.Fatalf("matched by %.3f, want %.3f: the target over the track's loudness", a.d.match, want)
	}
}

func TestAnAlbumExportsEachTrackAtItsOwnLength(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newApp(ctx, newDeck(audio.NewMixer()), "")
	paths := writeAlbum(t)
	for _, p := range paths {
		a.add(p, "", Edit{})
	}
	a.Tracks[1].Edit = Edit{Start: 300 * time.Millisecond, End: 2 * time.Second}
	a.ExportDir = t.TempDir()
	a.handle(Export{})
	settle(t, a, func() bool { return !a.Exporting })
	want := []time.Duration{4 * time.Second, 2700 * time.Millisecond, 3600*time.Millisecond + time.Second}
	for i, tr := range a.Tracks {
		if tr.Exported == "" || filepath.Base(tr.Exported) != exportName(i+1, tr.Title)+".wav" {
			t.Fatalf("track %d exported to %q", i+1, tr.Exported)
		}
		if d := tr.Out.Length - want[i]; d.Abs() > time.Millisecond {
			t.Fatalf("track %d exported %v long, want %v: a second of silence and its own cut", i+1, tr.Out.Length, want[i])
		}
	}
}

func TestThePointerPassingOverTheListMovesNoTrack(t *testing.T) {
	a := album()
	two := a.Tracks[0]
	two.ID, two.Title = 2, "Two"
	a.Tracks = append(a.Tracks, two)
	w, r, run := stage(t, a)
	b := boundsOf(t, w, run, r.list)
	for i := range 20 {
		w.Input(input.PointerMove{Pos: b.Min.Add(geom.Pt(40, 10+float32(i)*12))})
		run(1)
		if r.list.moving != 0 {
			t.Fatalf("move %d: with no button down, the pointer drags track %d", i, r.list.moving)
		}
		if y := r.list.rows[1].y.Value(); y != 0 {
			t.Fatalf("move %d: the first track is at %.1f, want 0", i, y)
		}
	}
}

// chained returns the album with a chain of two plugins on its track.
func chained() Album {
	a := album()
	a.Tracks[0].Chain = []Slot{
		{ID: 7, Name: "Ozone 11 Equalizer With A Long Name", Vendor: "iZotope"},
		{ID: 8, Name: "Ozone 11 Maximizer", Vendor: "iZotope", Latency: 2048},
	}
	return a
}

func TestAClickOnACardsLightBypassesItsPluginAndOnItOpensItsEditor(t *testing.T) {
	w, r, run := stage(t, chained())
	b := boundsOf(t, w, run, r.chain)
	k := r.chain.cards[8]
	light := b.Min.Add(r.chain.powerAt(k))
	w.Input(input.PointerMove{Pos: light})
	w.Input(input.PointerDown{Pos: light, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: light, Button: input.ButtonPrimary})
	run(1)
	body := b.Min.Add(r.chain.cardRect(k).Center().Add(geom.Pt(20, 0)))
	w.Input(input.PointerMove{Pos: body})
	w.Input(input.PointerDown{Pos: body, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: body, Button: input.ButtonPrimary})
	run(1)
	_, sent := edits(w)
	want := []gunim.Intent{SetBypass{Track: 1, Slot: 8, On: true}, ShowEditor{Track: 1, Slot: 8}}
	if len(sent) != 2 || sent[0] != want[0] || sent[1] != want[1] {
		t.Fatalf("the clicks sent %v, want %v", sent, want)
	}
}

func TestTheChainsCardsLeaveRoomForItsButtons(t *testing.T) {
	a := chained()
	for i := range 4 {
		a.Tracks[0].Chain = append(a.Tracks[0].Chain, Slot{ID: 20 + i, Name: "Another Plugin Of Some Length", Vendor: "Somebody"})
	}
	_, r, run := stage(t, a)
	run(40)
	c := r.chain
	last := c.cards[c.order[len(c.order)-1]]
	end := c.cardRect(last).Max.X
	copyAt := c.size.W - pillWidth("Copy to…")
	if end > copyAt || c.addX+pillWidth("+ Plugin") > copyAt+1 {
		t.Fatalf("the cards end at %.0f and the add button at %.0f, past the copy button at %.0f", end, c.addX+pillWidth("+ Plugin"), copyAt)
	}
}

func TestThePluginPickerFindsAPluginByItsName(t *testing.T) {
	a := chained()
	a.Plugins = []PluginChoice{{Name: "Ozone 11 Equalizer", Vendor: "iZotope", Kind: "Fx|EQ"},
		{Name: "Ozone 11 Maximizer", Vendor: "iZotope", Kind: "Fx|Dynamics"},
		{Name: "Pro-Q 3", Vendor: "FabFilter", Kind: "Fx|EQ"}, {Name: "Pro-L 2", Vendor: "FabFilter", Kind: "Fx|Dynamics"}}
	w, r, run := stage(t, a)
	b := boundsOf(t, w, run, r.chain)
	at := b.Min.Add(geom.Pt(r.chain.addX+20, b.Size().H/2))
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	run(10)
	if !r.chain.picker.IsOpen() {
		t.Fatal("the add button opened no picker")
	}
	w.Input(input.TextInput{Text: "pro-l"})
	run(10)
	w.Input(input.KeyPress{Key: input.KeyEnter})
	run(10)
	_, sent := edits(w)
	if len(sent) != 1 || sent[0] != (AddPlugin{Track: 1, Choice: a.Plugins[3]}) {
		t.Fatalf("picking sent %v, want Pro-L 2 added", sent)
	}
}

// playing stages an album of one real track, ten seconds long, playing
// from its start on the stage's deck; mix advances the sound by d.
func playing(t *testing.T, follow Follow) (r *root, run func(int), mix func(time.Duration)) {
	t.Helper()
	path := writeTrack(t, 0, 10*time.Second, 0, 0.3)
	sc, err := scanTrack(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	a := album()
	tr := &a.Tracks[0]
	tr.File, tr.Wave, tr.Frames, tr.Format = path, sc.Wave, sc.Frames, sc.Format
	a.Playing, a.Follow = true, follow
	_, r, run = stage(t, a)
	if err := r.d.play(tr.ID, path, a.Gap, tr.Edit, &rack{active: true}, 0, false, 0); err != nil {
		t.Fatal(err)
	}
	// The mixer runs in blocks of 10 ms, as many as the time asks, the
	// rest carried on: one block a frame, then two.
	buf := make([]float32, 2*audio.SampleRate/100)
	var owed time.Duration
	mix = func(d time.Duration) {
		for owed += d; owed >= 10*time.Millisecond; owed -= 10 * time.Millisecond {
			r.d.mix.Mix(buf)
		}
	}
	return r, run, mix
}

func TestScrollingKeepsThePlayheadStillAsTheSoundSlidesUnderIt(t *testing.T) {
	r, run, mix := playing(t, FollowScroll)
	ed := r.editor
	// Zoomed in to two seconds, where a jitter of the playhead shows.
	ed.v0.Jump(0)
	ed.v1.Jump(2)
	lo, _ := ed.fit()
	var lastX float32
	var lastT float64
	for f := range 300 {
		// The speakers tell the time in steps of 10 ms, against frames
		// of 16.7: the playhead must run smoothly all the same.
		mix(time.Second / 60)
		run(1)
		ph, ok := ed.playhead()
		if !ok {
			t.Fatalf("frame %d: no playhead", f)
		}
		x := ed.xOf(ph)
		switch {
		case f < 30:
			// The view glides from where it was to the sound's start.
		case ph < float64(ed.v1.Value()-ed.v0.Value())/4+lo:
			// Near the start, the view stays at the sound's start, and the
			// playhead runs to the quarter mark.
			if ed.v0.Value() != float32(lo) {
				t.Fatalf("frame %d: at %.2f s the view starts at %.3f, before the sound's start %.3f", f, ph, ed.v0.Value(), lo)
			}
		case f > 60:
			if math.Abs(float64(x-ed.size.W/4)) > 1 || math.Abs(float64(x-lastX)) > 0.5 {
				t.Fatalf("frame %d: the playhead is at %.2f, the frame before at %.2f, want still a quarter in", f, x, lastX)
			}
			if d := ph - lastT; math.Abs(d-1.0/60) > 0.002 {
				t.Fatalf("frame %d: the playhead moved %.4f s, want a frame's time, %.4f", f, d, 1.0/60)
			}
		}
		lastX, lastT = x, ph
	}
}

func TestJumpingTurnsTheViewOnAPageAndOffLeavesIt(t *testing.T) {
	for _, follow := range []Follow{FollowJump, FollowOff} {
		r, run, mix := playing(t, follow)
		ed := r.editor
		// Zoomed in on the first two seconds.
		ed.v0.Jump(0)
		ed.v1.Jump(2)
		for range 300 {
			mix(time.Second / 60)
			run(1)
		}
		ph, _ := ed.playhead()
		in := float64(ed.v0.Value()) <= ph && ph <= float64(ed.v1.Value())
		if follow == FollowJump && !in {
			t.Errorf("jumping, the playhead at %.2f s is out of the view, %.2f to %.2f", ph, ed.v0.Value(), ed.v1.Value())
		}
		if follow == FollowOff && (ed.v0.Value() != 0 || ed.v1.Value() != 2) {
			t.Errorf("off, the view moved to %.2f to %.2f", ed.v0.Value(), ed.v1.Value())
		}
	}
}

func TestZoomingInFarDrawsFromTheSamplesThemselves(t *testing.T) {
	r, run, _ := playing(t, FollowOff)
	ed := r.editor
	ed.v0.Jump(5)
	ed.v1.Jump(5.01)
	for range 120 {
		run(1)
		if len(ed.raw.Data) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if ed.raw.file != ed.track.File || ed.raw.From > 5*rate || ed.raw.From+int64(len(ed.raw.Data)/2) < 5*rate+441 {
		t.Fatalf("zoomed in on 5 s, the samples read are from %d, %d frames", ed.raw.From, len(ed.raw.Data)/2)
	}
}

func TestTheZoomSliderDrawsTheWaveformLouder(t *testing.T) {
	w, r, run := stage(t, album())
	ed := r.editor
	b := boundsOf(t, w, run, ed)
	top, laneH := ed.lanes()
	x := b.Max.X - zoomW/2
	for i, y := range []float32{top + 2*laneH - 8, top + laneH, top + 8} {
		at := geom.Pt(x, b.Min.Y+y)
		w.Input(input.PointerMove{Pos: at})
		w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
		w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
		run(40)
		want := []float32{0, maxZoom / 2, maxZoom}[i]
		if got := ed.zoom.Value(); math.Abs(float64(got-want)) > 1.5 {
			t.Fatalf("pressed at %.0f up the slider, the zoom is %.1f dB, want %.0f", y, got, want)
		}
	}
	at := geom.Pt(x, b.Min.Y+top+laneH)
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 2})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	run(60)
	if z := ed.zoom.Value(); z > 0.1 {
		t.Fatalf("a double-click left the zoom at %.1f dB", z)
	}
}

func TestADoubleClickOnTheTitleRenamesTheTrack(t *testing.T) {
	for _, how := range []string{"enter", "away", "escape"} {
		w, r, run := stage(t, album())
		b := boundsOf(t, w, run, r.head)
		at := b.Min.Add(geom.Pt(10, b.Size().H/2))
		w.Input(input.PointerMove{Pos: at})
		w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 2})
		w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
		run(5)
		if !r.head.renaming || r.head.field.Text() != "One" {
			t.Fatalf("%s: a double-click on the title opened no field", how)
		}
		w.Input(input.TextInput{Text: "Uno"})
		switch how {
		case "enter":
			w.Input(input.KeyPress{Key: input.KeyEnter})
		case "away":
			// A click on the editor takes the keyboard.
			eb := boundsOf(t, w, run, r.editor)
			w.Input(input.PointerDown{Pos: eb.Center(), Button: input.ButtonPrimary, Clicks: 1})
			w.Input(input.PointerUp{Pos: eb.Center(), Button: input.ButtonPrimary})
		case "escape":
			w.Input(input.KeyPress{Key: input.KeyEscape})
		}
		run(5)
		var renamed []gunim.Intent
		_, rest := edits(w)
		for _, in := range rest {
			if _, ok := in.(RenameTrack); ok {
				renamed = append(renamed, in)
			}
		}
		want := []gunim.Intent{RenameTrack{ID: 1, Title: "Uno"}}
		if how == "escape" {
			want = nil
		}
		if len(renamed) != len(want) || (len(want) > 0 && renamed[0] != want[0]) || r.head.renaming {
			t.Fatalf("%s: sent %v, want %v, and the field closed", how, renamed, want)
		}
	}
}

func TestDraggingTheRulerScrollsTheViewWithThePointer(t *testing.T) {
	w, r, run := stage(t, album())
	ed := r.editor
	ed.v0.Jump(2)
	ed.v1.Jump(4)
	b := boundsOf(t, w, run, ed)
	from := b.Min.Add(geom.Pt(ed.size.W/2, rulerH/2))
	grabbed := ed.tAt(ed.size.W / 2)
	w.Input(input.PointerMove{Pos: from})
	w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1})
	run(1)
	for i := 1; i <= 20; i++ {
		at := from.Add(geom.Pt(float32(-10*i), 0))
		w.Input(input.PointerMove{Pos: at})
		run(1)
		if got := ed.tAt(at.X - b.Min.X); math.Abs(got-grabbed) > 0.005 {
			t.Fatalf("move %d: under the pointer is %.3f s, want %.3f, the time grabbed", i, got, grabbed)
		}
	}
	w.Input(input.PointerUp{Pos: from.Add(geom.Pt(-200, 0)), Button: input.ButtonPrimary})
	run(1)
	if _, rest := edits(w); len(rest) != 0 {
		t.Fatalf("a drag of the ruler sent %v", rest)
	}
	// A click on it seeks there.
	w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: from, Button: input.ButtonPrimary})
	run(1)
	_, rest := edits(w)
	if len(rest) != 1 {
		t.Fatalf("a click on the ruler sent %v, want a seek", rest)
	}
	if s, ok := rest[0].(SeekTo); !ok || math.Abs(s.At.Seconds()-(ed.tAt(ed.size.W/2)+1)) > 0.01 {
		t.Fatalf("a click on the ruler sent %v, want a seek to %.2f s as rendered", rest[0], ed.tAt(ed.size.W/2)+1)
	}
}

func TestTheRestartButtonPlaysFromTheStart(t *testing.T) {
	w, r, run := stage(t, album())
	b := boundsOf(t, w, run, r.trans.restart)
	w.Input(input.PointerMove{Pos: b.Center()})
	w.Input(input.PointerDown{Pos: b.Center(), Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: b.Center(), Button: input.ButtonPrimary})
	run(1)
	if _, rest := edits(w); len(rest) != 1 || rest[0] != (PlayFromStart{}) {
		t.Fatalf("the restart button sent %v", rest)
	}
}

func TestCalcLUFSCountsTheTracksChangedAndMeasuresThem(t *testing.T) {
	a := album()
	two := a.Tracks[0]
	two.ID = 2
	a.Tracks = append(a.Tracks, two)
	a.Tracks[0].Stale = true
	w, r, run := stage(t, a)
	if got := r.header.calc.words; got != "Calc LUFS · 1" {
		t.Fatalf("the button reads %q, want one track to measure", got)
	}
	b := boundsOf(t, w, run, r.header.calc)
	w.Input(input.PointerMove{Pos: b.Center()})
	w.Input(input.PointerDown{Pos: b.Center(), Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: b.Center(), Button: input.ButtonPrimary})
	run(1)
	if _, rest := edits(w); len(rest) != 1 || rest[0] != (CalcLoudness{}) {
		t.Fatalf("the button sent %v", rest)
	}
}

func TestTheAlbumsNameOpensTheMenuOfAlbums(t *testing.T) {
	a := album()
	a.AlbumName = "Night"
	w, r, run := stage(t, a)
	b := boundsOf(t, w, run, r.header)
	at := b.Min.Add(geom.Pt(80, 24))
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	run(10)
	if !r.headerMenu.Focusable() {
		t.Fatal("a press on the album's name opened no menu")
	}
	w.Input(input.KeyPress{Key: input.KeyDown})
	w.Input(input.KeyPress{Key: input.KeyEnter})
	run(5)
	if _, rest := edits(w); len(rest) != 1 || rest[0] != (EditRelease{}) {
		t.Fatalf("the menu's first item sent %v", rest)
	}
}

func TestTheSpectrogramTakesColumnsWhileTheSoundPlays(t *testing.T) {
	r, run, mix := playing(t, FollowOff)
	m := r.meters
	columns := m.gram.Columns
	for range 120 {
		mix(time.Second / 60)
		run(1)
	}
	// Two seconds, at 40 columns a second, a frame's worth or so either
	// way.
	if n := columns(); n < 75 || n > 85 {
		t.Fatalf("two seconds played took %d columns, want about 80", n)
	}
	r.state.Playing = false
	was := columns()
	run(30)
	if columns() != was {
		t.Fatal("paused, the spectrogram goes on")
	}
}

func TestTheSpectrumsHeadingSwitchesToTheSpectrogram(t *testing.T) {
	w, r, run := stage(t, album())
	b := boundsOf(t, w, run, r.meters)
	at := b.Min.Add(r.meters.specHead.Center())
	for _, want := range []bool{true, false} {
		w.Input(input.PointerMove{Pos: at})
		w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
		w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
		run(1)
		if r.meters.showGram != want {
			t.Fatalf("a click on the heading left the spectrogram shown %v, want %v", r.meters.showGram, want)
		}
	}
}

func TestTheExportButtonOpensTheDialog(t *testing.T) {
	w, r, run := stage(t, album())
	b := boundsOf(t, w, run, r.header.export)
	w.Input(input.PointerMove{Pos: b.Center()})
	w.Input(input.PointerDown{Pos: b.Center(), Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: b.Center(), Button: input.ButtonPrimary})
	run(1)
	if _, rest := edits(w); len(rest) != 1 || !reflect.DeepEqual(rest[0], OpenExport{}) {
		t.Fatalf("the export button sent %v", rest)
	}
}

func TestTheLegendSwitchesTheLoudnessCurves(t *testing.T) {
	a := album()
	a.Curves = CurveS | CurveI
	w, r, run := stage(t, a)
	b := boundsOf(t, w, run, r.editor)
	for i, c := range audioui.CurveNames {
		at := b.Min.Add(r.editor.legendRects()[i].Center())
		w.Input(input.PointerMove{Pos: at})
		w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
		w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
		run(1)
		_, rest := edits(w)
		if len(rest) != 1 || rest[0] != (SetCurves{Curves: a.Curves ^ c.Bit}) {
			t.Fatalf("a click on %s sent %v", c.Name, rest)
		}
	}
}

func TestTheViewSwitchesToTheSpectrogramAndANoteIsKeptAsTyped(t *testing.T) {
	w, r, run := stage(t, album())
	b := boundsOf(t, w, run, r.head.view)
	w.Input(input.PointerMove{Pos: b.Center()})
	w.Input(input.PointerDown{Pos: b.Center(), Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: b.Center(), Button: input.ButtonPrimary})
	run(1)
	if _, rest := edits(w); len(rest) != 1 || rest[0] != (SetView{View: ViewGram}) {
		t.Fatalf("the view's switch sent %v", rest)
	}
	nb := boundsOf(t, w, run, r.head.note)
	w.Input(input.PointerMove{Pos: nb.Center()})
	w.Input(input.PointerDown{Pos: nb.Center(), Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: nb.Center(), Button: input.ButtonPrimary})
	w.Input(input.TextInput{Text: "Needs a new vocal"})
	run(2)
	_, rest := edits(w)
	if len(rest) == 0 || rest[len(rest)-1] != (SetNote{ID: 1, Note: "Needs a new vocal"}) {
		t.Fatalf("typing a note sent %v", rest)
	}
	// The application's answer, older than what is typed, leaves the
	// field as it is.
	gunim.RegisterPatch(w, "album", func(r *root, _ struct{}, u *gunim.UI) { r.head.show(r.state.Tracks[0], r.state, u) })
	if err := w.Client().Patch("album", struct{}{}); err != nil {
		t.Fatal(err)
	}
	run(1)
	if got := r.head.note.Text(); got != "Needs a new vocal" {
		t.Fatalf("the note reads %q as it is typed", got)
	}
}

func TestMatchingATrackNotMeasuredAsksForItsLoudness(t *testing.T) {
	a := album()
	a.Match = true
	w, r, run := stage(t, a)
	m := r.trans.match
	// In full words, or fewer in a narrow window.
	if !m.warn || (m.words != "Calc LUFS first" && m.words != "Calc first") {
		t.Fatalf("matching a track not measured, the button reads %q, warning %v", m.words, m.warn)
	}
	b := boundsOf(t, w, run, m)
	w.Input(input.PointerMove{Pos: b.Center()})
	w.Input(input.PointerDown{Pos: b.Center(), Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: b.Center(), Button: input.ButtonPrimary})
	run(1)
	if _, rest := edits(w); len(rest) != 1 || !reflect.DeepEqual(rest[0], CalcLoudness{}) {
		t.Fatalf("the button sent %v, want the track measured", rest)
	}
}

func TestDraggingTheSilencesStartGivesTheTrackItsOwn(t *testing.T) {
	a := album()
	a.Tracks[0].Edit = Edit{Start: 3 * time.Second}
	w, r, run := stage(t, a)
	ed := r.editor
	b := boundsOf(t, w, run, ed)
	from := b.Min.Add(geom.Pt(ed.xOf(2), b.Size().H/2))
	w.Input(input.PointerMove{Pos: from})
	w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1})
	for i := 1; i <= 10; i++ {
		to := b.Min.Add(geom.Pt(ed.xOf(2-0.1*float64(i)), b.Size().H/2))
		w.Input(input.PointerMove{Pos: to})
		run(1)
		if got := ed.gap(); math.Abs(got-(1+0.1*float64(i))) > 0.01 {
			t.Fatalf("step %d: the silence is %.3f s, want %.3f, as dragged", i, got, 1+0.1*float64(i))
		}
	}
	w.Input(input.PointerUp{Pos: from, Button: input.ButtonPrimary})
	run(1)
	_, rest := edits(w)
	last, ok := rest[len(rest)-1].(SetSilence)
	if !ok || last.Silence == nil || math.Abs(last.Silence.Seconds()-2) > 0.01 {
		t.Fatalf("the drag sent %v, want the track's silence at 2 s", rest[len(rest)-1])
	}
}

func TestTheOutputsFaderSetsTheGainAfterTheChain(t *testing.T) {
	w, r, run := stage(t, album())
	b := boundsOf(t, w, run, r.meters.outFader)
	from := b.Center()
	w.Input(input.PointerMove{Pos: from})
	w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1})
	// Up a quarter of its travel: half of 24 dB.
	up := (b.Size().H - 24) / 4
	w.Input(input.PointerMove{Pos: from.Add(geom.Pt(0, -up))})
	w.Input(input.PointerUp{Pos: from.Add(geom.Pt(0, -up)), Button: input.ButtonPrimary})
	run(1)
	sent, _ := edits(w)
	if len(sent) == 0 {
		t.Fatal("the fader sent no edit")
	}
	last := sent[len(sent)-1].Edit
	if math.Abs(float64(last.Out-12)) > 0.2 || last.Gain != 0 {
		t.Fatalf("the output's fader set the gain in %.1f and out %.1f, want out +12", last.Gain, last.Out)
	}
}

func TestTheViewsFadeIntoEachOther(t *testing.T) {
	w, r, run := stage(t, album())
	ed := r.editor
	if ed.gram.Value() != 0 {
		t.Fatal("the waveform view starts faded")
	}
	a := album()
	a.View = ViewGram
	if err := w.Client().Update("album", a); err != nil {
		t.Fatal(err)
	}
	last := float32(0)
	for f := range 40 {
		run(1)
		g := ed.gram.Value()
		if g < last-1e-6 {
			t.Fatalf("frame %d: the spectrogram faded back, %.3f to %.3f", f, last, g)
		}
		last = g
	}
	if last < 0.99 {
		t.Fatalf("after 40 frames the spectrogram is %.2f in", last)
	}
}

func TestAFlickOfTheRulerGlidesTheViewOnAndSlows(t *testing.T) {
	w, r, run := stage(t, album())
	ed := r.editor
	ed.v0.Jump(4)
	ed.v1.Jump(6)
	b := boundsOf(t, w, run, ed)
	at := b.Min.Add(geom.Pt(ed.size.W/2, rulerH/2))
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	run(1)
	for range 6 {
		at = at.Add(geom.Pt(-30, 0))
		w.Input(input.PointerMove{Pos: at})
		run(1)
	}
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	let := ed.v0.Value()
	var lastStep float32 = 1e9
	moved := false
	for f := range 120 {
		was := ed.v0.Value()
		run(1)
		step := ed.v0.Value() - was
		if step < 0 {
			t.Fatalf("frame %d: the view glides back", f)
		}
		if step > lastStep+1e-5 {
			t.Fatalf("frame %d: the glide sped up, %.4f to %.4f", f, lastStep, step)
		}
		moved = moved || step > 0
		lastStep = step
	}
	if !moved || ed.v0.Value()-let < 0.1 || ed.fling.Gliding() {
		t.Fatalf("let go moving, the view glided %.3f s and is still flinging %v", ed.v0.Value()-let, ed.fling.Gliding())
	}
}

func TestANoteIsWrittenAtATimeAndTakenAwayFromItsCard(t *testing.T) {
	w, r, run := stage(t, album())
	ed := r.editor
	ed.v0.Jump(2)
	ed.v1.Jump(6)
	b := boundsOf(t, w, run, ed)
	at := b.Min.Add(ed.noteButton().Center())
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	run(2)
	if !ed.writing {
		t.Fatal("+ Note opened no field")
	}
	w.Input(input.TextInput{Text: "Snare too loud"})
	w.Input(input.KeyPress{Key: input.KeyEnter})
	run(2)
	_, rest := edits(w)
	want := AddMark{Track: 1, At: 4 * time.Second, Text: "Snare too loud"}
	if len(rest) != 1 || rest[0] != want {
		t.Fatalf("writing a note sent %v, want %v", rest, want)
	}
	// The application's answer: the note, at 4 s.
	a := album()
	a.Tracks[0].Marks = []Mark{{ID: 5, At: 4 * time.Second, Text: "Snare too loud"}}
	if err := w.Client().Update("album", a); err != nil {
		t.Fatal(err)
	}
	run(2)
	pin := b.Min.Add(ed.markAt(a.Tracks[0].Marks[0]).Center())
	w.Input(input.PointerMove{Pos: pin})
	run(1)
	if ed.hotMark != 0 {
		t.Fatal("the pointer over the note's pin shows no card")
	}
	_, remove := ed.cardOf(0)
	at = b.Min.Add(remove.Center())
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	run(1)
	if _, rest := edits(w); len(rest) != 1 || rest[0] != (RemoveMark{Track: 1, ID: 5}) {
		t.Fatalf("the card's button sent %v", rest)
	}
}

func TestTheLoopChipMakesALoopAndTheEdgesDragIt(t *testing.T) {
	w, r, run := stage(t, album())
	ed := r.editor
	ed.v0.Jump(2)
	ed.v1.Jump(6)
	b := boundsOf(t, w, run, ed)
	at := b.Min.Add(ed.loopButton().Center())
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	run(1)
	_, rest := edits(w)
	want := []gunim.Intent{SetLoop{Track: 1, Loop: &Loop{In: 2 * time.Second, Out: 10 * time.Second}}, SetLooping{On: true}}
	if !reflect.DeepEqual(rest, want) {
		t.Fatalf("the loop's chip sent %v, want %v", rest, want)
	}
	// The application's answer: the loop, looping.
	a := album()
	a.Tracks[0].Loop = &Loop{In: 3 * time.Second, Out: 5 * time.Second}
	a.Looping = true
	if err := w.Client().Update("album", a); err != nil {
		t.Fatal(err)
	}
	run(2)
	from := b.Min.Add(geom.Pt(ed.xOf(5), rulerH/2))
	w.Input(input.PointerMove{Pos: from})
	w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1})
	for i := 1; i <= 5; i++ {
		w.Input(input.PointerMove{Pos: b.Min.Add(geom.Pt(ed.xOf(5+0.1*float64(i)), rulerH/2))})
		run(1)
		if l := ed.loopShown(); l == nil || math.Abs(l.Out.Seconds()-(5+0.1*float64(i))) > 0.01 {
			t.Fatalf("step %d: the loop's out is at %v, want under the pointer", i, l)
		}
	}
	w.Input(input.PointerUp{Pos: from, Button: input.ButtonPrimary})
	run(1)
	if v0 := ed.v0.Value(); v0 != 2 {
		t.Fatalf("dragging the loop's edge scrolled the view to %.2f", v0)
	}
	// I sets the in where the view's middle is, as nothing plays.
	_, _ = edits(w)
	w.Input(input.KeyPress{Key: input.KeyI})
	run(1)
	_, rest = edits(w)
	if len(rest) != 1 || !reflect.DeepEqual(rest[0], SetLoop{Track: 1, Loop: &Loop{In: 4 * time.Second, Out: 5 * time.Second}}) {
		t.Fatalf("I sent %v", rest)
	}
}

func TestZoomingOutStopsAtTheWholeTrack(t *testing.T) {
	w, r, run := stage(t, album())
	ed := r.editor
	b := boundsOf(t, w, run, ed)
	f0, f1 := ed.fit()
	for range 30 {
		w.Input(input.Scroll{Pos: b.Center(), Notches: geom.Pt(0, -1)})
		run(2)
	}
	run(30)
	if math.Abs(float64(ed.v0.Value())-f0) > 0.01 || math.Abs(float64(ed.v1.Value())-f1) > 0.01 {
		t.Fatalf("zoomed out all the way, the view is %.2f to %.2f, want the whole track, %.2f to %.2f",
			ed.v0.Value(), ed.v1.Value(), f0, f1)
	}
}

func TestADoubleClickOnANoteWritesItWithoutSeeking(t *testing.T) {
	a := album()
	a.Tracks[0].Marks = []Mark{{ID: 5, At: 4 * time.Second, Text: "Snare"}}
	w, r, run := stage(t, a)
	ed := r.editor
	ed.v0.Jump(2)
	ed.v1.Jump(6)
	b := boundsOf(t, w, run, ed)
	pin := b.Min.Add(ed.markAt(a.Tracks[0].Marks[0]).Center())
	w.Input(input.PointerMove{Pos: pin})
	run(1)
	w.Input(input.PointerDown{Pos: pin, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: pin, Button: input.ButtonPrimary})
	w.Input(input.PointerDown{Pos: pin, Button: input.ButtonPrimary, Clicks: 2})
	w.Input(input.PointerUp{Pos: pin, Button: input.ButtonPrimary})
	// Past the time a click waits for a second, on the window's clock.
	run(40)
	_, rest := edits(w)
	for _, in := range rest {
		if _, ok := in.(SeekTo); ok {
			t.Fatalf("a double-click on a note sought: %v", rest)
		}
	}
	if !ed.writing || ed.writeID != 5 {
		t.Fatal("a double-click on a note opened no field to write it anew")
	}
}

func TestF1ShowsTheHelp(t *testing.T) {
	w, _, run := stage(t, album())
	w.Input(input.KeyPress{Key: input.KeyF1})
	run(1)
	if _, rest := edits(w); len(rest) != 1 || !reflect.DeepEqual(rest[0], ShowHelp{}) {
		t.Fatalf("F1 sent %v", rest)
	}
}

func TestAClickOnANoteSeeksToItOnceNoSecondFollows(t *testing.T) {
	a := album()
	a.Tracks[0].Marks = []Mark{{ID: 5, At: 4 * time.Second, Text: "Snare"}}
	w, r, run := stage(t, a)
	ed := r.editor
	ed.v0.Jump(2)
	ed.v1.Jump(6)
	b := boundsOf(t, w, run, ed)
	pin := b.Min.Add(ed.markAt(a.Tracks[0].Marks[0]).Center())
	w.Input(input.PointerMove{Pos: pin})
	run(1)
	w.Input(input.PointerDown{Pos: pin, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: pin, Button: input.ButtonPrimary})
	run(1)
	if _, rest := edits(w); len(rest) != 0 {
		t.Fatalf("a click sought at once, before a second could follow: %v", rest)
	}
	// The window's own clock, which its timers keep, past the wait.
	run(30)
	_, rest := edits(w)
	if len(rest) != 1 || rest[0] != (SeekTo{At: 5 * time.Second}) {
		t.Fatalf("a click on the note at 4 s sent %v, want a seek to it as rendered, 5 s", rest)
	}
}

func TestTheSpectrumBeforeTheChainMatchesTheOneAfterWithNoPlugins(t *testing.T) {
	r, run, mix := playing(t, FollowOff)
	for range 90 {
		mix(time.Second / 60)
		run(1)
	}
	m := r.meters
	k := 0
	for i, f := range m.spectrum.Freqs {
		if math.Abs(float64(f)-1000) < math.Abs(float64(m.spectrum.Freqs[k])-1000) {
			k = i
		}
	}
	// The output as drawn: the listening level taken back out.
	out := m.spec[k]
	if math.Abs(float64(m.specIn[k]+10.5)) > 1 || math.Abs(float64(m.specIn[k]-out)) > 1 {
		t.Fatalf("a 1 kHz tone at -10.5 dBFS reads %.1f dB into the chain and %.1f out of it", m.specIn[k], out)
	}
}
