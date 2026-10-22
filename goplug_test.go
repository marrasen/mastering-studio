package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/plug/eq"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/braid"
	"github.com/marrasen/gunim/input"
)

// A plugin of the tests' own, which keeps a state: what it was last
// given, which it makes no sound of.
func init() {
	plug.Register(plug.Descriptor{ID: "test.keeper", Name: "Keeper", Vendor: "Tests",
		New: func(plug.Host) plug.Plugin { return &keeper{} }})
}

type keeper struct {
	state []byte
	proc  trimless
}

func (k *keeper) Processor() plug.Processor { return &k.proc }
func (k *keeper) State() []byte             { return k.state }
func (k *keeper) Close()                    {}

func (k *keeper) SetState(b []byte) error {
	if bytes.Equal(b, []byte("bad")) {
		return errors.New("bad state")
	}
	k.state = b
	return nil
}

// trimless passes the sound as it is.
type trimless struct{}

func (trimless) Prepare(plug.Setup)  {}
func (trimless) Process(*plug.Block) {}
func (trimless) Latency() int        { return 0 }
func (trimless) Tail() int           { return 0 }
func (trimless) Reset()              {}

// braidSlot is a slot of the braid.
func braidSlot(id int) Slot {
	return Slot{ID: id, Path: goPath, Class: braid.Descriptor.ID, Name: braid.Descriptor.Name}
}

func TestTheGoPluginsAreAmongTheChoices(t *testing.T) {
	ps := scanPlugins(nil)
	if !slices.ContainsFunc(ps, func(c PluginChoice) bool { return c.Path == goPath && c.Class == "marrasen.braid" }) {
		t.Fatal("the braid is not among the plugins to add")
	}
}

func TestATrackMeasuresThroughAGoPlugin(t *testing.T) {
	path := writeTrack(t, 0, 3*time.Second, 0, 0.3)
	dry, err := measure(context.Background(), path, 0, Edit{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	state := goSettings{Params: map[uint32]float64{braid.GainID: -6}}.pack()
	wet, err := measure(context.Background(), path, 0, Edit{}, []Slot{braidSlot(1)}, map[int][]byte{1: state})
	if err != nil {
		t.Fatal(err)
	}
	if !dry.Loud || !wet.Loud || math.Abs(float64(wet.LUFS-(dry.LUFS-6))) > 0.1 {
		t.Fatalf("through the braid at -6 dB the track measures %.2f LUFS, %.2f without", wet.LUFS, dry.LUFS)
	}
}

func TestAGoPluginsSettingsRoundTrip(t *testing.T) {
	lp, err := newPlugin(braidSlot(1), goSettings{Params: map[uint32]float64{braid.GainID: 60}}.pack(), 48000, false)
	if err != nil {
		t.Fatal(err)
	}
	defer lp.p.Close()
	b, err := lp.p.State()
	if err != nil {
		t.Fatal(err)
	}
	s, err := readGoSettings(b)
	if err != nil || s.Params[braid.GainID] != 48 {
		t.Fatalf("the gain set to +60 dB comes back as %v (%v), want +48, its most", s.Params, err)
	}
	if _, err := newPlugin(Slot{ID: 2, Path: goPath, Class: "nobody.nothing", Name: "Nothing"}, nil, 48000, false); err == nil {
		t.Fatal("a Go plugin not built in loaded")
	}
	if err := lp.p.SetState([]byte("{")); err == nil {
		t.Fatal("broken settings were taken")
	}
}

// goApp returns savedApp's album with the braid added to its first
// track, its chain loaded.
func goApp(t *testing.T) (*app, *Track, Slot) {
	t.Helper()
	a := savedApp(t)
	t.Cleanup(a.closeRacks)
	tr := &a.Tracks[0]
	shows := a.ShowGos
	a.handle(AddPlugin{Track: tr.ID, Choice: goChoices()[slices.IndexFunc(goChoices(), func(c PluginChoice) bool {
		return c.Class == braid.Descriptor.ID
	})]})
	if a.ShowGos != shows+1 || len(tr.Chain) != 1 || a.ShowGo != tr.Chain[0].ID {
		t.Fatalf("the braid added has chain %+v, and asks to show %d (%d asks)", tr.Chain, a.ShowGo, a.ShowGos)
	}
	a.rackOf(tr)
	return a, tr, tr.Chain[0]
}

// gainOf returns the gain of slot s's braid, as kept, and as loaded.
func gainOf(t *testing.T, a *app, tr *Track, s Slot) (kept, loaded float64) {
	t.Helper()
	set, err := readGoSettings(a.states[s.ID])
	if err != nil {
		t.Fatal(err)
	}
	e := a.goEngineOf(tr, s.ID)
	if e == nil {
		t.Fatal("the braid is not loaded")
	}
	return set.values(&braid.Descriptor)[braid.GainID], e.param(braid.GainID).value()
}

func TestAGestureOnAGoPluginIsOneStepToUndo(t *testing.T) {
	a, tr, s := goApp(t)
	steps := len(a.undo)
	for _, v := range []float64{-1, -2, -3} {
		a.handle(SetGoParam{Track: tr.ID, Slot: s.ID, ID: braid.GainID, Value: v, Gesture: 1})
	}
	if kept, loaded := gainOf(t, a, tr, s); kept != -3 || loaded != -3 {
		t.Fatalf("dragged to -3 dB, the braid keeps %v and plays %v", kept, loaded)
	}
	if len(a.undo) != steps+1 || a.UndoLabel != "Gain of Braid on "+tr.Title {
		t.Fatalf("one drag made %d steps, the last %q", len(a.undo)-steps, a.UndoLabel)
	}
	// Another gesture, another step.
	a.handle(SetGoParam{Track: tr.ID, Slot: s.ID, ID: braid.GainID, Value: 5, Gesture: 2})
	if len(a.undo) != steps+2 {
		t.Fatalf("two gestures made %d steps", len(a.undo)-steps)
	}
	a.handle(Undo{})
	if kept, loaded := gainOf(t, a, tr, s); kept != -3 || loaded != -3 {
		t.Fatalf("undone, the braid keeps %v and plays %v, want -3", kept, loaded)
	}
	a.handle(Undo{})
	if kept, loaded := gainOf(t, a, tr, s); kept != 0 || loaded != 0 {
		t.Fatalf("undone again, the braid keeps %v and plays %v, want 0", kept, loaded)
	}
	// What the window is shown follows.
	a.showGoSettings()
	if v := a.Tracks[0].Chain[0].Values[braid.GainID]; v != 0 {
		t.Fatalf("the window is shown a gain of %v, want 0", v)
	}
}

func TestAGoPluginsStateIsSetKeptAndUndone(t *testing.T) {
	a, tr, _ := goApp(t)
	a.handle(AddPlugin{Track: tr.ID, Choice: PluginChoice{Path: goPath, Class: "test.keeper", Name: "Keeper"}})
	s := tr.Chain[1]
	steps := len(a.undo)
	// The same edit three times, as a wheel turned, is one step; another
	// is another.
	for _, st := range []string{"once", "twice", "one"} {
		a.handle(SetGoState{Track: tr.ID, Slot: s.ID, State: []byte(st), Name: "Keep one"})
	}
	if len(a.undo) != steps+1 || a.UndoLabel != "Keep one in Keeper on "+tr.Title {
		t.Fatalf("the edit made %d steps, the last called %q", len(a.undo)-steps, a.UndoLabel)
	}
	e := a.goEngineOf(tr, s.ID)
	k, ok := e.p.(*keeper)
	if !ok || string(k.state) != "one" {
		t.Fatal("the keeper loaded was not given its state")
	}
	// The project keeps it, as it keeps a VST3's.
	if kt := a.kept(tr); !bytes.Equal(kt.Chain[1].State, a.states[s.ID]) || kt.Chain[1].Path != goPath {
		t.Fatalf("the project keeps the slot as %+v", kt.Chain[1])
	}
	a.handle(Undo{})
	if len(k.state) != 0 {
		t.Fatalf("undone, the keeper keeps %q", k.state)
	}
	a.handle(Redo{})
	if string(k.state) != "one" {
		t.Fatalf("redone, the keeper keeps %q", k.state)
	}
	// A state the plugin refuses is neither kept nor played.
	a.handle(SetGoState{Track: tr.ID, Slot: s.ID, State: []byte("bad"), Name: "Keep bad"})
	if a.Note == "" || string(k.state) != "one" {
		t.Fatalf("a state refused leaves %q, and says %q", k.state, a.Note)
	}
	if set, _ := readGoSettings(a.states[s.ID]); string(set.State) != "one" {
		t.Fatalf("a state refused is kept: %q", set.State)
	}
}

func TestAGoPluginOpensItsViewsInTheWindow(t *testing.T) {
	a, tr, s := goApp(t)
	shows := a.ShowGos
	a.handle(ShowEditor{Track: tr.ID, Slot: s.ID})
	if a.ShowGos != shows+1 || a.ShowGo != s.ID || a.Note != "" {
		t.Fatalf("asked to open, the braid asks the window %d times (%q)", a.ShowGos-shows, a.Note)
	}
}

// goAlbum is album's track with the braid, at gain.
func goAlbum(gain float64) Album {
	a := album()
	s := braidSlot(9)
	s.Values = map[uint32]float64{braid.GainID: gain}
	a.Tracks[0].Chain = []Slot{s}
	return a
}

func TestTheBraidsChipShowsItsGainAndFollowsIt(t *testing.T) {
	w, r, run := stage(t, goAlbum(-3))
	v := r.goViews[9]
	if v == nil || v.chip == nil || !slices.Contains(r.chain.Children(), v.chip) {
		t.Fatal("the braid's card has no chip")
	}
	// The chip reads as what it says.
	text := func() string {
		a, ok := v.chip.(gunim.Accessible)
		if !ok {
			t.Fatal("the chip reads as nothing")
		}
		return a.Access().Name
	}
	if got := text(); got != "-3.0 dB" {
		t.Fatalf("the chip says %q, want -3.0 dB", got)
	}
	if err := w.Client().Publish(albumTopic, goAlbum(1.5)); err != nil {
		t.Fatal(err)
	}
	run(2)
	if got := text(); got != "+1.5 dB" {
		t.Fatalf("set elsewhere, the chip says %q, want +1.5 dB", got)
	}
}

func TestAGoPluginsViewsSendTheirChangesAndKeepThemAsTheNewsCatchesUp(t *testing.T) {
	w, r, run := stage(t, goAlbum(0))
	a := goAlbum(0)
	a.ShowGo, a.ShowGos = 9, 1
	if err := w.Client().Publish(albumTopic, a); err != nil {
		t.Fatal(err)
	}
	run(10)
	if !r.pane.isOpen() || r.pane.v != r.goViews[9] {
		t.Fatal("asked to, the window shows no braid")
	}
	if b := boundsOf(t, w, run, r.pane); b.Size().W < 100 {
		t.Fatalf("the pane is %v", b)
	}
	// A drag in the pane: two moves, one gesture.
	gunim.RegisterPatch(w, "album", func(r *root, _ struct{}, u *gunim.UI) {
		h := goHost{r.goViews[9]}
		h.Begin(braid.GainID, u)
		h.Set(braid.GainID, -2, u)
		h.Set(braid.GainID, -4, u)
		h.End(braid.GainID, u)
	})
	if err := w.Client().Patch("album", struct{}{}); err != nil {
		t.Fatal(err)
	}
	run(1)
	_, sent := edits(w)
	want := []gunim.Intent{SetGoParam{Track: 1, Slot: 9, ID: braid.GainID, Value: -2, Gesture: 1},
		SetGoParam{Track: 1, Slot: 9, ID: braid.GainID, Value: -4, Gesture: 1}}
	if !slices.Equal(sent, want) {
		t.Fatalf("the drag sent %v, want %v", sent, want)
	}
	// News of the first move comes late: the views keep the last.
	a.Tracks[0].Chain[0].Values = map[uint32]float64{braid.GainID: -2}
	if err := w.Client().Publish(albumTopic, a); err != nil {
		t.Fatal(err)
	}
	run(2)
	if v := r.goViews[9].values[braid.GainID]; v != -4 {
		t.Fatalf("news of an older move set the views to %v", v)
	}
	// Then the news of the last, then of an undo, which they follow.
	for _, g := range []float64{-4, 0} {
		a.Tracks[0].Chain[0].Values = map[uint32]float64{braid.GainID: g}
		if err := w.Client().Publish(albumTopic, a); err != nil {
			t.Fatal(err)
		}
		run(2)
	}
	if v := r.goViews[9].values[braid.GainID]; v != 0 {
		t.Fatalf("undone, the views show %v, want 0", v)
	}
	// Escape closes the pane.
	w.Input(input.KeyPress{Key: input.KeyEscape})
	run(2)
	if r.pane.isOpen() {
		t.Fatal("Escape left the pane open")
	}
}

func TestATrackMeasuresThroughTheEQ(t *testing.T) {
	path := writeTrack(t, 0, 3*time.Second, 0, 0.3)
	dry, err := measure(context.Background(), path, 0, Edit{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A shelf lifting everything over 20 Hz by 6 dB.
	bands := []byte(`{"version":1,"bands":[{"id":1,"kind":"highShelf","freq":20,"gain":6,"q":0.7,"on":true}]}`)
	state := goSettings{State: bands}.pack()
	s := Slot{ID: 1, Path: goPath, Class: "marrasen.eq", Name: "EQ"}
	wet, err := measure(context.Background(), path, 0, Edit{}, []Slot{s}, map[int][]byte{1: state})
	if err != nil {
		t.Fatal(err)
	}
	if !dry.Loud || !wet.Loud || math.Abs(float64(wet.LUFS-(dry.LUFS+6))) > 0.5 {
		t.Fatalf("through the EQ lifting all by 6 dB the track measures %.2f LUFS, %.2f without", wet.LUFS, dry.LUFS)
	}
}

func TestAGoPluginRunsWithoutLockingOrMakingAnything(t *testing.T) {
	lp, err := newPlugin(braidSlot(1), nil, 48000, false)
	if err != nil {
		t.Fatal(err)
	}
	defer lp.p.Close()
	e, ok := lp.p.(*goEngine)
	if !ok {
		t.Fatal("the braid loaded is no goEngine")
	}
	frames := make([]float32, 2*1500)
	// The gain set from another goroutine as it plays, for the race
	// detector, then every allocation in Process counted.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 2000 {
			e.setParam(braid.GainID, float64(i%48-24))
		}
	}()
	for running := true; running; {
		select {
		case <-done:
			running = false
		default:
			e.Process(frames)
		}
	}
	if raceOn {
		t.Skip("the race detector allocates of its own")
	}
	// On one thread, as testing.AllocsPerRun counts, so other
	// goroutines allocate as little as may be meanwhile.
	made := allocsUnder(t, "(*goEngine).Process", func() {
		for i := range 50 {
			e.setParam(braid.GainID, float64(i%10))
			e.Process(frames)
		}
	})
	if made != 0 {
		t.Fatalf("processing made %d things over 50 blocks", made)
	}
	// Sets between two blocks come as one change, of the last.
	e.setParam(braid.GainID, -6)
	e.setParam(braid.GainID, -12)
	e.Process(frames[:2])
	if len(e.changes) != 1 || e.changes[0].Value != -12 {
		t.Fatalf("two sets came as %v", e.changes)
	}
}

func TestThePaneAnimatesItsPluginsInOutAndAcross(t *testing.T) {
	a := goAlbum(0)
	eqSlot := Slot{ID: 10, Path: goPath, Class: "marrasen.eq", Name: "EQ", Values: map[uint32]float64{1: 0}}
	a.Tracks[0].Chain = append(a.Tracks[0].Chain, eqSlot)
	w, r, run := stage(t, a)
	show := func(id, n int) {
		t.Helper()
		a.ShowGo, a.ShowGos = id, n
		if err := w.Client().Publish(albumTopic, a); err != nil {
			t.Fatal(err)
		}
	}
	show(9, 1)
	// A frame to start, and one to move.
	run(2)
	braidSheet := r.pane.sheets[r.goViews[9]]
	if braidSheet == nil || braidSheet.in.Value() <= 0 || braidSheet.in.Value() >= 1 {
		t.Fatal("the braid's sheet did not start coming in")
	}
	run(60)
	if braidSheet.in.Value() != 1 || r.chain.cards[9].open.Target() != 1 {
		t.Fatalf("the braid's sheet is in %v, its card open %v", braidSheet.in.Value(), r.chain.cards[9].open.Target())
	}
	// The EQ in the braid's place: the two cross.
	show(10, 2)
	run(2)
	eqSheet := r.pane.sheets[r.goViews[10]]
	if !braidSheet.leaving || braidSheet.in.Value() <= 0 || braidSheet.in.Value() >= 1 || eqSheet.in.Value() <= 0 || eqSheet.in.Value() >= 1 {
		t.Fatalf("crossing, the braid's sheet is in %v (leaving %v), the EQ's %v", braidSheet.in.Value(), braidSheet.leaving, eqSheet.in.Value())
	}
	run(60)
	if braidSheet.in.Value() != 0 || eqSheet.in.Value() != 1 || r.chain.cards[9].open.Target() != 0 || r.chain.cards[10].open.Target() != 1 {
		t.Fatal("after crossing, the braid's sheet is not gone, or the EQ's not in")
	}
	// Closing: the EQ's sheet goes, a moment after.
	w.Input(input.KeyPress{Key: input.KeyEscape})
	run(2)
	if r.pane.isOpen() || !eqSheet.leaving || eqSheet.in.Value() <= 0 {
		t.Fatal("Escape closed the pane at once, or not at all")
	}
	run(60)
	if eqSheet.in.Value() != 0 || r.chain.cards[10].open.Target() != 0 {
		t.Fatal("the EQ's sheet did not go")
	}
}

// A plugin of the tests' own that reports the peak of the last block
// it processed, as a ReadOnly parameter.
func init() {
	plug.Register(plug.Descriptor{ID: "test.reporter", Name: "Reporter", Vendor: "Tests",
		Params: []plug.Param{{ID: 1, Name: "Peak", Max: 1, Default: 0.5, Flags: plug.ReadOnly}},
		New:    func(plug.Host) plug.Plugin { return &reporter{} }})
}

type reporter struct{ proc reporterProc }

type reporterProc struct{ peak plug.Meter }

func (r *reporter) Processor() plug.Processor { return &r.proc }
func (r *reporter) State() []byte             { return nil }
func (r *reporter) SetState([]byte) error     { return nil }
func (r *reporter) Close()                    {}

func (*reporterProc) Prepare(plug.Setup) {}
func (p *reporterProc) Process(b *plug.Block) {
	var peak float32
	for _, ch := range b.In {
		for _, v := range ch[:b.Frames] {
			peak = max(peak, v, -v)
		}
	}
	p.peak.Set(float64(peak))
}
func (*reporterProc) Latency() int            { return 0 }
func (*reporterProc) Tail() int               { return 0 }
func (*reporterProc) Reset()                  {}
func (p *reporterProc) Report(uint32) float64 { return p.peak.Get() }

func TestAPluginsViewsReadWhatItReports(t *testing.T) {
	d, ok := plug.Lookup("test.reporter")
	if !ok {
		t.Fatal("the reporter is not registered")
	}
	v := &goView{d: d, slot: 77, values: map[uint32]float64{}}
	h := goHost{v}
	// Playing nowhere, the parameter reads its default.
	if got := h.Param(1); got != 0.5 {
		t.Fatalf("playing nowhere, the peak reads %v, want the default 0.5", got)
	}
	lp, err := newPlugin(Slot{ID: 77, Path: goPath, Class: "test.reporter", Name: "Reporter"}, nil, 48000, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		lp.forget()
		lp.p.Close()
	}()
	frames := make([]float32, 2*256)
	frames[100] = -0.75
	lp.p.Process(frames)
	if got := h.Param(1); got != 0.75 {
		t.Fatalf("the views read a peak of %v, want 0.75, as it reports", got)
	}
}

func TestAPluginsViewsSeeItsBypassAtOnce(t *testing.T) {
	a := savedApp(t)
	id := a.Tracks[0].ID
	a.handle(AddPlugin{Track: id, Choice: PluginChoice{Path: goPath, Class: eq.Descriptor.ID, Name: "EQ"}})
	a.rackOf(&a.Tracks[0])
	slot := a.Tracks[0].Chain[0].ID
	for _, on := range []bool{true, false, true} {
		a.handle(SetBypass{Track: id, Slot: slot, On: on})
		a.showGoSettings()
		want := 0.0
		if on {
			want = 1
		}
		if got := a.Tracks[0].Chain[0].Values[eq.BypassID]; got != want {
			t.Fatalf("bypassed %v, the EQ's views are shown its bypass at %v", on, got)
		}
	}
}

func TestAGoPluginNoLongerBuiltInIsDropped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newApp(ctx, newDeck(audio.NewMixer()), "")
	gone := keptSlot{Path: goPath, Class: "marrasen.trim", Name: "Trim"}
	built := keptSlot{Path: goPath, Class: braid.Descriptor.ID, Name: "Braid"}
	// A VST3 is no Go plugin, whatever its class.
	vst := keptSlot{Path: "/plugins/x.vst3", Class: "marrasen.trim", Name: "Elsewhere"}
	names := func(chain []Slot) (out []string) {
		for _, s := range chain {
			out = append(out, s.Name)
		}
		return out
	}
	id := a.restore(keptTrack{File: writeAlbum(t)[0], Title: "One", Chain: []keptSlot{gone, built, vst},
		Other: []keptSlot{built, gone}}, false)
	tr := a.track(id)
	if got := names(tr.Chain); !slices.Equal(got, []string{"Braid", "Elsewhere"}) {
		t.Fatalf("the album's chain is read as %v", got)
	}
	if len(tr.Other) != 1 || tr.Other[0].Name != "Braid" {
		t.Fatalf("session B's chain is read as %+v", tr.Other)
	}

	dir := t.TempDir()
	write := func(name string, v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	a.presetsFile = write("presets.json", keptPresets{Presets: []Preset{{Name: "Old", Chain: []keptSlot{gone, built}}}})
	a.loadPresets()
	if len(a.presets) != 1 || len(a.presets[0].Chain) != 1 || a.presets[0].Chain[0].Name != "Braid" {
		t.Fatalf("the presets are read as %+v", a.presets)
	}
	a.patchesFile = write("patches.json", keptPatches{Patches: []keptPatch{
		{Class: "marrasen.trim", Patch: plug.Patch{Name: "Quiet"}},
		{Class: braid.Descriptor.ID, Patch: plug.Patch{Name: "Loud"}}}})
	a.loadPatches()
	if !slices.Equal(a.Patches, []PatchName{{Class: braid.Descriptor.ID, Name: "Loud"}}) {
		t.Fatalf("the patches are read as %v", a.Patches)
	}
	st := readSettings(write("settings.json", settings{Plugins: []PluginChoice{
		{Path: goPath, Class: "marrasen.trim", Name: "Trim"}, {Path: goPath, Class: braid.Descriptor.ID, Name: "Braid"}}}))
	if p := st.plugins(); len(p) != 1 || p[0].Name != "Braid" {
		t.Fatalf("the plugins added last are read as %v", p)
	}
}
