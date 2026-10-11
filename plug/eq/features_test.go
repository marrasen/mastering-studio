package eq

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/mastering-studio/plug"
)

// loudBell is a dynamic bell at 1 kHz, flat until its band rises over
// -20 dB, then turning down at 4:1, 12 dB at most.
var loudBell = audio.Band{ID: 7, Kind: audio.Bell, Freq: 1000, Q: 1, On: true,
	Dynamic: audio.Dynamics{On: true, Threshold: -20, Ratio: 4, Attack: 1, Release: 50, Range: 12}}

func TestADynamicBandIsKeptAndABadOneRefused(t *testing.T) {
	p, _ := newEQ(t)
	p.setBands([]audio.Band{loudBell, bell[0]})
	q, _ := newEQ(t)
	if err := q.SetState(p.State()); err != nil {
		t.Fatal(err)
	}
	if got := q.Bands(); !slices.Equal(got, p.Bands()) {
		t.Fatalf("the bands come back as %+v, want %+v", got, p.Bands())
	}
	for name, d := range map[string]audio.Dynamics{
		"threshold": {On: true, Threshold: 6, Ratio: 2, Attack: 1, Release: 50, Range: 6},
		"ratio":     {On: true, Threshold: -20, Ratio: 0.5, Attack: 1, Release: 50, Range: 6},
		"attack":    {On: true, Threshold: -20, Ratio: 2, Attack: 0, Release: 50, Range: 6},
		"range":     {On: true, Threshold: -20, Ratio: 2, Attack: 1, Release: 50, Range: 40},
	} {
		b := loudBell
		b.Dynamic = d
		p.setBands([]audio.Band{b})
		if err := q.SetState(p.State()); err == nil {
			t.Errorf("a band with a bad %s was taken", name)
		}
	}
}

// through plays a second of a sine at hz and amp through p, and
// returns how much it lifts the last quarter, in decibels.
func through(t *testing.T, p *plugin, hz, amp float64, changes []plug.Change) float64 {
	t.Helper()
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 480, Channels: 2})
	var in, out []float32
	for at := 0; at < 48000; at += 480 {
		ch := [][]float32{sine(480, at, hz, 48000), sine(480, at, hz, 48000)}
		for i := range ch {
			for k := range ch[i] {
				ch[i][k] *= float32(amp / 0.5)
			}
		}
		in = append(in, ch[0]...)
		pr.Process(&plug.Block{In: ch, Out: ch, Frames: 480, Changes: changes, Position: plug.Position{Frame: int64(at)}})
		changes = nil
		out = append(out, ch[0]...)
	}
	return rms(out[36000:]) - rms(in[36000:])
}

func TestTheAmountScalesTheBands(t *testing.T) {
	for _, c := range []struct{ amount, want float64 }{{100, 6}, {50, 3}, {0, 0}, {200, 12}} {
		p, _ := newEQ(t)
		p.setBands(bell)
		if got := through(t, p, 1000, 0.1, []plug.Change{{ID: AmountID, Value: c.amount}}); math.Abs(got-c.want) > 0.2 {
			t.Errorf("at %v%% a bell of +6 dB lifts %+.2f dB, want %+.1f", c.amount, got, c.want)
		}
	}
}

func TestADynamicBandTurnsDownAndTheViewsReadHowFar(t *testing.T) {
	p, h := newEQ(t)
	p.setBands([]audio.Band{loudBell})
	h.proc = &p.proc
	// A loud sine far from the bell passes as it was: the bell does not
	// hear it.
	if got := through(t, p, 100, 0.5, nil); math.Abs(got) > 0.2 {
		t.Errorf("a loud sine at 100 Hz through a dynamic bell at 1 kHz comes out %+.2f dB", got)
	}
	// A sine at -6 dB, 14 dB over: turned down 10.5 dB.
	if got := through(t, p, 1000, 0.5, nil); math.Abs(got+10.5) > 0.5 {
		t.Errorf("a loud sine through a dynamic bell comes out %+.2f dB, want -10.5", got)
	}
	if v, ok := h.Traced(bandTrace+uint32(loudBell.ID), 47000); !ok || math.Abs(float64(v)+10.5) > 0.5 {
		t.Errorf("the views read the bell turned down %v dB (%v), want -10.5", v, ok)
	}
	if _, ok := h.Traced(bandTrace+99, 47000); ok {
		t.Error("the views read a band the EQ has not")
	}
	// The graph draws the curve as it plays.
	g := newGraph(p, h)
	g.spec.playing, g.spec.at = true, 47001
	d := g.dot(p.Bands()[0], nil)
	g.followDown()
	for range 60 {
		g.Group.Step(time.Second / 60)
	}
	if got := d.playing(1).Response(1000); math.Abs(got+10.5) > 0.5 {
		t.Errorf("the graph draws the bell at %+.2f dB at 1 kHz, want -10.5", got)
	}
}

func TestProcessingDynamicBandsAllocatesNothing(t *testing.T) {
	p, _ := newEQ(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
	ch := [][]float32{sine(512, 0, 1000, 48000), sine(512, 0, 1000, 48000)}
	steps := [][]audio.Band{{loudBell}, {loudBell, bell[0]}, {bell[0]}, nil}
	if raceOn {
		t.Skip("the race detector allocates of its own")
	}
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	var made uint64
	for i := range 4 * len(steps) {
		if i%4 == 0 {
			setBands(t, p, steps[i/4])
		}
		b := &plug.Block{In: ch, Out: ch, Frames: 512, Position: plug.Position{Frame: int64(512 * i)},
			Changes: []plug.Change{{ID: AmountID, Value: float64(50 * (i % 4))}}}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		pr.Process(b)
		runtime.ReadMemStats(&after)
		made += after.Mallocs - before.Mallocs
	}
	if made != 0 {
		t.Errorf("processing made %d things over %d blocks", made, 4*len(steps))
	}
}

func TestTheStripMakesABandDynamicAndMovesItsDynamics(t *testing.T) {
	p, h := newEQ(t)
	p.setBands(bell)
	v, ok := p.View(plug.Full, h).(*full)
	if !ok {
		t.Fatal("the full view is of another type")
	}
	w, run := stage(t, geom.Sz(1200, 600), v)
	g, s := v.panel.graph, v.strip
	g.sel = 1
	run(2)
	stripAt := func(slot int) geom.Point {
		var b geom.Rect
		for _, r := range []geom.Rect{s.slotOf[slot]} {
			b = r
		}
		return b.Center().Add(geom.Pt(0, g.size.H+16))
	}
	before := s.height(800, nil)
	at := stripAt(slotDynamic)
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	run(2)
	if b := p.Bands()[0]; b.Dynamic != dynDefaults {
		t.Fatalf("the dynamics button left the band's dynamics %+v", b.Dynamic)
	}
	if s.height(800, nil) <= before {
		t.Error("made dynamic, the strip shows no row more")
	}
	// The threshold, dragged up 40 pixels: 5 dB up, as one gesture.
	h.log = nil
	at = stripAt(slotThreshold)
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerMove{Pos: at.Add(geom.Pt(0, -20))})
	w.Input(input.PointerMove{Pos: at.Add(geom.Pt(0, -40))})
	w.Input(input.PointerUp{Pos: at.Add(geom.Pt(0, -40)), Button: input.ButtonPrimary})
	run(2)
	if got := p.Bands()[0].Dynamic.Threshold; got != dynDefaults.Threshold+5 {
		t.Errorf("dragged up 40 pixels, the threshold is %v, want %v", got, dynDefaults.Threshold+5)
	}
	if want := []string{"edit Band 1 dynamics", "changed", "changed", "end edit"}; !slices.Equal(h.log, want) {
		t.Errorf("the drag sent %v, want %v", h.log, want)
	}
	// Static again, it keeps its dynamics for the next time.
	g.toggleDynamic(1, nil)
	if b := p.Bands()[0]; b.Dynamic.On || b.Dynamic.Threshold != dynDefaults.Threshold+5 {
		t.Errorf("made static, the band's dynamics are %+v", b.Dynamic)
	}
}

// memSource is a track in memory, of version, as a host gives a plugin.
type memSource struct {
	frames  [][]float32
	version int
	// gate, where set, holds each read until it is closed.
	gate chan struct{}
}

func (m *memSource) ID() int       { return 1 }
func (m *memSource) Version() int  { return m.version }
func (m *memSource) Name() string  { return "noise" }
func (m *memSource) Rate() float64 { return 48000 }
func (m *memSource) Channels() int { return 2 }
func (m *memSource) Frames() int64 { return int64(len(m.frames[0])) }
func (m *memSource) ReadAt(dst [][]float32, at int64) (int, error) {
	if m.gate != nil {
		<-m.gate
	}
	n := 0
	for c := range dst {
		n = copy(dst[c], m.frames[c%2][min(at, m.Frames()):])
	}
	return n, nil
}

type memDoc struct{ src *memSource }

func (d memDoc) Sources() []plug.Source { return []plug.Source{d.src} }
func (d memDoc) Regions() []plug.Region { return []plug.Region{{Source: 1, Length: d.src.Frames()}} }

// noise returns seconds of white noise at 48 kHz, as a source.
func noise(seconds, version int) *memSource {
	r := rand.New(rand.NewPCG(3, 4))
	m := &memSource{frames: [][]float32{make([]float32, seconds*48000), make([]float32, seconds*48000)}, version: version}
	for c := range m.frames {
		for i := range m.frames[c] {
			m.frames[c][i] = float32(0.2 * (2*r.Float64() - 1))
		}
	}
	return m
}

func TestTheEQReadsATracksSpectrum(t *testing.T) {
	var at float64
	levels, err := spectrumOf(context.Background(), noise(6, 1), autoFreqs, func(p float64) { at = p })
	if err != nil {
		t.Fatal(err)
	}
	// White noise's power rises 3 dB an octave, sixth-octave bands
	// summing it: a line through the levels.
	var sx, sy, sxx, sxy float64
	for i, f := range autoFreqs {
		x := math.Log2(f / 100)
		sx, sy, sxx, sxy = sx+x, sy+levels[i], sxx+x*x, sxy+x*levels[i]
	}
	n := float64(len(autoFreqs))
	if slope := (n*sxy - sx*sy) / (n*sxx - sx*sx); math.Abs(slope-3) > 0.15 {
		t.Errorf("white noise rises %.2f dB an octave, want 3", slope)
	}
	if at < 0.95 {
		t.Errorf("read whole, the reading says it got %.2f of the way", at)
	}
	silent := &memSource{frames: [][]float32{make([]float32, 48000), make([]float32, 48000)}}
	if _, err := spectrumOf(context.Background(), silent, autoFreqs, func(float64) {}); !errors.Is(err, errSilent) {
		t.Errorf("a silent track reads with %v", err)
	}
}

func TestAReadingStopsAndANewTrackIsReadAnew(t *testing.T) {
	p, _ := newEQ(t)
	if p.canRead() {
		t.Fatal("with no document the EQ says it can read")
	}
	src := noise(2, 1)
	src.gate = make(chan struct{})
	p.SetDocument(memDoc{src})
	p.startReading()
	if _, reading, _, _ := p.readState(); !reading {
		t.Fatal("started, the EQ is not reading")
	}
	p.stopReading()
	close(src.gate)
	// Long enough for the reading to have run through, had it not
	// stopped.
	time.Sleep(200 * time.Millisecond)
	if levels, reading, _, _ := p.readState(); reading || levels != nil {
		t.Fatal("stopped, the EQ reads on, or kept what it read")
	}
	// Read through, started again at once, the reading stopped ending
	// meanwhile: it leaves the new one be.
	src.gate = make(chan struct{})
	p.SetDocument(memDoc{src})
	p.startReading()
	p.stopReading()
	p.startReading()
	close(src.gate)
	waitRead(t, p)
	if levels, _, _, err := p.readState(); len(levels) != len(autoFreqs) || p.Analysed() != 1 || err != nil {
		t.Fatalf("read, the EQ has %d levels, says %.2f, and failed with %v", len(levels), p.Analysed(), err)
	}
	// A new version of the track: what was read of the old goes.
	p.SetDocument(memDoc{noise(2, 2)})
	if levels, _, _, _ := p.readState(); levels != nil || p.Analysed() != 0 {
		t.Error("a new version of the track keeps what was read of the old")
	}
}

// waitRead waits for p to finish reading.
func waitRead(t *testing.T, p *plugin) {
	t.Helper()
	for range 1000 {
		if _, reading, _, _ := p.readState(); !reading {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the EQ never finished reading")
}

func TestAutoReadsTheTrackAndShapesTheBands(t *testing.T) {
	p, h := newEQ(t)
	p.setBands(bell)
	// White noise, brighter than pink: Auto darkens it.
	p.SetDocument(memDoc{noise(4, 1)})
	v, ok := p.View(plug.Full, h).(*full)
	if !ok {
		t.Fatal("the full view is of another type")
	}
	w, run := stage(t, geom.Sz(1200, 600), v)
	h.log = nil
	clickOn(t, w, run, v.auto)
	for range 600 {
		run(1)
		if !v.listening {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if v.listening || v.note.Text != "Matched to pink noise" {
		t.Fatalf("Auto still reads %v, and says %q", v.listening, v.note.Text)
	}
	if low, high := audio.Response(p.Bands(), 100), audio.Response(p.Bands(), 8000); high > low-6 {
		t.Errorf("for white noise, Auto sets 100 Hz to %+.1f dB and 8 kHz to %+.1f dB: not darker", low, high)
	}
	if want := []string{"edit Auto EQ", "changed", "end edit"}; !slices.Equal(h.log, want) {
		t.Errorf("Auto sent %v, want one step", h.log)
	}
}

// clickOn clicks the middle of node n in w.
func clickOn(t *testing.T, w *gunim.Window, run func(int), n gunim.Node) {
	t.Helper()
	var b geom.Rect
	gunim.RegisterPatch(w, "eq", func(_ gunim.Node, _ struct{}, u *gunim.UI) { b, _ = u.Bounds(n) })
	if err := w.Client().Patch("eq", struct{}{}); err != nil {
		t.Fatal(err)
	}
	run(1)
	at := b.Center()
	w.Input(input.PointerMove{Pos: at})
	w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary})
	run(1)
}
