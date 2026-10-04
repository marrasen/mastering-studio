package main

import (
	"bytes"
	"context"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/vst3"
)

// lspSlot returns a slot of LSP's effect name, where LSP's plugins are:
// LSP_VST3 names their bundle.
func lspSlot(t *testing.T, id int, name string) Slot {
	t.Helper()
	path := os.Getenv("LSP_VST3")
	if path == "" {
		path = "/tmp/lsp/lsp-plugins-1.2.35-Linux-x86_64/VST3/lsp-plugins.vst3"
	}
	if _, err := os.Stat(path); err != nil {
		t.Skip("no LSP plugins at", path)
	}
	m, err := loadModule(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range m.Effects() {
		if c.Name == name {
			return Slot{ID: id, Path: path, Class: c.IDString(), Name: c.Name}
		}
	}
	t.Fatalf("no %s", name)
	return Slot{}
}

// clicks is a second of silence at 44.1 kHz with one click, at frame at.
type clicks struct {
	at, pos, n int64
}

func (c *clicks) Len() int64 { return c.n }

func (c *clicks) SeekFrame(f int64) error {
	c.pos = f
	return nil
}

func (c *clicks) Read(dst []float32) (int, error) {
	k := int(min(int64(len(dst)/2), c.n-c.pos))
	if k <= 0 {
		return 0, io.EOF
	}
	clear(dst[:2*k])
	if c.at >= c.pos && c.at < c.pos+int64(k) {
		i := c.at - c.pos
		dst[2*i], dst[2*i+1] = 0.25, 0.25
	}
	c.pos += int64(k)
	return k, nil
}

func TestAChainsLatencyIsMadeUp(t *testing.T) {
	s := lspSlot(t, 1, "Limiter Stereo")
	r, failed := newRack([]Slot{s}, nil, 44100, true)
	if failed != nil {
		t.Fatal(failed)
	}
	defer r.close()
	if lat := r.latencyLocked(); lat == 0 {
		t.Fatal("the limiter has no latency to make up")
	}
	for _, seek := range []int64{0, 5000} {
		st := newStage(&clicks{at: 20000, n: 44100}, r)
		if err := st.SeekFrame(seek); err != nil {
			t.Fatal(err)
		}
		at, loudest, total := int64(-1), float32(0), seek
		buf := make([]float32, 2*700)
		for {
			n, err := st.Read(buf)
			for i := range n {
				if v := float32(math.Abs(float64(buf[2*i]))); v > loudest {
					loudest, at = v, total+int64(i)
				}
			}
			total += int64(n)
			if err != nil || n == 0 {
				break
			}
		}
		if at != 20000 {
			t.Errorf("from %d: the click comes out at frame %d, want 20000, as it went in", seek, at)
		}
		if total != 44100 {
			t.Errorf("from %d: the stage played to frame %d, want 44100", seek, total)
		}
	}
}

func TestATrackMeasuresThroughItsChain(t *testing.T) {
	s := lspSlot(t, 1, "Parametric Equalizer x16 Stereo")
	// The equalizer, its output down, kept as a state.
	lp, err := newPlugin(s, nil, 44100, false)
	if err != nil {
		t.Fatal(err)
	}
	var gain vst3.Param
	for _, q := range lp.p.Params() {
		if q.Title == "Output gain" {
			gain = q
		}
	}
	lp.p.Set(gain.ID, gain.Default/2)
	lp.p.Process(make([]float32, 2*512))
	state, err := lp.p.State()
	lp.p.Close()
	if err != nil {
		t.Fatal(err)
	}
	path := writeTrack(t, 0, 3*time.Second, 0, 0.3)
	dry, err := measure(context.Background(), path, 0, Edit{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	wet, err := measure(context.Background(), path, 0, Edit{}, []Slot{s}, map[int][]byte{1: state})
	if err != nil {
		t.Fatal(err)
	}
	if !dry.Loud || !wet.Loud || wet.LUFS > dry.LUFS-3 {
		t.Fatalf("through the equalizer turned down the track measures %.1f LUFS, %.1f without", wet.LUFS, dry.LUFS)
	}
	if wet.Length != dry.Length {
		t.Fatalf("through the chain the track is %v long, %v without", wet.Length, dry.Length)
	}
}

// chainApp returns an application of the three tracks writeAlbum
// writes, each with LSP's equalizer in its chain.
func chainApp(t *testing.T, mix *audio.Mixer) *app {
	t.Helper()
	s := lspSlot(t, 0, "Parametric Equalizer x16 Stereo")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a := newApp(ctx, newDeck(mix), "")
	t.Cleanup(a.closeRacks)
	for _, p := range writeAlbum(t) {
		a.add(p, "", Edit{})
	}
	a.handle(AddPlugin{Track: a.Tracks[0].ID, Choice: PluginChoice{Path: s.Path, Class: s.Class, Name: s.Name}})
	a.handle(CopyChain{From: a.Tracks[0].ID})
	return a
}

// eventually waits for cond, for up to a second.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(time.Second); !cond(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal(what)
		}
	}
}

func TestOnlyTheTrackHeardRunsItsPlugins(t *testing.T) {
	mix := audio.NewMixer()
	a := chainApp(t, mix)
	one, two := a.Tracks[0].ID, a.Tracks[1].ID
	a.handle(TogglePlay{})
	mix.Mix(make([]float32, 2*4096))
	if !a.racks[one].active {
		t.Fatal("the track playing has its plugins stopped")
	}
	a.handle(Pick{ID: two})
	// The first track fades out, and stops its plugins.
	for range 10 {
		mix.Mix(make([]float32, 2*4096))
	}
	eventually(t, "the track left still runs its plugins", func() bool {
		a.racks[one].mu.Lock()
		defer a.racks[one].mu.Unlock()
		return !a.racks[one].active
	})
	if !a.racks[two].active {
		t.Fatal("the track picked has its plugins stopped")
	}
	if r := a.racks[a.Tracks[2].ID]; r != nil && r.active {
		t.Fatal("a track never heard runs its plugins")
	}
}

func TestCopyingAChainCopiesEveryPluginAsSet(t *testing.T) {
	a := chainApp(t, audio.NewMixer())
	from := a.Tracks[0]
	// The first track's equalizer, set, then copied on.
	lp := a.rackOf(&a.Tracks[0]).find(from.Chain[0].ID)
	a.readRack(from.ID)
	was := a.states[from.Chain[0].ID]
	for _, q := range lp.p.Params() {
		if q.Title == "Output gain" {
			lp.p.Set(q.ID, q.Default/3)
		}
	}
	// LSP's state takes up a change a moment after it processes it.
	eventually(t, "the equalizer's state never took the change", func() bool {
		a.readRack(from.ID)
		return !bytes.Equal(a.states[from.Chain[0].ID], was)
	})
	a.handle(CopyChain{From: from.ID, To: []int{a.Tracks[2].ID}})
	got := a.Tracks[2].Chain
	if len(got) != 1 || got[0].Name != from.Chain[0].Name || got[0].ID == from.Chain[0].ID {
		t.Fatalf("the copy's chain is %+v", got)
	}
	if !bytes.Equal(a.states[got[0].ID], a.states[from.Chain[0].ID]) || len(a.states[got[0].ID]) == 0 {
		t.Fatal("the copy's equalizer is not set as the first's")
	}
	if bytes.Equal(a.states[a.Tracks[1].Chain[0].ID], a.states[from.Chain[0].ID]) {
		t.Fatal("a track not copied to took the first's setting")
	}
}

func TestAlbumPlayRunsOnIntoTheNextTrackWithoutAGap(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mix := audio.NewMixer()
	a := newApp(ctx, newDeck(mix), "")
	for _, p := range writeAlbum(t) {
		a.add(p, "", Edit{})
	}
	a.handle(SetAlbumPlay{On: true})
	a.handle(TogglePlay{})
	a.queueNext()
	// The first track, its second of silence and three of sound, and
	// half a second into the second.
	buf := make([]float32, 2*audio.SampleRate/10)
	for range 45 {
		mix.Mix(buf)
	}
	var id int
	select {
	case id = <-a.d.turns:
	case <-time.After(time.Second):
		t.Fatal("the deck never turned to the next track")
	}
	a.turned(id)
	if a.Current != a.Tracks[1].ID {
		t.Fatalf("playing track %d, want the second, %d", a.Current, a.Tracks[1].ID)
	}
	at, _, playing := a.d.position()
	if playing != a.Tracks[1].ID || at > 600*time.Millisecond {
		t.Fatalf("the second track plays at %v (track %d), want half a second in", at, playing)
	}
	// The third is queued next.
	a.queueNext()
	if a.queued.id != a.Tracks[2].ID {
		t.Fatalf("queued track %d, want the third", a.queued.id)
	}
}

func TestReplacingATracksFileKeepsItsEditAndChain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newApp(ctx, newDeck(audio.NewMixer()), "")
	paths := writeAlbum(t)
	a.add(paths[0], "Opener", Edit{})
	tr := &a.Tracks[0]
	e := Edit{Start: 200 * time.Millisecond, FadeIn: Fade{Length: 50 * time.Millisecond, Curve: Smooth}, Gain: -1.5}
	tr.Edit = e
	settle(t, a, func() bool { return tr.Scanned && tr.Measured })
	// A chain, as kept; its plugin is none to load, so it goes again
	// before the measuring.
	tr.Chain = []Slot{{ID: 9, Name: "Some EQ"}}
	a.handle(ReplaceFile{ID: tr.ID, Path: paths[1]})
	if tr.File != paths[1] || tr.Scanned || tr.Title != "Opener" || tr.Edit != e || len(tr.Chain) != 1 {
		t.Fatalf("after the replacement the track is %+v", *tr)
	}
	tr.Chain = nil
	if !tr.Stale {
		t.Fatal("a new file leaves the track's measure as fresh")
	}
	a.handle(CalcLoudness{})
	settle(t, a, func() bool { return tr.Scanned && !tr.Stale && !tr.Measuring })
	if tr.Frames == 0 {
		t.Fatal("the new file was never read")
	}
}

func TestEndingStopsThePluginsAtWorkBeforeUnloadingThem(t *testing.T) {
	mix := audio.NewMixer()
	a := chainApp(t, mix)
	a.handle(TogglePlay{})
	mix.Mix(make([]float32, 2*4096))
	// Every track's measuring starts, through copies of its chain.
	for id := range a.settle {
		a.settle[id] = time.Now()
	}
	a.startMeasures()
	time.Sleep(20 * time.Millisecond)
	done := make(chan struct{})
	go func() {
		a.shutdown()
		close(done)
	}()
	// The sound plays on meanwhile, through the chain being let go.
	for range 20 {
		mix.Mix(make([]float32, 2*512))
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ending never finished")
	}
	modulesMu.Lock()
	left := len(modules)
	modulesMu.Unlock()
	if left != 0 || len(a.racks) != 0 {
		t.Fatalf("after the end, %d modules and %d chains are loaded", left, len(a.racks))
	}
}

// measuredApp returns an application of the three tracks writeAlbum
// writes, each measured, kept in a project file of its own.
func measuredApp(t *testing.T) *app {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a := newApp(ctx, newDeck(audio.NewMixer()), filepath.Join(t.TempDir(), "album.json"))
	for _, p := range writeAlbum(t) {
		a.add(p, "", Edit{})
	}
	settle(t, a, func() bool {
		for _, tr := range a.Tracks {
			if !tr.Measured || tr.Measuring || tr.Stale {
				return false
			}
		}
		return true
	})
	return a
}

func TestAChangeWaitsForCalcLoudnessToBeMeasured(t *testing.T) {
	a := measuredApp(t)
	tr := &a.Tracks[1]
	was := tr.Measure.LUFS
	a.handle(SetEdit{ID: tr.ID, Edit: Edit{Gain: -6}, Seq: 1})
	if !tr.Stale || tr.Measuring || len(a.settle) != 0 {
		t.Fatalf("after a change the track is stale %v, measuring %v, with %d measurings due", tr.Stale, tr.Measuring, len(a.settle))
	}
	a.handle(CalcLoudness{})
	if !a.Tracks[1].Measuring || a.Tracks[0].Measuring {
		t.Fatal("Calc LUFS measures other than the track changed")
	}
	settle(t, a, func() bool { return !tr.Measuring })
	if tr.Stale || math.Abs(float64(tr.Measure.LUFS-(was-6))) > 0.1 {
		t.Fatalf("measured again, the track is %.2f LUFS (stale %v), want %.2f", tr.Measure.LUFS, tr.Stale, was-6)
	}
}

func TestAChangeWhileMeasuringLeavesTheTrackStale(t *testing.T) {
	a := measuredApp(t)
	tr := &a.Tracks[0]
	a.handle(SetEdit{ID: tr.ID, Edit: Edit{Gain: -3}, Seq: 1})
	a.handle(CalcLoudness{})
	a.startMeasures()
	// Changed again before the measure comes back.
	a.handle(SetEdit{ID: tr.ID, Edit: Edit{Gain: -9}, Seq: 2})
	settle(t, a, func() bool { return !tr.Measuring })
	if !tr.Stale {
		t.Fatal("a measure of the track as it was left it fresh")
	}
}

func TestAProjectKeepsItsMeasuresUntilAFileChanges(t *testing.T) {
	a := measuredApp(t)
	a.handle(SetEdit{ID: a.Tracks[2].ID, Edit: Edit{Gain: -1}, Seq: 1})
	a.save()
	// The second file changes on disk.
	f, err := os.OpenFile(a.Tracks[1].File, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte{0})
	_ = f.Close()
	b := newApp(a.ctx, newDeck(audio.NewMixer()), a.file)
	b.load()
	one, two, three := b.Tracks[0], b.Tracks[1], b.Tracks[2]
	if !one.Measured || one.Measuring || one.Stale || one.Measure.LUFS != a.Tracks[0].Measure.LUFS {
		t.Fatalf("the first track came back %+v, want as measured", one)
	}
	if !two.Measuring {
		t.Fatal("a track whose file changed is not measured again")
	}
	if !three.Measured || !three.Stale || three.Measuring {
		t.Fatal("a track changed since it was measured came back otherwise")
	}
	// The album's loudness waits for the track measured again.
	settle(t, b, func() bool { return !b.Tracks[1].Measuring })
	if !b.Loudness.Loud {
		t.Fatal("with every track measured, the album has no loudness")
	}
}

func TestAnExportedTrackIsMeasuredAsWritten(t *testing.T) {
	a := measuredApp(t)
	tr := &a.Tracks[0]
	a.handle(SetEdit{ID: tr.ID, Edit: Edit{Gain: -4}, Seq: 1})
	a.ExportDir = t.TempDir()
	a.handle(Export{IDs: []int{tr.ID}})
	settle(t, a, func() bool { return !a.Exporting })
	if tr.Stale || tr.Measure.LUFS != tr.Out.LUFS {
		t.Fatalf("exported, the track is stale %v at %.2f LUFS, written at %.2f", tr.Stale, tr.Measure.LUFS, tr.Out.LUFS)
	}
}

func TestThePluginsAddedLastComeFirst(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newApp(ctx, newDeck(audio.NewMixer()), "")
	for i := range 10 {
		a.used(PluginChoice{Path: "p", Class: strconv.Itoa(i)})
	}
	a.used(PluginChoice{Path: "p", Class: "4"})
	if len(a.Recent) != recentPlugins || a.Recent[0].Class != "4" || a.Recent[1].Class != "9" {
		t.Fatalf("recent are %v", a.Recent)
	}
}
