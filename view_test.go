package main

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// album returns an album of one track, ten seconds of a file read, with
// its sound from the second second.
func album() Album {
	w := &Wave{Frames: 10 * rate, Rate: rate}
	for ch := range 2 {
		w.Peak[ch] = make([]float32, waveBuckets)
		w.RMS[ch] = make([]float32, waveBuckets)
		for b := waveBuckets / 5; b < waveBuckets; b++ {
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
		if tr.Exported == "" || filepath.Base(tr.Exported) != exportName(i+1, tr.Title) {
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
	buf := make([]float32, 2*audio.SampleRate/100)
	mix = func(d time.Duration) {
		for range int(d / (10 * time.Millisecond)) {
			r.d.mix.Mix(buf)
		}
	}
	return r, run, mix
}

func TestScrollingKeepsThePlayheadStillAsTheSoundSlidesUnderIt(t *testing.T) {
	r, run, mix := playing(t, FollowScroll)
	ed := r.editor
	for f := range 240 {
		mix(time.Second / 60)
		run(1)
		ph, ok := ed.playhead()
		if !ok {
			t.Fatalf("frame %d: no playhead", f)
		}
		// Once the view has caught up, a quarter of the way in.
		if x := ed.xOf(ph); f > 40 && math.Abs(float64(x-ed.size.W/4)) > 2 {
			t.Fatalf("frame %d: the playhead is at %.1f, want %.1f, a quarter in", f, x, ed.size.W/4)
		}
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
		if len(ed.raw.data) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if ed.raw.file != ed.track.File || ed.raw.from > 5*rate || ed.raw.from+int64(len(ed.raw.data)/2) < 5*rate+441 {
		t.Fatalf("zoomed in on 5 s, the samples read are from %d, %d frames", ed.raw.from, len(ed.raw.data)/2)
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
	if _, rest := edits(w); len(rest) != 1 || rest[0] != (NewAlbum{}) {
		t.Fatalf("the menu's first item sent %v", rest)
	}
}

func TestTheSpectrogramTakesColumnsWhileTheSoundPlays(t *testing.T) {
	r, run, mix := playing(t, FollowOff)
	m := r.meters
	columns := func() int { return len(m.gram.tiles)*gramTile + m.gram.n }
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

func TestTheSpectrogramKeepsTheTilesItShows(t *testing.T) {
	var g spectrogram
	g.keep = 3
	col := make([]float32, specPoints)
	for range 10 * gramTile {
		g.push(col)
	}
	if len(g.tiles) != 3 || g.n != 0 {
		t.Fatalf("after 10 tiles' columns, %d tiles are kept, %d columns filling", len(g.tiles), g.n)
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
