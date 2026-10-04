package main

import (
	"bytes"
	"context"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
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

func TestToTargetFindsTheGainThatBringsATrackThere(t *testing.T) {
	path := writeTrack(t, 0, 6*time.Second, 0, 0.3)
	// Plain, the gain is found at once; through a limiter, in a few
	// measures.
	g, m, err := matchGain(context.Background(), path, 0, Edit{}, nil, nil, -20)
	if err != nil || math.Abs(float64(m.LUFS+20)) > matchWithin {
		t.Fatalf("plain: gain %.2f measures %.2f LUFS (%v), want -20", g, m.LUFS, err)
	}
	// Through a compressor, the loudness follows the gain less than one
	// for one.
	s := lspSlot(t, 1, "Compressor Stereo")
	plain, _, _ := matchGain(context.Background(), path, 0, Edit{}, nil, nil, -12)
	g, m, err = matchGain(context.Background(), path, 0, Edit{}, []Slot{s}, nil, -12)
	if err != nil || math.Abs(float64(m.LUFS+12)) > matchWithin {
		t.Fatalf("compressed: gain %.2f measures %.2f LUFS (%v), want -12", g, m.LUFS, err)
	}
	if g-plain < 0.2 {
		t.Fatalf("compressed, the gain is %.2f, plain %.2f: the compressor took no part", g, plain)
	}
}

func TestAMatchAppliesItsGainAndMeasure(t *testing.T) {
	a := measuredApp(t)
	tr := &a.Tracks[1]
	a.Target = -20
	a.handle(MatchTarget{ID: tr.ID})
	var r matched
	select {
	case r = <-a.matches:
	case <-time.After(10 * time.Second):
		t.Fatal("no match")
	}
	a.matchedGain(r)
	if tr.Matching || tr.Stale || math.Abs(float64(tr.Measure.LUFS+20)) > matchWithin || tr.Edit.Gain != r.gain {
		t.Fatalf("matched, the track has gain %.2f, %.2f LUFS, stale %v", tr.Edit.Gain, tr.Measure.LUFS, tr.Stale)
	}
	// A track changed while it is matched keeps its change.
	a.handle(MatchTarget{ID: tr.ID})
	a.handle(SetEdit{ID: tr.ID, Edit: Edit{Gain: 3}, Seq: 99})
	r = <-a.matches
	a.matchedGain(r)
	if tr.Edit.Gain != 3 {
		t.Fatalf("a match overwrote a change made meanwhile: gain %.2f", tr.Edit.Gain)
	}
}

func TestAnAlbumMovesWithItsFolderAndSwitchesBack(t *testing.T) {
	a := measuredApp(t)
	// The album saved beside its tracks keeps them by their names.
	dir := filepath.Dir(a.Tracks[0].File)
	album := filepath.Join(dir, "Night"+albumExt)
	a.saveAs(album)
	b, err := os.ReadFile(album)
	if err != nil || !strings.Contains(string(b), `"File": "a.wav"`) {
		t.Fatalf("the album keeps its tracks as %s", b)
	}
	if a.AlbumName != "Night" {
		t.Fatalf("the album is called %q", a.AlbumName)
	}
	// Another album, made empty, then this one again.
	a.switchTo(filepath.Join(t.TempDir(), "Other"+albumExt), true)
	if len(a.Tracks) != 0 || a.AlbumName != "Other" || a.Loudness.Loud {
		t.Fatalf("a new album opened with %d tracks, called %q", len(a.Tracks), a.AlbumName)
	}
	a.handle(OpenAlbumPath{Path: album})
	if len(a.Tracks) != 3 || a.Tracks[0].File != filepath.Join(dir, "a.wav") || !a.Tracks[0].Measured || a.Tracks[0].Measuring {
		t.Fatalf("back, the album has %d tracks, the first %+v", len(a.Tracks), a.Tracks)
	}
	// Moved whole, it finds its tracks where it is.
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	c := newApp(a.ctx, newDeck(audio.NewMixer()), filepath.Join(moved, "Night"+albumExt))
	c.load()
	// The track in its folder moved with it; one elsewhere is kept where
	// it is.
	if len(c.Tracks) != 3 || c.Tracks[0].File != filepath.Join(moved, "a.wav") || c.Tracks[1].File != a.Tracks[1].File {
		t.Fatalf("moved, the album's tracks are in %q and %q", c.Tracks[0].File, c.Tracks[1].File)
	}
}

func TestAnAlbumGoneIsLeftOutOfTheRecent(t *testing.T) {
	a := measuredApp(t)
	gone := filepath.Join(t.TempDir(), "gone"+albumExt)
	a.RecentAlbums = []string{gone}
	a.handle(OpenAlbumPath{Path: gone})
	if len(a.RecentAlbums) != 0 || a.Note == "" || len(a.Tracks) != 3 {
		t.Fatalf("opening an album gone left recent %v, note %q, %d tracks", a.RecentAlbums, a.Note, len(a.Tracks))
	}
}

func TestAnAlbumFindsItsTracksOutsideItsFolderAsTheyMoveTogether(t *testing.T) {
	a := measuredApp(t)
	// A cloud folder: the album in Albums, the mixes beside it in Mixes.
	cloud := t.TempDir()
	for _, d := range []string{"Albums", "Mixes"} {
		if err := os.Mkdir(filepath.Join(cloud, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for i := range a.Tracks {
		to := filepath.Join(cloud, "Mixes", filepath.Base(a.Tracks[i].File))
		if err := os.Rename(a.Tracks[i].File, to); err != nil {
			t.Fatal(err)
		}
		a.Tracks[i].File = to
	}
	a.Release = Release{Artist: "The Oscillators", Title: "Night Drive", Year: "2026"}
	album := filepath.Join(cloud, "Albums", "Night"+albumExt)
	a.saveAs(album)
	b, _ := os.ReadFile(album)
	if !strings.Contains(string(b), `"File": "../Mixes/a.wav"`) {
		t.Fatalf("the album keeps its tracks as %s", b)
	}
	// The cloud folder, found at another path on another computer.
	elsewhere := filepath.Join(t.TempDir(), "Cloud")
	if err := os.Rename(cloud, elsewhere); err != nil {
		t.Fatal(err)
	}
	c := newApp(a.ctx, newDeck(audio.NewMixer()), filepath.Join(elsewhere, "Albums", "Night"+albumExt))
	c.load()
	if c.Tracks[0].File != filepath.Join(elsewhere, "Mixes", "a.wav") || c.Release != a.Release {
		t.Fatalf("elsewhere, the first track is %q and the release %+v", c.Tracks[0].File, c.Release)
	}
	// The album alone, copied away from its mixes, finds them where they
	// were when it was saved.
	if err := os.Rename(elsewhere, cloud); err != nil {
		t.Fatal(err)
	}
	alone := filepath.Join(t.TempDir(), "Night"+albumExt)
	if err := os.WriteFile(alone, b, 0o644); err != nil {
		t.Fatal(err)
	}
	d := newApp(a.ctx, newDeck(audio.NewMixer()), alone)
	d.load()
	if d.Tracks[0].File != filepath.Join(cloud, "Mixes", "a.wav") {
		t.Fatalf("copied alone, the album's first track is %q", d.Tracks[0].File)
	}
}

func TestTheReleasesDialogSaysWhatWasWritten(t *testing.T) {
	r := Release{Artist: "The Oscillators", Title: "Night Drive", Year: "2026", Genre: "Synthwave"}
	if got := newReleaseDialog(r).OnAccept(); got != (SetRelease{Release: r}) {
		t.Fatalf("the dialog, saved as it opened, says %v", got)
	}
}

func TestAnExportRunsTracksSideBySideAndTagsThem(t *testing.T) {
	a := measuredApp(t)
	a.Release = Release{Artist: "The Oscillators", Title: "Night Drive", Year: "2026"}
	a.ExportDir = t.TempDir()
	start := time.Now()
	a.handle(Export{})
	running := 0
	settle(t, a, func() bool {
		n := 0
		for _, tr := range a.Tracks {
			if tr.Progress > 0 {
				n++
			}
		}
		running = max(running, n)
		return !a.Exporting
	})
	if runtime.NumCPU() >= 4 && running < 2 {
		t.Errorf("the tracks exported one at a time, %v in all", time.Since(start))
	}
	b, err := os.ReadFile(filepath.Join(a.ExportDir, "02 b.wav"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"INAM", "b\x00", "IART", "The Oscillators", "IPRD", "Night Drive", "ITRK", "2/3", "ICRD", "2026"} {
		if !bytes.Contains(b, []byte(want)) {
			t.Fatalf("the second track's file is not tagged %q", want)
		}
	}
}

func TestTheExportDialogSendsWhatIsTicked(t *testing.T) {
	d := ExportDraft{Dir: "/x", Bits: 24, Dither: true, WAV: true, MP3Rates: mp3Rates, MP3Rate: 256,
		Tracks: []ExportTrack{{ID: 1, Number: 1, Title: "One", Ticked: true}, {ID: 2, Number: 2, Title: "Two"}}}
	e := newExportDialog(d)
	got := e.OnAccept()
	want := StartExport{IDs: []int{1}, Bits: 24, Dither: true, WAV: true, MP3Rate: 256}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the dialog, as it opened, sends %+v, want %+v", got, want)
	}
	if e.Check() != "" {
		t.Fatalf("the dialog, as it opened, says %q", e.Check())
	}
	d.Tracks[0].Ticked = false
	if newExportDialog(d).Check() == "" {
		t.Fatal("with no track ticked the dialog exports")
	}
}

func TestACancelledExportLeavesNoFilesHalfWritten(t *testing.T) {
	a := measuredApp(t)
	a.ExportDir = t.TempDir()
	a.handle(Export{})
	a.handle(CancelExport{})
	settle(t, a, func() bool { return !a.Exporting })
	left, _ := os.ReadDir(a.ExportDir)
	if len(left) != 0 || !strings.Contains(a.Note, "cancelled") {
		t.Fatalf("cancelled, the export left %d files, and says %q", len(left), a.Note)
	}
	for _, tr := range a.Tracks {
		if tr.Progress != 0 {
			t.Fatalf("cancelled, %s shows progress %.2f", tr.Title, tr.Progress)
		}
	}
}

func TestATracksOwnSilenceIsMeasuredAndExported(t *testing.T) {
	a := measuredApp(t)
	before := a.Tracks[1].Measure.Length
	s := 2500 * time.Millisecond
	a.handle(SetSilence{ID: a.Tracks[1].ID, Silence: &s})
	a.handle(CalcLoudness{})
	settle(t, a, func() bool { return !a.Tracks[1].Measuring })
	if d := a.Tracks[1].Measure.Length - before; (d - 1500*time.Millisecond).Abs() > time.Millisecond {
		t.Fatalf("with 2.5 s of silence for the album's 1, the track grew %v", d)
	}
	if a.Tracks[0].Stale || a.Tracks[2].Stale {
		t.Fatal("a track's own silence marked the others changed")
	}
	a.handle(SetSilence{ID: a.Tracks[1].ID})
	if a.Tracks[1].Silence != nil || a.gapOf(&a.Tracks[1]) != a.Gap {
		t.Fatal("a silence reset is not the album's")
	}
}
