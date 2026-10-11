package maximizer

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/mastering-studio/plug"
)

// stage shows n in a window of size, and returns it with a way to run
// frames.
func stage(t *testing.T, size geom.Size, n gunim.Node) (w *gunim.Window, run func(int)) {
	t.Helper()
	w = gunimtest.New(t, size, nil)
	gunim.RegisterView(w, "max", func(struct{}) gunim.Node { return n }, nil)
	if err := w.Client().Mount(gunim.Root, "max", "max", nil); err != nil {
		t.Fatal(err)
	}
	run = func(k int) {
		for range k {
			w.Frame(time.Second / 60)
		}
	}
	run(2)
	return w, run
}

// boundsIn returns where n is in w.
func boundsIn(t *testing.T, w *gunim.Window, run func(int), n gunim.Node) geom.Rect {
	t.Helper()
	var b geom.Rect
	gunim.RegisterPatch(w, "max", func(_ gunim.Node, _ struct{}, u *gunim.UI) { b, _ = u.Bounds(n) })
	if err := w.Client().Patch("max", struct{}{}); err != nil {
		t.Fatal(err)
	}
	run(1)
	return b
}

// click clicks the middle of n, or at, where given.
func click(t *testing.T, w *gunim.Window, run func(int), n gunim.Node, at ...geom.Point) {
	t.Helper()
	pt := boundsIn(t, w, run, n).Center()
	if len(at) > 0 {
		pt = at[0]
	}
	w.Input(input.PointerMove{Pos: pt})
	w.Input(input.PointerDown{Pos: pt, Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: pt, Button: input.ButtonPrimary})
	run(1)
}

func panelOf(t *testing.T, p *plugin, h *host) *panel {
	t.Helper()
	pn, ok := p.View(plug.Panel, h).(*panel)
	if !ok {
		t.Fatal("the panel is of another type")
	}
	return pn
}

func TestTheSoundFillsThePanelUnderItsReadoutsAndControls(t *testing.T) {
	p, h := newMaximizer(t)
	pn := panelOf(t, p, h)
	size := geom.Sz(1400, 820)
	w, run := stage(t, size, pn)
	hist, pane := boundsIn(t, w, run, pn.hist), boundsIn(t, w, run, pn.readouts)
	first, last := boundsIn(t, w, run, pn.sections[0]), boundsIn(t, w, run, pn.sections[len(pn.sections)-1])
	if hist != (geom.Rect{Max: size.Point()}) {
		t.Fatalf("the history is %v, want the whole panel", hist)
	}
	if first.Min.Y != last.Min.Y || math.Abs(float64((first.Min.X+last.Max.X)/2-size.W/2)) > 1 {
		t.Errorf("the sections run from %v to %v: not in a row, centred", first, last)
	}
	if pane.Max.Y > first.Min.Y || math.Abs(float64(pane.Center().X-size.W/2)) > 1 {
		t.Errorf("the readouts are at %v, over sections from %v: not centred above them", pane, first)
	}
}

func TestTheModesLampsSetTheModeAsAGesture(t *testing.T) {
	p, h := newMaximizer(t)
	pn := panelOf(t, p, h)
	w, run := stage(t, geom.Sz(1400, 820), pn)
	lamps := boundsIn(t, w, run, pn.mode)
	click(t, w, run, pn.mode, geom.Pt(lamps.Min.X+lamps.Size().W*2.5/3, lamps.Min.Y+8))
	if h.values[ModeID] != Loud || !slices.Equal(h.log, []string{"begin", "set", "end"}) {
		t.Errorf("Loud's lamp set the mode to %v, by %v", h.values[ModeID], h.log)
	}
	pn.Follow(ModeID, Punchy, nil)
	if pn.mode.Selected() != Punchy {
		t.Errorf("set elsewhere to Punchy, the lamps show %d", pn.mode.Selected())
	}
}

func TestLearnSetsTheGainAsOneStep(t *testing.T) {
	p, h := newMaximizer(t)
	pn := panelOf(t, p, h)
	w, run := stage(t, geom.Sz(1400, 820), pn)
	if !pn.learn.Disabled || pn.learn.Tooltip != "The host gives the maximizer no track to read" {
		t.Fatalf("with no track, Learn is disabled %v: %q", pn.learn.Disabled, pn.learn.Tooltip)
	}
	p.SetDocument(memDoc{quietMusic(4, 0.25)})
	pn.FollowDocument(nil)
	h.values[TargetID] = -10
	pn.Follow(TargetID, -10, nil)
	click(t, w, run, pn.learn)
	for range 1000 {
		run(1)
		if !pn.learning {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if pn.learning || h.values[GainID] <= 0 || !slices.Equal(h.log, []string{"begin", "set", "end"}) {
		t.Fatalf("learned, the gain is %v, by %v", h.values[GainID], h.log)
	}
	if pn.sections[1].Knobs[0].Value() != h.values[GainID] {
		t.Errorf("the gain's knob shows %v, the host has %v", pn.sections[1].Knobs[0].Value(), h.values[GainID])
	}
}

func TestTheChipFollowsTheGain(t *testing.T) {
	p, h := newMaximizer(t)
	h.values[GainID] = 4
	c, ok := p.View(plug.Chip, h).(*chip)
	if !ok || c.label.Text != "+4.0 dB" {
		t.Fatalf("the chip shows %q, want +4.0 dB", c.label.Text)
	}
	c.Follow(GainID, 6.5, nil)
	if c.label.Text != "+6.5 dB" {
		t.Errorf("the chip follows to %q", c.label.Text)
	}
}

func TestLearnStopsAtASecondClick(t *testing.T) {
	p, h := newMaximizer(t)
	pn := panelOf(t, p, h)
	w, run := stage(t, geom.Sz(1400, 820), pn)
	src := quietMusic(4, 0.25)
	src.gate = make(chan struct{})
	p.SetDocument(memDoc{src})
	pn.FollowDocument(nil)
	click(t, w, run, pn.learn)
	run(5)
	if !pn.learning || pn.learn.Label != "Stop learning · 0%" {
		t.Fatalf("learning, the button says %q", pn.learn.Label)
	}
	click(t, w, run, pn.learn)
	close(src.gate)
	run(5)
	if pn.learning || pn.learn.Label != learnWords || len(h.log) != 0 {
		t.Errorf("clicked again, it learns on %v, says %q, and sent %v", pn.learning, pn.learn.Label, h.log)
	}
	if _, learning, _, _ := p.learnState(); learning {
		t.Error("clicked again, the maximizer learns on")
	}
}
