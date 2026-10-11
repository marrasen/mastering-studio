// Package plugui holds controls a Go plugin's views share, each working
// a parameter through the [plug.Host], so every turn is undone as the
// host undoes it.
package plugui

import (
	"image/color"
	"math"
	"strconv"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
	"github.com/marrasen/mastering-studio/plug"
)

// The knob's colours: the cap, from its top to its bottom, and the
// groove its arc runs in. The arc is the theme's accent.
var (
	KnobCapTop    = theme.Color("plug.knob.cap.top", color.NRGBA{R: 0x44, G: 0x4a, B: 0x5c, A: 0xff})
	KnobCapBottom = theme.Color("plug.knob.cap.bottom", color.NRGBA{R: 0x22, G: 0x26, B: 0x31, A: 0xff})
	KnobGroove    = theme.Color("plug.knob.groove", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x16})
)

// KnobW and KnobH are how big a knob is: its cap, its name and its
// value under it.
const (
	KnobW = 54
	KnobH = 76
)

// Knob turns a parameter, as a synth's knob does: a drag up turns it
// up, ten times finer with Shift; the wheel and the arrow keys step it;
// a double click sets it back to its default. Each turn is a gesture
// of the host's, one step to undo. A parameter that runs either side
// of zero fills its arc from zero.
type Knob struct {
	h     plug.Host
	p     plug.Param
	v     float64
	held  bool
	from  geom.Point
	start float64
	// Disabled greys the knob, as for a section switched off, though it
	// still turns.
	Disabled bool
}

// NewKnob returns a knob for p, at its value in h.
func NewKnob(h plug.Host, p plug.Param) *Knob {
	return &Knob{h: h, p: p, v: h.Param(p.ID)}
}

// Value returns the value the knob shows.
func (k *Knob) Value() float64 { return k.v }

// Param returns the knob's parameter.
func (k *Knob) Param() plug.Param { return k.p }

// Follow shows v, set elsewhere, unless the knob is held.
func (k *Knob) Follow(v float64, u *gunim.UI) {
	if !k.held && v != k.v {
		k.v = v
		u.Invalidate()
	}
}

// Layout implements [gunim.Node].
func (k *Knob) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return c.Constrain(geom.Sz(KnobW, KnobH))
}

// Paint implements [gunim.Node].
func (k *Knob) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	ink, accent := widget.Ink.Get(th), widget.Accent.Get(th)
	if k.Disabled {
		accent = audioui.Faded(ink, 0.35)
	}
	r := float32(17)
	c := geom.Pt(box.W/2, 4+r)
	// The turn runs three quarters round, clockwise from bottom left.
	const from, sweep = 0.75 * math.Pi, 1.5 * math.Pi
	arc := func(t0, t1 float64, w float32, col color.NRGBA) {
		if t1 < t0 {
			t0, t1 = t1, t0
		}
		n := max(int((t1-t0)*32), 1)
		for i := range n {
			a0 := from + sweep*(t0+(t1-t0)*float64(i)/float64(n))
			a1 := from + sweep*(t0+(t1-t0)*float64(i+1)/float64(n))
			audioui.Segment(p, geom.Pt(c.X+r*float32(math.Cos(a0)), c.Y+r*float32(math.Sin(a0))),
				geom.Pt(c.X+r*float32(math.Cos(a1)), c.Y+r*float32(math.Sin(a1))), w, col)
		}
	}
	arc(0, 1, 3.5, KnobGroove.Get(th))
	t := k.p.Normal(k.v)
	zero := 0.0
	if k.p.Min < 0 && k.p.Max > 0 {
		zero = k.p.Normal(0)
	}
	if k.held {
		arc(zero, t, 5, audioui.Faded(accent, 0.25))
	}
	arc(zero, t, 3.5, accent)
	p.ShadowRRect(geom.Rc(c.X-r+6, c.Y-r+6, 2*r-12, 2*r-12), r-6, paint.Fill{Gradient: &paint.Gradient{
		From: geom.Pt(c.X, c.Y-r), To: geom.Pt(c.X, c.Y+r), Start: KnobCapTop.Get(th), End: KnobCapBottom.Get(th)}},
		paint.Shadow{Offset: geom.Pt(0, 2), Blur: 4, Color: color.NRGBA{A: 0x90}})
	a := from + sweep*t
	audioui.Segment(p, geom.Pt(c.X+(r-13)*float32(math.Cos(a)), c.Y+(r-13)*float32(math.Sin(a))),
		geom.Pt(c.X+(r-7)*float32(math.Cos(a)), c.Y+(r-7)*float32(math.Sin(a))), 2, ink)
	name := audioui.Shaped(k.p.Name, 10.5, false, false)
	name.Paint(p, geom.Pt((box.W-name.Advance)/2, c.Y+r+4), audioui.Faded(ink, 0.6))
	val := audioui.Shaped(k.p.Text(k.v), 10.5, false, true)
	col := audioui.Faded(ink, 0.9)
	if k.held {
		col = accent
	}
	val.Paint(p, geom.Pt((box.W-val.Advance)/2, c.Y+r+4+name.Height()), col)
}

// turn sets the knob to v as one move of the gesture held.
func (k *Knob) turn(v float64, u *gunim.UI) {
	v = k.p.Clamp(v)
	if v == k.v {
		return
	}
	k.v = v
	k.h.Set(k.p.ID, v, u)
	u.Invalidate()
}

// step sets the knob to v as a gesture of its own.
func (k *Knob) step(v float64, u *gunim.UI) {
	if v = k.p.Clamp(v); v == k.v {
		return
	}
	k.h.Begin(k.p.ID, u)
	k.turn(v, u)
	k.h.End(k.p.ID, u)
}

// stepSize is a step of the wheel or an arrow key, along Normal.
func (k *Knob) stepSize() float64 {
	if k.p.Steps > 0 {
		return 1 / float64(k.p.Steps)
	}
	return 0.01
}

// Focusable implements [gunim.Focusable].
func (k *Knob) Focusable() bool { return true }

// DragsTouch says a finger drags the knob rather than scrolling.
func (k *Knob) DragsTouch() bool { return true }

// Handle implements [gunim.Handler].
func (k *Knob) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		if e.Clicks == 2 {
			k.step(k.p.Default, u)
			return true
		}
		k.held, k.from, k.start = true, e.Pos, k.p.Normal(k.v)
		k.h.Begin(k.p.ID, u)
		u.Invalidate()
	case input.PointerMove:
		if !k.held {
			return false
		}
		px := float32(180)
		if e.Mods.Has(input.ModShift) {
			px *= 10
		}
		k.turn(k.p.Plain(k.start+float64((k.from.Y-e.Pos.Y)/px)), u)
	case input.PointerUp:
		if !k.held {
			return false
		}
		k.held = false
		k.h.End(k.p.ID, u)
		u.Invalidate()
	case input.Scroll:
		d := k.stepSize()
		if e.Delta.Y > 0 {
			d = -d
		}
		k.step(k.p.Plain(k.p.Normal(k.v)+d), u)
	case input.KeyPress:
		d := k.stepSize()
		if e.Mods.Has(input.ModShift) {
			d *= 10
		}
		switch e.Key {
		case input.KeyUp, input.KeyRight:
		case input.KeyDown, input.KeyLeft:
			d = -d
		case input.KeyHome:
			d = -1
		case input.KeyEnd:
			d = 1
		default:
			return false
		}
		k.step(k.p.Plain(k.p.Normal(k.v)+d), u)
	default:
		return false
	}
	return true
}

// Access implements [gunim.Accessible]: a slider, named as its
// parameter, its value as the parameter writes it.
func (k *Knob) Access() access.Info {
	st := access.State(0)
	if k.Disabled {
		st = access.StateDisabled
	}
	step := 0.0
	if k.p.Steps > 0 {
		step = (k.p.Max - k.p.Min) / float64(k.p.Steps)
	}
	return access.Info{Role: access.RoleSlider, Name: k.p.Name, Value: k.p.Text(k.v), State: st,
		Range: &access.Range{Min: k.p.Min, Max: k.p.Max, Value: k.v, Step: step}}
}

// AccessAct implements [gunim.AccessActor]: a value set, as one gesture.
func (k *Knob) AccessAct(r access.Request, u *gunim.UI) bool {
	if !r.SetValue {
		return false
	}
	k.step(r.Value, u)
	return true
}

// String returns the knob's value, written, for tests and logs.
func (k *Knob) String() string { return k.p.Name + " " + strconv.FormatFloat(k.v, 'g', 4, 64) }
