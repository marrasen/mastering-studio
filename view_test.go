package main

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/widget"
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
		a.add(p, "")
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
		a.add(p, "")
	}
	settle(t, a, func() bool { return a.Tracks[2].Measured && a.Tracks[0].Measured })
	a.handle(SetMatch{On: true})
	want := math.Pow(10, float64(a.Target-a.Tracks[0].Measure.LUFS)/20)
	if math.Abs(float64(a.d.matchOf(a.Current))-want) > 1e-3 {
		t.Fatalf("matched by %.3f, want %.3f: the target over the track's loudness", a.d.matchOf(a.Current), want)
	}
}

func TestChangingTheExportFolderOpensWhereItIs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newApp(ctx, newDeck(audio.NewMixer()), "")
	paths := writeAlbum(t)
	for _, p := range paths {
		a.add(p, "")
	}
	asked := make(chan string, 1)
	a.choose = func(o driver.ChooseOptions) ([]string, error) {
		asked <- o.Folder
		return nil, nil
	}
	// No Export folder yet: the dialog opens beside the tracks.
	a.handle(ChooseExportDir{})
	if got, want := <-asked, filepath.Dir(paths[0]); got != want {
		t.Fatalf("with no Export folder yet the dialog opened in %q, want %q", got, want)
	}
	a.ExportDir = t.TempDir()
	a.handle(ChooseExportDir{})
	if got := <-asked; got != a.ExportDir {
		t.Fatalf("the dialog opened in %q, want the folder chosen, %q", got, a.ExportDir)
	}
}

func TestAnAlbumExportsEachTrackAtItsOwnLength(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newApp(ctx, newDeck(audio.NewMixer()), "")
	paths := writeAlbum(t)
	for _, p := range paths {
		a.add(p, "")
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

func TestADraggedCardMovesItsPluginTheOthersMakingWay(t *testing.T) {
	// Three, short enough to stand in one row.
	a := chained()
	a.Tracks[0].Chain[0].Name = "Ozone 11 Equalizer"
	a.Tracks[0].Chain = append(a.Tracks[0].Chain, Slot{ID: 9, Name: "Trim", Vendor: "Marras"})
	w, r, run := stage(t, a)
	run(40)
	b := boundsOf(t, w, run, r.chain)
	c := r.chain
	first, second, last := c.cards[7], c.cards[8], c.cards[9]
	secondAt := second.x.Value()
	from := b.Min.Add(c.cardRect(first).Center().Add(geom.Pt(20, 0)))
	w.Input(input.PointerMove{Pos: from})
	w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1})
	// Along, a step a frame, to past the middle of the last card.
	to := b.Min.X + c.cardRect(last).Center().X + 30
	for x := from.X; x < to; x += 20 {
		w.Input(input.PointerMove{Pos: geom.Pt(x, from.Y)})
		run(1)
	}
	w.Input(input.PointerMove{Pos: geom.Pt(to, from.Y)})
	run(20)
	if c.moving != 7 || c.moveTo != 2 {
		t.Fatalf("dragging, card %d is moving to %d; want the first to the end", c.moving, c.moveTo)
	}
	if second.x.Value() >= secondAt {
		t.Errorf("the second card stays at %.0f, from %.0f: it makes no way", second.x.Value(), secondAt)
	}
	if got := b.Min.X + first.x.Value() + c.grab; got != to {
		t.Errorf("the card dragged is at %.0f, want under the pointer at %.0f", got, to)
	}
	w.Input(input.PointerUp{Pos: geom.Pt(to, from.Y), Button: input.ButtonPrimary})
	run(1)
	_, sent := edits(w)
	if len(sent) != 1 || sent[0] != (MovePlugin{Track: 1, From: 0, To: 2}) {
		t.Fatalf("the drag sent %v, want the move alone", sent)
	}
	// A press that hardly moves is a click.
	at := b.Min.Add(c.cardRect(second).Center().Add(geom.Pt(20, 0)))
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerMove{Pos: at.Add(geom.Pt(4, 0))})
	w.Input(input.PointerUp{Pos: at.Add(geom.Pt(4, 0)), Button: input.ButtonPrimary})
	run(1)
	if _, sent := edits(w); len(sent) != 1 || sent[0] != (ShowEditor{Track: 1, Slot: 8}) {
		t.Fatalf("a click sent %v, want the editor shown", sent)
	}
}

func TestTheChainsCardsLeaveRoomForItsButtons(t *testing.T) {
	// A plugin more: one row, its cards clear of the buttons.
	a := chained()
	a.Tracks[0].Chain[0].Name = "Ozone 11 Equalizer"
	a.Tracks[0].Chain = append(a.Tracks[0].Chain, Slot{ID: 20, Name: "Trim", Vendor: "Somebody"})
	_, r, run := stage(t, a)
	run(40)
	c := r.chain
	// The A/B switch stands before the menu.
	menuAt := c.size.W - menuPillWidth(c.more.words) - 8 - abSwitchW
	last := c.cards[c.order[len(c.order)-1]]
	if c.rows != 1 || c.cardRect(last).Max.X > menuAt || c.addX+pillWidth("+ Plugin") > menuAt+1 {
		t.Fatalf("in %d rows, the cards end at %.0f and the add button at %.0f, past the menu at %.0f", c.rows,
			c.cardRect(last).Max.X, c.addX+pillWidth("+ Plugin"), menuAt)
	}
}

func TestAChainTooLongForItsRowTakesTwo(t *testing.T) {
	a := chained()
	for i := range 6 {
		a.Tracks[0].Chain = append(a.Tracks[0].Chain, Slot{ID: 20 + i, Name: "Another Plugin Of Some Length", Vendor: "Somebody"})
	}
	_, r, run := stage(t, a)
	run(60)
	c := r.chain
	if c.rows != 2 || math.Abs(float64(c.height()-(chainH+rowStep))) > 0.5 || math.Abs(float64(c.size.H-c.height())) > 0.5 {
		t.Fatalf("a long chain takes %d rows, %v tall, laid out %v", c.rows, c.height(), c.size.H)
	}
	// The first row clear of the menu, the second of the add button,
	// which ends inside the row; the second row's cards under the first's.
	// The A/B switch stands before the menu.
	menuAt := c.size.W - menuPillWidth(c.more.words) - 8 - abSwitchW
	rows := c.splitRows(c.order)
	for _, id := range rows[0] {
		if r := c.cardRect(c.cards[id]); r.Max.X > menuAt || math.Abs(float64(r.Center().Y-rowMid(0))) > 0.5 {
			t.Errorf("a card of the first row is at %v, the menu at %.0f", r, menuAt)
		}
	}
	for _, id := range rows[1] {
		if r := c.cardRect(c.cards[id]); r.Max.X > c.addX || math.Abs(float64(r.Center().Y-rowMid(1))) > 0.5 {
			t.Errorf("a card of the second row is at %v, the add button at %.0f", r, c.addX)
		}
	}
	if c.addX+pillWidth("+ Plugin") > c.size.W || c.addY != rowMid(1) {
		t.Errorf("the add button is at %.0f, %.0f, in a row %.0f wide", c.addX, c.addY, c.size.W)
	}
	// The sound runs along both rows, round from the first to the second.
	path := c.soundPath()
	if len(path) != 6 || path[0].Y != rowMid(0) || path[len(path)-1].Y != rowMid(1) {
		t.Errorf("the sound's line runs %v", path)
	}
	// A card dragged down onto the second row lands there.
	first := c.cards[c.order[0]]
	c.moving, c.grab = c.order[0], 0
	first.x.Jump(c.cardRect(c.cards[rows[1][0]]).Center().X + 5)
	first.y.Jump(rowMid(1))
	if to := c.landing(); to != len(rows[0]) {
		t.Errorf("dragged onto the second row past its first card, the card would land at %d, want %d", to, len(rows[0]))
	}
	c.moving = 0
}

func TestADoubleClickOnACardRenamesIt(t *testing.T) {
	w, r, run := stage(t, chained())
	run(40)
	b := boundsOf(t, w, run, r.chain)
	at := b.Min.Add(r.chain.cardRect(r.chain.cards[8]).Center().Add(geom.Pt(20, 0)))
	w.Input(input.PointerMove{Pos: at})
	for _, n := range []int{1, 2} {
		w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: n})
		w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	}
	run(2)
	if r.chain.renaming != 8 {
		t.Errorf("a double-click on the card names slot %d", r.chain.renaming)
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
	if err := r.d.play(tr.ID, path, a.Gap, tr.Edit, newRackOf(nil), 0, false, 0); err != nil {
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

func TestMeasureLoudnessCountsTheTracksChangedAndMeasuresThem(t *testing.T) {
	a := album()
	two := a.Tracks[0]
	two.ID = 2
	a.Tracks = append(a.Tracks, two)
	a.Tracks[0].Stale = true
	w, r, run := stage(t, a)
	if got := r.header.calc.words; got != "Measure loudness · 1" {
		t.Fatalf("the button reads %q, want one track to measure", got)
	}
	b := boundsOf(t, w, run, r.header.calc)
	w.Input(input.PointerMove{Pos: b.Center()})
	w.Input(input.PointerDown{Pos: b.Center(), Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: b.Center(), Button: input.ButtonPrimary})
	run(1)
	if _, rest := edits(w); len(rest) != 1 || rest[0] != (MeasureLoudness{}) {
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
	// Nothing to undo or redo, the first item that takes a press saves.
	if _, rest := edits(w); len(rest) != 1 || rest[0] != (SaveAlbum{}) {
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
	at := b.Min.Add(r.head.view.parts[partGram].Center())
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
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
	if !m.warn || (m.words != "Measure loudness first" && m.words != "Measure first") {
		t.Fatalf("matching a track not measured, the button reads %q, warning %v", m.words, m.warn)
	}
	b := boundsOf(t, w, run, m)
	w.Input(input.PointerMove{Pos: b.Center()})
	w.Input(input.PointerDown{Pos: b.Center(), Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: b.Center(), Button: input.ButtonPrimary})
	run(1)
	if _, rest := edits(w); len(rest) != 1 || !reflect.DeepEqual(rest[0], MeasureLoudness{}) {
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

func TestAClickInTheWaveOnlyClosesTheNoteBeingWritten(t *testing.T) {
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
	w.Input(input.TextInput{Text: "Snare too loud"})
	run(1)
	edits(w)
	wave := b.Min.Add(geom.Pt(ed.xOf(5), b.Size().H/2))
	w.Input(input.PointerMove{Pos: wave})
	w.Input(input.PointerDown{Pos: wave, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: wave, Button: input.ButtonPrimary})
	run(2)
	_, rest := edits(w)
	want := AddMark{Track: 1, At: 4 * time.Second, Text: "Snare too loud"}
	if ed.writing || len(rest) != 1 || rest[0] != want {
		t.Fatalf("a click in the wave while writing sent %v, want only %v", rest, want)
	}
	// The next click seeks.
	w.Input(input.PointerDown{Pos: wave, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: wave, Button: input.ButtonPrimary})
	run(2)
	if _, rest := edits(w); len(rest) != 1 {
		t.Fatalf("the click after sent %v, want a seek", rest)
	} else if _, ok := rest[0].(SeekTo); !ok {
		t.Fatalf("the click after sent %v, want a seek", rest)
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
	_, _ = edits(w)
	w.Input(input.PointerMove{Pos: from})
	w.Input(input.PointerDown{Pos: from, Button: input.ButtonPrimary, Clicks: 1})
	for i := 1; i <= 5; i++ {
		w.Input(input.PointerMove{Pos: b.Min.Add(geom.Pt(ed.xOf(5+0.1*float64(i)), rulerH/2))})
		run(1)
		if l := ed.loopShown(); l == nil || math.Abs(l.Out.Seconds()-(5+0.1*float64(i))) > 0.01 {
			t.Fatalf("step %d: the loop's out is at %v, want under the pointer", i, l)
		}
	}
	if _, got := edits(w); len(got) != 0 {
		t.Fatalf("while dragged, the loop sent %v", got)
	}
	w.Input(input.PointerUp{Pos: from, Button: input.ButtonPrimary})
	run(1)
	// Let go: sent once, and shown where it was let go until the
	// application answers.
	_, rest = edits(w)
	if len(rest) != 1 {
		t.Fatalf("let go, the loop sent %v", rest)
	}
	set, ok := rest[0].(SetLoop)
	sent := set.Loop
	if !ok || sent == nil || math.Abs(sent.Out.Seconds()-5.5) > 0.01 {
		t.Fatalf("let go, the loop sent is %v, want out at 5.5 s", sent)
	}
	if l := ed.loopShown(); l == nil || *l != *sent {
		t.Fatalf("waiting on the application, the loop shows as %v", l)
	}
	a.Tracks[0].Loop = sent
	if err := w.Client().Update("album", a); err != nil {
		t.Fatal(err)
	}
	run(1)
	if ed.dragLoop != nil {
		t.Fatal("answered, the loop dragged is still shown in place of the application's")
	}
	a.Tracks[0].Loop = &Loop{In: 3 * time.Second, Out: 5 * time.Second}
	if err := w.Client().Update("album", a); err != nil {
		t.Fatal(err)
	}
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

func TestTheNextTrackKeepsTheZoom(t *testing.T) {
	a := album()
	two := a.Tracks[0]
	two.ID, two.Title, two.File = 2, "Two", "two.wav"
	a.Tracks = append(a.Tracks, two)
	w, r, run := stage(t, a)
	ed := r.editor
	ed.v0.Jump(3)
	ed.v1.Jump(5)
	a.Current = 2
	if err := w.Client().Publish(albumTopic, a); err != nil {
		t.Fatal(err)
	}
	run(30)
	f0, _ := ed.fit()
	if ed.track.ID != 2 || math.Abs(float64(ed.v1.Value()-ed.v0.Value())-2) > 0.01 || math.Abs(float64(ed.v0.Value())-f0) > 0.01 {
		t.Fatalf("on the next track the view is %.2f to %.2f, want two seconds from its start, %.2f", ed.v0.Value(), ed.v1.Value(), f0)
	}
	// Zoomed out, the next track is shown whole.
	ed.v0.Jump(float32(f0))
	_, f1 := ed.fit()
	ed.v1.Jump(float32(f1))
	a.Current = 1
	if err := w.Client().Publish(albumTopic, a); err != nil {
		t.Fatal(err)
	}
	run(30)
	if g0, g1 := ed.fit(); math.Abs(float64(ed.v0.Value())-g0) > 0.01 || math.Abs(float64(ed.v1.Value())-g1) > 0.01 {
		t.Fatalf("zoomed out, the next track is shown %.2f to %.2f, want the whole of it", ed.v0.Value(), ed.v1.Value())
	}
}

func TestStoppedTheLevelsFallOnWithoutThePointer(t *testing.T) {
	_, r, _ := stage(t, album())
	m := r.meters
	for ch := range 2 {
		m.out.Peak[ch], m.out.Hold[ch] = -6, -6
	}
	frames := 0
	for m.Step(time.Second / 60) {
		frames++
		if frames > 60*20 {
			t.Fatal("stopped, the levels never come to rest")
		}
	}
	if frames < 60 || falling(m.out) {
		t.Fatalf("the levels stopped falling after %d frames, at a peak of %.1f dB", frames, m.out.Peak[0])
	}
}

func TestTheSpectrumsLegendSwitchesItsLines(t *testing.T) {
	w, r, run := stage(t, album())
	m := r.meters
	b := boundsOf(t, w, run, m)
	out, in := m.spectrum.LegendRects(m.specArea)
	click := func(at geom.Point) {
		at = b.Min.Add(at)
		w.Input(input.PointerMove{Pos: at})
		w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
		w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
		run(1)
	}
	click(out.Center())
	click(in.Center())
	if !m.spectrum.HideOut || m.spectrum.ShowIn {
		t.Fatalf("after a click on each, the output is hidden %v and the input shown %v", m.spectrum.HideOut, m.spectrum.ShowIn)
	}
	click(out.Center())
	if m.spectrum.HideOut || m.showGram {
		t.Fatal("a second click on OUT left the output hidden, or switched the view")
	}
}

func TestASlotIsNamedApartFromItsPlugin(t *testing.T) {
	w, r, run := stage(t, chained())
	gunim.RegisterPatch(w, "album", func(r *root, _ struct{}, u *gunim.UI) { r.chain.rename(8, u) })
	if err := w.Client().Patch("album", struct{}{}); err != nil {
		t.Fatal(err)
	}
	run(1)
	// The name typed over the plugin's, which the field starts with,
	// all of it picked.
	w.Input(input.TextInput{Text: "Maximizer, gentle"})
	w.Input(input.KeyPress{Key: input.KeyEnter})
	run(2)
	_, rest := edits(w)
	if len(rest) != 1 || rest[0] != (RenamePlugin{Track: 1, Slot: 8, Label: "Maximizer, gentle"}) {
		t.Fatalf("naming the slot sent %v", rest)
	}
	if r.chain.renaming != 0 {
		t.Fatal("named, the field stays open")
	}
	s := Slot{Name: "Ozone 11 Maximizer", Vendor: "iZotope", Label: "Maximizer, gentle", Gain: 1.24, Gained: true}
	if s.title() != "Maximizer, gentle" || cardDetail(s) != "+1.2 LU · Ozone 11 Maximizer" {
		t.Fatalf("a slot named apart reads %q, %q", s.title(), cardDetail(s))
	}
}

func TestASlotNamedAsItsPluginIsCalledAfterItAgain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newApp(ctx, newDeck(audio.NewMixer()), "")
	a.Tracks = []Track{{ID: 1, Chain: []Slot{{ID: 3, Name: "Limiter"}}}}
	a.handle(RenamePlugin{Track: 1, Slot: 3, Label: "  Last limiter "})
	if l := a.Tracks[0].Chain[0].Label; l != "Last limiter" {
		t.Fatalf("named %q", l)
	}
	a.handle(RenamePlugin{Track: 1, Slot: 3, Label: "Limiter"})
	if l := a.Tracks[0].Chain[0].Label; l != "" {
		t.Fatalf("named as its plugin, the slot keeps %q", l)
	}
}

func TestTheSpectrumAsLeftIsKeptForTheNextRun(t *testing.T) {
	// Shown as kept.
	a := album()
	a.Spectrum = SpectrumView{HideIn: true}
	w, r, run := stage(t, a)
	m := r.meters
	if m.showGram || m.spectrum.HideOut || m.spectrum.ShowIn {
		t.Fatalf("the spectrum kept with its input hidden shows as %+v", m.view())
	}
	// A click on OUT hides it, and sends the view to keep.
	b := boundsOf(t, w, run, m)
	out, _ := m.spectrum.LegendRects(m.specArea)
	at := b.Min.Add(out.Center())
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	run(1)
	want := SpectrumView{HideOut: true, HideIn: true}
	if _, rest := edits(w); len(rest) != 1 || rest[0] != (SetSpectrum{View: want}) {
		t.Fatalf("a click on OUT sent %v", rest)
	}
	// Kept in the settings, and read back.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := newApp(ctx, newDeck(audio.NewMixer()), "")
	app.settingsFile = filepath.Join(t.TempDir(), "settings.json")
	app.handle(SetSpectrum{View: want})
	if got := readSettings(app.settingsFile).spectrum(); got != want {
		t.Fatalf("the settings keep %+v, want %+v", got, want)
	}
	// The first run, with no settings, shows the output alone.
	if got := readSettings(filepath.Join(t.TempDir(), "none.json")).spectrum(); got != (SpectrumView{HideIn: true}) {
		t.Fatalf("the first run shows the spectrum as %+v", got)
	}
}

func TestTheLoopChipOnATrackWithNoLoopLoopsItEvenAsAnotherLoops(t *testing.T) {
	a := album()
	a.Looping = true
	w, r, run := stage(t, a)
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
		t.Fatalf("with looping on and no loop, the chip sent %v, want %v", rest, want)
	}
}

func TestAltLeftAndRightGoToTheLoopsInAndOut(t *testing.T) {
	a := album()
	a.Tracks[0].Loop = &Loop{In: 3 * time.Second, Out: 5 * time.Second}
	w, _, run := stage(t, a)
	w.Input(input.KeyPress{Key: input.KeyLeft, Mods: input.ModAlt})
	w.Input(input.KeyPress{Key: input.KeyRight, Mods: input.ModAlt})
	run(1)
	// The second of silence before the track, then its file's time.
	_, rest := edits(w)
	want := []gunim.Intent{SeekTo{At: 4 * time.Second}, SeekTo{At: 6 * time.Second}}
	if !reflect.DeepEqual(rest, want) {
		t.Fatalf("Alt with Left and Right sent %v, want %v", rest, want)
	}
	// With no loop, they seek as Left and Right do.
	a.Tracks[0].Loop = nil
	if err := w.Client().Update("album", a); err != nil {
		t.Fatal(err)
	}
	run(1)
	w.Input(input.KeyPress{Key: input.KeyRight, Mods: input.ModAlt})
	run(1)
	if _, rest := edits(w); len(rest) != 1 || rest[0] != (SeekTo{At: 5 * time.Second}) {
		t.Fatalf("with no loop, Alt and Right sent %v", rest)
	}
}

func TestTheChainsMenuCopiesAndKeepsPresets(t *testing.T) {
	a := chained()
	a.Presets = []string{"Gentle", "Loud"}
	a.Tracks[0].Preset = "Loud"
	w, r, run := stage(t, a)
	c := r.chain
	gunim.RegisterPatch(w, "album", func(_ *root, item int, u *gunim.UI) {
		if item < 0 {
			c.openMore(u)
			return
		}
		if in := c.menu.OnPick(item, u); in != nil {
			u.Send(c, in)
		}
	})
	patch := func(item int) {
		if err := w.Client().Patch("album", item); err != nil {
			t.Fatal(err)
		}
		run(1)
	}
	patch(-1)
	want := []string{"Copy to…", "Copy to B", "Save preset “Loud”", "Save preset as…", "Load preset…", "Delete preset…"}
	items := c.menu.Items()
	labels := make([]string, len(items))
	for i, it := range items {
		labels[i] = it.Label
	}
	if !reflect.DeepEqual(labels, want) || slices.ContainsFunc(items, func(it widget.MenuItem) bool { return it.Disabled }) {
		t.Fatalf("the chain's menu offers %v", items)
	}
	// Load: the presets, the track's ticked; one picked loads.
	patch(4)
	items = c.menu.Items()
	if len(items) != 2 || items[0].Label != "Gentle" || items[1].Label != "Loud" || items[0].Checked || !items[1].Checked {
		t.Fatalf("the presets to load are %v", items)
	}
	patch(0)
	if _, rest := edits(w); len(rest) != 1 || rest[0] != (LoadPreset{Track: 1, Name: "Gentle"}) {
		t.Fatalf("picking a preset sent %v", rest)
	}
}

func TestATimedNotesCardHoldsAllOfItsNote(t *testing.T) {
	a := album()
	long := "The hats get harsh here once the chorus opens up; try a gentler shelf above 8 kHz, " +
		"and ask the artist whether the delay throw on the last word is meant to ring out this long"
	a.Tracks[0].Marks = []Mark{{ID: 1, At: 4 * time.Second, Text: long}, {ID: 2, At: 6 * time.Second, Text: "Hats"}}
	_, r, _ := stage(t, a)
	ed := r.editor
	card, _ := ed.cardOf(0)
	para := ed.noteText(a.Tracks[0].Marks[0])
	if para.Truncated || len(para.Lines) < 3 || card.Size().W > cardMaxW || card.Size().H < 19+para.Size.H {
		t.Fatalf("a long note's card is %v, its text %d lines, cut %v", card.Size(), len(para.Lines), para.Truncated)
	}
	if short, _ := ed.cardOf(1); short.Size().H != 34 {
		t.Fatalf("a short note's card is %v tall, want one line's", short.Size().H)
	}
}

func TestATracksRowCountsItsTimedNotes(t *testing.T) {
	// Drawn without fault, with a note and with notes at times.
	a := album()
	a.Tracks[0].Note = "Low end"
	a.Tracks[0].Marks = []Mark{{ID: 1, At: time.Second, Text: "a"}, {ID: 2, At: 2 * time.Second, Text: "b"}}
	_, _, run := stage(t, a)
	run(2)
}

func TestTheViewSwitchShowsAPluginOpenAndClosesIt(t *testing.T) {
	a := goAlbum(0)
	w, r, run := stage(t, a)
	sw := r.head.view
	open := func() {
		t.Helper()
		a.ShowGo, a.ShowGos = 9, a.ShowGos+1
		if err := w.Client().Publish(albumTopic, a); err != nil {
			t.Fatal(err)
		}
		run(60)
		if !r.pane.isOpen() || sw.shown() != partPlugin || sw.plugIn.Value() < 0.99 || sw.plugName != "Trim" {
			t.Fatalf("opened, the switch shows part %d, the plugin's in %v, named %q", sw.shown(), sw.plugIn.Value(), sw.plugName)
		}
		if r.head.followIn.Value() > 0.01 {
			t.Errorf("with a plugin shown, Follow is still in at %v", r.head.followIn.Value())
		}
	}
	clickPart := func(at geom.Point) {
		t.Helper()
		b := boundsOf(t, w, run, sw)
		w.Input(input.PointerMove{Pos: b.Min.Add(at)})
		w.Input(input.PointerDown{Pos: b.Min.Add(at), Button: input.ButtonPrimary, Clicks: 1})
		w.Input(input.PointerUp{Pos: b.Min.Add(at), Button: input.ButtonPrimary})
		run(60)
	}
	// Wave closes the plugin, the view being the waveform already.
	open()
	clickPart(sw.parts[partWave].Center())
	if _, rest := edits(w); r.pane.isOpen() || len(rest) != 0 || r.head.followIn.Value() < 0.99 {
		t.Fatalf("Wave left the plugin open %v, sent %v, Follow in at %v", r.pane.isOpen(), rest, r.head.followIn.Value())
	}
	// The cross closes it.
	open()
	clickPart(sw.closeAt().Center())
	if r.pane.isOpen() || sw.plugIn.Value() > 0.01 {
		t.Fatalf("the cross left the plugin open %v, its part in at %v", r.pane.isOpen(), sw.plugIn.Value())
	}
	// Spectrogram closes it, and shows the spectrogram.
	open()
	clickPart(sw.parts[partGram].Center())
	if _, rest := edits(w); r.pane.isOpen() || len(rest) != 1 || rest[0] != (SetView{View: ViewGram}) {
		t.Errorf("Spectrogram left the plugin open %v, and sent %v", r.pane.isOpen(), rest)
	}
}

func TestSwitchingPluginsGlidesTheirNameAndHidesWhatIsUnder(t *testing.T) {
	a := goAlbum(0)
	other := trimSlot(10)
	other.Label = "A much longer name"
	a.Tracks[0].Chain = append(a.Tracks[0].Chain, other)
	w, r, run := stage(t, a)
	sw := r.head.view
	show := func(slot int) {
		a.ShowGo, a.ShowGos = slot, a.ShowGos+1
		if err := w.Client().Publish(albumTopic, a); err != nil {
			t.Fatal(err)
		}
	}
	show(9)
	run(60)
	short := sw.partW(partPlugin)
	show(10)
	run(4)
	// Partway: the names crossing, the part between their widths, and
	// a card under the two sheets as solid as they are in together.
	mid := sw.partW(partPlugin)
	if s := sw.swap.Value(); s <= 0.05 || s >= 0.95 || sw.wasName != "Trim" {
		t.Errorf("partway, the names have swapped %v, from %q", s, sw.wasName)
	}
	if mid <= short+1 || mid >= pillWidth(other.Label)+switchClose-1 {
		t.Errorf("partway, the plugin's part is %v wide, from %v to %v", mid, short, pillWidth(other.Label)+switchClose)
	}
	if len(r.pane.sheets) != 2 {
		t.Errorf("partway, %d sheets", len(r.pane.sheets))
	}
	// At every frame of the swap, what is under the pane hardly shows
	// through the card under the sheets and the sheets over it.
	for f := range 40 {
		through := 1 - r.pane.under()
		for _, sh := range r.pane.sheets {
			through *= 1 - min(max(sh.in.Value(), 0), 1)
		}
		if through > 0.05 {
			t.Fatalf("%d frames into the swap, %.0f%% of what is under the pane shows through", f+4, 100*through)
		}
		run(1)
	}
	run(30)
	if sw.swap.Value() < 0.99 || math.Abs(float64(sw.partW(partPlugin)-(pillWidth(other.Label)+switchClose))) > 0.5 {
		t.Errorf("after, the names have swapped %v, the part %v wide", sw.swap.Value(), sw.partW(partPlugin))
	}
}

func TestCtrlClickShowsUpToFourPluginsInAGrid(t *testing.T) {
	a := goAlbum(0)
	for id := 10; id <= 13; id++ {
		a.Tracks[0].Chain = append(a.Tracks[0].Chain, trimSlot(id))
	}
	w, r, run := stage(t, a)
	run(40)
	click := func(id int, mods input.Mods) {
		t.Helper()
		b := boundsOf(t, w, run, r.chain)
		at := b.Min.Add(r.chain.cardRect(r.chain.cards[id]).Center().Add(geom.Pt(20, 0)))
		w.Input(input.PointerMove{Pos: at})
		w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1, Mods: mods})
		w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary, Mods: mods})
		run(1)
		// A plain click asks the app to show the plugin, as it does.
		if mods == 0 {
			a.ShowGo, a.ShowGos = id, a.ShowGos+1
			if err := w.Client().Publish(albumTopic, a); err != nil {
				t.Fatal(err)
			}
		}
		run(60)
	}
	slots := func() []int {
		out := make([]int, 0, len(r.pane.shown))
		for _, v := range r.pane.shown {
			out = append(out, v.slot)
		}
		return out
	}
	click(9, 0)
	click(10, input.ModControl)
	if got := slots(); !slices.Equal(got, []int{9, 10}) {
		t.Fatalf("a click and a Ctrl+click show %v", got)
	}
	// Two side by side, each half the pane.
	size := r.pane.size
	first, second := r.pane.sheets[r.goViews[9]].rect(), r.pane.sheets[r.goViews[10]].rect()
	if first.Min.Y != 0 || second.Min.Y != 0 || first.Max.X >= second.Min.X || math.Abs(float64(first.Size().W-(size.W-paneGap)/2)) > 0.5 ||
		first.Size().H != size.H {
		t.Errorf("two show at %v and %v in a pane %v: not side by side", first, second, size)
	}
	if r.chain.cards[9].open.Target() != 1 || r.chain.cards[10].open.Target() != 1 {
		t.Error("the two cards do not show open")
	}
	if r.head.view.plugName != "Trim · Trim" {
		t.Errorf("the view switch names %q", r.head.view.plugName)
	}
	// Four: two by two.
	click(11, input.ModControl)
	click(12, input.ModControl)
	want := cells(4, size)
	for i, id := range []int{9, 10, 11, 12} {
		if got := r.pane.sheets[r.goViews[id]].rect(); math.Abs(float64(got.Min.X-want[i].Min.X)) > 0.5 ||
			math.Abs(float64(got.Min.Y-want[i].Min.Y)) > 0.5 || math.Abs(float64(got.Size().W-want[i].Size().W)) > 0.5 {
			t.Errorf("of four, the sheet of slot %d stands at %v, want %v", id, got, want[i])
		}
	}
	if want[3].Min.Y <= want[0].Min.Y || want[3].Min.X <= want[0].Min.X {
		t.Errorf("four cells %v: not two by two", want)
	}
	// Each view laid out no smaller than viewLeast, and drawn down to its
	// cell.
	for _, id := range []int{9, 10, 11, 12} {
		sh := r.pane.sheets[r.goViews[id]]
		view := boundsOf(t, w, run, r.goViews[id].paneView())
		if sh.scale >= 1 || sh.scale <= 0 || view.Size().W > sh.rect().Size().W+0.5 {
			t.Errorf("of four, slot %d's view is drawn at %v, %v wide in a cell %v wide", id, sh.scale, view.Size().W, sh.rect().Size().W)
		}
		if lw := view.Size().W / sh.scale; lw < viewLeast.W-0.5 {
			t.Errorf("of four, slot %d's view is laid out %v wide, under %v", id, lw, viewLeast.W)
		}
	}
	// A fifth: the first gives way.
	click(13, input.ModControl)
	if got := slots(); !slices.Equal(got, []int{10, 11, 12, 13}) {
		t.Errorf("a fifth shows %v", got)
	}
	// Ctrl+click on one shown takes it away; its own cross another.
	click(11, input.ModControl)
	gunim.RegisterPatch(w, "album", func(r *root, _ struct{}, u *gunim.UI) { r.pane.sheets[r.goViews[12]].closer.OnClick(u) })
	if err := w.Client().Patch("album", struct{}{}); err != nil {
		t.Fatal(err)
	}
	run(60)
	if got := slots(); !slices.Equal(got, []int{10, 13}) {
		t.Errorf("one Ctrl+clicked away and one closed, %v show", got)
	}
	// A plain click shows the one alone.
	click(9, 0)
	if got := slots(); !slices.Equal(got, []int{9}) || r.pane.sheets[r.goViews[9]].rect().Size() != size {
		t.Errorf("a click shows %v", got)
	}
}

func TestADoubleClickOnAPluginsTitleRenamesIt(t *testing.T) {
	a := goAlbum(0)
	a.ShowGo, a.ShowGos = 9, 1
	w, r, run := stage(t, a)
	run(60)
	sh := r.pane.sheets[r.goViews[9]]
	at := boundsOf(t, w, run, r.pane).Min.Add(sh.rect().Min).Add(geom.Pt(30, 20))
	w.Input(input.PointerMove{Pos: at})
	for _, n := range []int{1, 2} {
		w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: n})
		w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	}
	run(2)
	if !sh.renaming || sh.field.Text() != "Trim" {
		t.Fatalf("double-clicked, the title is renaming %v, from %q", sh.renaming, sh.field.Text())
	}
	edits(w)
	gunim.RegisterPatch(w, "album", func(_ *root, _ struct{}, u *gunim.UI) { sh.field.SetText("More bass", u) })
	if err := w.Client().Patch("album", struct{}{}); err != nil {
		t.Fatal(err)
	}
	run(1)
	w.Input(input.KeyPress{Key: input.KeyEnter})
	run(2)
	if _, rest := edits(w); sh.renaming || !slices.Contains(rest, gunim.Intent(RenamePlugin{Track: 1, Slot: 9, Label: "More bass"})) {
		t.Errorf("Enter sent %v, renaming still %v", rest, sh.renaming)
	}
	// Named so, the title, the switch and the card say it.
	a.Tracks[0].Chain[0].Label = "More bass"
	if err := w.Client().Publish(albumTopic, a); err != nil {
		t.Fatal(err)
	}
	run(30)
	if sh.title.Text != "More bass" || r.head.view.plugName != "More bass" {
		t.Errorf("renamed, the sheet says %q and the switch %q", sh.title.Text, r.head.view.plugName)
	}
}

func TestTheChainsABSwitchPlaysTheOtherSide(t *testing.T) {
	w, r, run := stage(t, chained())
	run(40)
	c := r.chain
	ab, menu := boundsOf(t, w, run, c.ab), boundsOf(t, w, run, c.more)
	if ab.Max.X > menu.Min.X || ab.Max.X < menu.Min.X-12 || math.Abs(float64(ab.Center().Y-menu.Center().Y)) > 0.5 {
		t.Errorf("the A/B switch is at %v, the menu at %v: not just before it", ab, menu)
	}
	edits(w)
	at := geom.Pt(ab.Min.X+abSideW*1.5, ab.Center().Y)
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	run(30)
	if _, rest := edits(w); len(rest) != 1 || rest[0] != (SwitchChain{Track: 1, B: true}) || c.ab.at.Value() < 0.99 {
		t.Fatalf("B clicked, the switch sent %v, its light at %v", rest, c.ab.at.Value())
	}
	// On B, the menu copies to A.
	a := chained()
	a.Tracks[0].OnB = true
	if err := w.Client().Publish(albumTopic, a); err != nil {
		t.Fatal(err)
	}
	run(2)
	gunim.RegisterPatch(w, "album", func(r *root, item int, u *gunim.UI) {
		if item < 0 {
			r.chain.openMore(u)
			return
		}
		u.Send(r, r.chain.menu.OnPick(item, u))
	})
	if err := w.Client().Patch("album", -1); err != nil {
		t.Fatal(err)
	}
	run(1)
	if got := c.menu.Items()[1].Label; got != "Copy to A" {
		t.Fatalf("on B, the menu offers %q", got)
	}
	if err := w.Client().Patch("album", 1); err != nil {
		t.Fatal(err)
	}
	run(1)
	if _, rest := edits(w); len(rest) != 1 || rest[0] != (CopyChainSide{Track: 1}) {
		t.Errorf("Copy to A sent %v", rest)
	}
}
