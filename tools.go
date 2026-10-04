package main

import (
	"fmt"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// editTools are the track's edit as values, under the editor: the fade
// in, its length and its curve; the cut's start, with a button that
// cuts to where the sound starts; its end, likewise; the fade out; and
// the gain. Each drags, and the curves step through their shapes.
type editTools struct {
	r                 *root
	fadeIn, fadeOut   *valueChip
	inCurve, outCurve *curveChip
	start, end, gain  *valueChip
	toSound, endSound *pill
	scanned           bool
}

func newEditTools(r *root) *editTools {
	t := &editTools{r: r}
	ed := func() Edit { return r.editor.edit }
	send := func(e Edit, u *gunim.UI) { r.editor.send(e, u) }
	sec := func(v float64) time.Duration { return time.Duration(v * float64(time.Second)) }
	t.fadeIn = newValueChip("FADE IN", func(v float64) string { return fmt.Sprintf("%.2f s", v) }, 0.005, 0.05, 0, 60, 0,
		func(v float64, u *gunim.UI) {
			e := ed()
			e.FadeIn.Length = sec(v)
			send(e, u)
		})
	t.fadeOut = newValueChip("FADE OUT", func(v float64) string { return fmt.Sprintf("%.2f s", v) }, 0.02, 0.25, 0, 120, 0,
		func(v float64, u *gunim.UI) {
			e := ed()
			e.FadeOut.Length = sec(v)
			send(e, u)
		})
	t.start = newValueChip("START", func(v float64) string { return clock(sec(v)) }, 0.002, 0.01, 0, 3600, 0,
		func(v float64, u *gunim.UI) {
			e := ed()
			e.Start = sec(v)
			send(e, u)
		})
	t.end = newValueChip("END", func(v float64) string { return clock(sec(v)) }, 0.01, 0.1, 0, 3600, 0,
		func(v float64, u *gunim.UI) {
			e := ed()
			e.End = sec(v)
			send(e, u)
		})
	t.gain = newValueChip("GAIN", func(v float64) string { return fmt.Sprintf("%+.1f dB", v) }, 0.02, 0.1, -24, 24, 0,
		func(v float64, u *gunim.UI) {
			e := ed()
			e.Gain = float32(v)
			send(e, u)
		})
	t.inCurve = newCurveChip(true, func(c Curve, u *gunim.UI) {
		e := ed()
		e.FadeIn.Curve = c
		send(e, u)
	})
	t.outCurve = newCurveChip(false, func(c Curve, u *gunim.UI) {
		e := ed()
		e.FadeOut.Curve = c
		send(e, u)
	})
	// Cut to where the sound starts, a hair before it, with a short
	// fade in, so a cut into the sound never clicks.
	t.toSound = newPill("Trim to sound", func(u *gunim.UI) {
		tr, ok := r.track()
		if !ok || !tr.Scanned {
			return
		}
		e := ed()
		e.Start = max(0, tr.SoundStart-10*time.Millisecond)
		e.FadeIn.Length = max(e.FadeIn.Length, 15*time.Millisecond)
		send(e, u)
	})
	t.endSound = newPill("End at sound", func(u *gunim.UI) {
		tr, ok := r.track()
		if !ok || !tr.Scanned {
			return
		}
		e := ed()
		e.End = min(duration(tr.Frames, tr.Format.SampleRate), tr.SoundEnd+50*time.Millisecond)
		send(e, u)
	})
	return t
}

func (t *editTools) show(tr Track) {
	e := t.r.editor.edit
	t.fadeIn.value, t.fadeOut.value = e.FadeIn.Length.Seconds(), e.FadeOut.Length.Seconds()
	t.start.value, t.gain.value = e.Start.Seconds(), float64(e.Gain)
	end := e.End
	if end == 0 && tr.Format.SampleRate > 0 {
		end = duration(tr.Frames, tr.Format.SampleRate)
	}
	t.end.value = end.Seconds()
	t.end.reset = duration(tr.Frames, max(tr.Format.SampleRate, 1)).Seconds()
	t.inCurve.show(e.FadeIn.Curve)
	t.outCurve.show(e.FadeOut.Curve)
	t.scanned = tr.Scanned
}

// Children implements [gunim.Composite].
func (t *editTools) Children() []gunim.Node {
	return []gunim.Node{t.fadeIn, t.inCurve, t.start, t.toSound, t.end, t.endSound, t.fadeOut, t.outCurve, t.gain}
}

// Layout implements [gunim.Node]: in a row, as the track's time runs:
// the fade in, the start, the end, the fade out, the gain.
func (t *editTools) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	widths := []float32{92, 44, 96, pillWidth("Trim to sound"), 96, pillWidth("End at sound"), 92, 44, 92}
	var total float32
	for _, w := range widths {
		total += w + 8
	}
	scale := min(1, size.W/total)
	x := float32(0)
	for i, w := range widths {
		w *= scale
		h := float32(44)
		y := (size.H - h) / 2
		if i == 3 || i == 5 {
			h, y = 32, (size.H-32)/2
		}
		kids.At(i).Layout(gunim.Tight(geom.Sz(w, h)))
		kids.At(i).Place(geom.Pt(x, y))
		x += w + 8*scale
	}
	return size
}

// Paint implements [gunim.Node]: the values, faint until the track is
// read.
func (t *editTools) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	alpha := float32(1)
	if !t.scanned {
		alpha = 0.35
	}
	end := p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: alpha})
	defer end()
	for k := range kids.All {
		k.Paint(p)
	}
}

// curveChip is a fade's curve, drawn as its shape; a click steps it on
// to the next, the drawing turning into the new shape.
type curveChip struct {
	anim.Group
	rising     bool
	curve, was Curve
	turn       *anim.Float
	hover      *anim.Float
	pick       func(Curve, *gunim.UI)
	size       geom.Size
}

func newCurveChip(rising bool, pick func(Curve, *gunim.UI)) *curveChip {
	c := &curveChip{rising: rising, pick: pick, turn: anim.NewFloat(1), hover: anim.NewFloat(0)}
	c.Add(c.turn, c.hover)
	return c
}

func (c *curveChip) show(cv Curve) {
	if cv != c.curve {
		c.was, c.curve = c.curve, cv
		c.turn.Jump(0)
		c.turn.Animate(1, anim.Spring{Response: 0.35, Damping: 0.9})
	}
}

// Focusable implements [gunim.Focusable].
func (c *curveChip) Focusable() bool { return false }

// Handle implements [gunim.Handler].
func (c *curveChip) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerEnter:
		c.hover.Animate(1, anim.Snappy)
	case input.PointerLeave:
		c.hover.Animate(0, anim.Gentle)
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		next := (c.curve + 1) % Curve(len(curveNames))
		if e.Mods.Has(input.ModShift) {
			next = (c.curve + Curve(len(curveNames)) - 1) % Curve(len(curveNames))
		}
		u.Cue(gunim.CueTick, c)
		c.pick(next, u)
	default:
		return false
	}
	u.Invalidate()
	return true
}

// Layout implements [gunim.Node].
func (c *curveChip) Layout(cs gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	c.size = cs.Max
	return cs.Max
}

// Paint implements [gunim.Node]: the curve, rising or falling, and its
// name under it.
func (c *curveChip) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, _ gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 10, paint.Solid(faded(ink, 0.05+0.06*c.hover.Value())))
	area := geom.Rc(8, 6, box.W-16, box.H-22)
	m := float64(c.turn.Value())
	var prev geom.Point
	for i := range 17 {
		u := float64(i) / 16
		x := u
		if !c.rising {
			u = 1 - u
		}
		v := c.was.at(u)*(1-m) + c.curve.at(u)*m
		pt := geom.Pt(area.Min.X+area.Size().W*float32(x), area.Max.Y-area.Size().H*float32(v))
		if i > 0 {
			segment(p, prev, pt, 1.6, amber)
		}
		prev = pt
	}
	run := shaped(curveNames[c.curve], 8, true)
	run.Paint(p, geom.Pt((box.W-run.Advance)/2, box.H-14), faded(ink, 0.6))
}
