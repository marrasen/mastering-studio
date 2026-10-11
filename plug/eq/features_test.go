package eq

import (
	"errors"
	"math"
	"runtime"
	"slices"
	"testing"
	"time"

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

func TestAutoAsksForTheTracksSpectrumAndShapesTheBands(t *testing.T) {
	p, h := newEQ(t)
	p.setBands(bell)
	v, ok := p.View(plug.Full, h).(*full)
	if !ok {
		t.Fatal("the full view is of another type")
	}
	v.auto.OnClick(nil)
	if !slices.Equal(h.asked, autoFreqs) || !v.auto.Disabled {
		t.Fatalf("Auto asked for the spectrum at %v, and is disabled %v", h.asked, v.auto.Disabled)
	}
	// Twice: one listen at a time.
	h.asked = nil
	v.auto.OnClick(nil)
	if h.asked != nil {
		t.Error("Auto asked again while listening")
	}
	// A track with 6 dB too much about 1 kHz.
	levels := levelsOf(func(f float64) float64 { o := math.Log2(f / 1000); return 6 * math.Exp(-o*o) })
	h.log = nil
	h.answer(levels, nil, nil)
	if got := audio.Response(p.Bands(), 1000); got > -3 {
		t.Errorf("for a track loud about 1 kHz, Auto lowers it by %+.2f dB", got)
	}
	if want := []string{"edit Auto EQ", "changed", "end edit"}; !slices.Equal(h.log, want) {
		t.Errorf("Auto sent %v, want one step", h.log)
	}
	if v.auto.Disabled || v.note.Text != "Matched to pink noise" {
		t.Errorf("done, Auto is disabled %v, and says %q", v.auto.Disabled, v.note.Text)
	}
	// A track the host could not measure changes nothing.
	was := p.Bands()
	v.auto.OnClick(nil)
	h.answer(nil, errors.New("the track is silent"), nil)
	if !slices.Equal(p.Bands(), was) || v.note.Text != "Could not listen: the track is silent" {
		t.Errorf("unmeasured, the bands are %v, and Auto says %q", p.Bands(), v.note.Text)
	}
}
