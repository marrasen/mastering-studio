package limiter

import (
	"math"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
	"github.com/marrasen/mastering-studio/plug"
)

// reduction reads how far the limiter turns the sound down, in
// decibels, below zero: nothing while no sound plays, as a meter of it
// rests.
type reduction struct {
	h plug.Host
	l plug.Listener
}

func newReduction(h plug.Host) *reduction {
	_, out := h.Sound()
	return &reduction{h: h, l: plug.Listener{Sound: out}}
}

// read reads it, and whether the sound moves.
func (r *reduction) read() (db float32, live bool) {
	if frames, _ := r.l.Listen(4096); len(frames) == 0 {
		return 0, false
	}
	return float32(r.h.Param(ReductionID)), true
}

// vuOf reads the level of a sound in VU, 0 VU at -18 dBFS, as studios
// calibrate: the average of the frames heard since the last read, as a
// VU meter rectifies, and whether the sound moves.
func vuOf(s plug.Sound) func(time.Duration) (float32, bool) {
	l := plug.Listener{Sound: s}
	return func(time.Duration) (float32, bool) {
		frames, _ := l.Listen(8192)
		if len(frames) == 0 {
			return vuLow - 2, false
		}
		var sum float64
		for _, v := range frames {
			sum += math.Abs(float64(v))
		}
		// A sine's average is 0.9 of its RMS, by which a VU meter reads it.
		avg := sum / float64(len(frames)) * math.Pi / (2 * math.Sqrt2)
		return float32(20*math.Log10(max(avg, 1e-9)) + 18), true
	}
}

// chip shows the ceiling, in a plugin's chip in a chain, and a bar of
// how far the limiter turns the sound down, growing from the right.
type chip struct {
	label  *widget.Label
	r      *reduction
	shown  *anim.Float
	shownG anim.Group
}

func newChip(h plug.Host) *chip {
	l := widget.NewLabel(Descriptor.Params[1].Text(h.Param(CeilingID)))
	l.NoWrap, l.MaxLines = true, 1
	c := &chip{label: l, r: newReduction(h), shown: anim.NewFloat(0)}
	c.shownG.Add(c.shown)
	return c
}

// chipBar is how wide the chip's bar is, and how many decibels fill it.
const (
	chipBar   = 40
	chipBarDB = 12
)

// Children implements [gunim.Composite].
func (c *chip) Children() []gunim.Node { return []gunim.Node{c.label} }

// Layout implements [gunim.Node].
func (c *chip) Layout(cs gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kids.At(0).Layout(gunim.Constraints{Max: geom.Sz(max(cs.Max.W-chipBar-6, 0), cs.Max.H)})
	kids.At(0).Place(geom.Point{})
	return cs.Max
}

// Paint implements [gunim.Node].
func (c *chip) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
	th := f.Theme
	x, h := box.W-chipBar, float32(5)
	y := (box.H - h) / 2
	p.RRect(geom.Rc(x, y, chipBar, h), h/2, paint.Solid(audioui.Faded(audioui.Ground.Get(th), 0.85)))
	if w := chipBar * min(-c.shown.Value()/chipBarDB, 1); w > 0.5 {
		p.RRect(geom.Rc(x+chipBar-w, y, w, h), h/2, paint.Solid(VURed.Get(th)))
	}
}

// Step implements [gunim.Animator]: the bar follows the reduction.
func (c *chip) Step(dt time.Duration) bool {
	db, live := c.r.read()
	c.shown.Animate(db, anim.Snappy)
	return c.shownG.Step(dt) || live
}

// Access implements [gunim.Accessible]: the chip reads as the ceiling.
func (c *chip) Access() access.Info { return c.label.Access() }

// Follow implements [plug.Follower].
func (c *chip) Follow(id uint32, v float64, u *gunim.UI) {
	if id == CeilingID {
		c.label.Text = Descriptor.Params[1].Text(v)
		u.Invalidate()
	}
}

// panel is two VU meters, of the reduction and of the sound that comes
// out, over a slider for each control.
type panel struct {
	gr, out *vu
	rows    []*widget.SliderRow
	ids     []uint32
}

func newPanel(h plug.Host) *panel {
	r := newReduction(h)
	_, out := h.Sound()
	p := &panel{
		gr:  newVU("GAIN REDUCTION", func(time.Duration) (float32, bool) { return r.read() }),
		out: newVU("OUTPUT", vuOf(out)),
	}
	for _, id := range []uint32{DriveID, CeilingID, ReleaseID} {
		p.rows = append(p.rows, newRow(h, id))
		p.ids = append(p.ids, id)
	}
	return p
}

// newRow returns a slider row of parameter id, each drag a gesture.
func newRow(h plug.Host, id uint32) *widget.SliderRow {
	prm, _ := Descriptor.Param(id)
	s := widget.NewSlider(float32(prm.Min), float32(prm.Max))
	s.Rest, s.HasRest = float32(prm.Default), true
	s.SetValue(float32(h.Param(id)), nil)
	row := widget.NewSliderRow(prm.Name, s)
	row.Format = func(v float32) string { return prm.Text(float64(v)) }
	held := false
	s.OnChange = func(v float32, u *gunim.UI) gunim.Intent {
		if !held {
			h.Begin(id, u)
			held = true
		}
		h.Set(id, float64(v), u)
		return nil
	}
	s.OnCommit = func(v float32, u *gunim.UI) gunim.Intent {
		if !held {
			h.Begin(id, u)
		}
		h.Set(id, float64(v), u)
		h.End(id, u)
		held = false
		return nil
	}
	return row
}

// Children implements [gunim.Composite].
func (p *panel) Children() []gunim.Node {
	kids := make([]gunim.Node, 0, 2+len(p.rows))
	kids = append(kids, p.gr, p.out)
	for _, r := range p.rows {
		kids = append(kids, r)
	}
	return kids
}

// Layout implements [gunim.Node]: the meters side by side along the
// top, each as a meter's face is, wider than tall; the sliders under
// them.
func (p *panel) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	const gap = 24
	w := min((size.W-gap)/2, 340)
	h := min(w*0.62, max(size.H-3*48, 80))
	w = h / 0.62
	x := (size.W - (2*w + gap)) / 2
	for i := range 2 {
		kids.At(i).Layout(gunim.Tight(geom.Sz(w, h)))
		kids.At(i).Place(geom.Pt(x+float32(i)*(w+gap), 0))
	}
	y := h + gap
	rw := min(size.W, 520)
	for i := range p.rows {
		rs := kids.At(2 + i).Layout(gunim.Constraints{Max: geom.Sz(rw, size.H)})
		kids.At(2 + i).Place(geom.Pt((size.W-rw)/2, y))
		y += rs.H + 6
	}
	return size
}

// Paint implements [gunim.Node].
func (p *panel) Paint(pt *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(pt)
	}
}

// Follow implements [plug.Follower]: a slider glides to its parameter
// set elsewhere, unless it is held.
func (p *panel) Follow(id uint32, v float64, u *gunim.UI) {
	for i, rid := range p.ids {
		if rid == id && !p.rows[i].Slider.Held() {
			p.rows[i].Slider.SetValue(float32(v), u)
		}
	}
}
