package dynamics

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
	"github.com/marrasen/mastering-studio/plug/plugui"
)

// reduction reads how far the compressor and limiter turn the sound
// heard down, together, in decibels, below zero: from what the plugin
// kept for the very frames heard, so it moves with the sound. Between
// the chunks the sound comes in it holds, and once the sound stops it
// reads nothing.
type reduction struct {
	h    plug.Host
	l    plug.Listener
	last float32
}

func newReduction(h plug.Host) *reduction {
	_, out := h.Sound()
	return &reduction{h: h, l: plug.Listener{Sound: out}}
}

// read reads it, a frame of the window, dt, on, and whether the sound
// plays.
func (r *reduction) read(dt time.Duration) (db float32, live bool) {
	frames, _, playing := r.l.Hear(4096, dt)
	switch {
	case len(frames) > 0:
		end := r.l.At()
		r.last = 0
		for f := end - int64(len(frames)/2); f < end; f += plug.TraceFrames {
			c, _ := r.h.Traced(CompGRID, f)
			l, _ := r.h.Traced(LimitGRID, f)
			r.last = min(r.last, c+l)
		}
	case !playing:
		r.last = 0
	}
	return r.last, playing
}

// vuOf reads the level of a sound in VU, 0 VU at ref() dBFS: the
// average of the frames heard since the last chunk, as a VU meter
// rectifies, held between chunks, and whether the sound plays.
func vuOf(s plug.Sound, ref func() float64) func(time.Duration) (float32, bool) {
	l := plug.Listener{Sound: s}
	last := float32(vuRest)
	return func(dt time.Duration) (float32, bool) {
		frames, _, playing := l.Hear(8192, dt)
		switch {
		case len(frames) > 0:
			var sum float64
			for _, v := range frames {
				sum += math.Abs(float64(v))
			}
			// A sine's average is 0.9 of its RMS, by which a VU meter
			// reads it.
			avg := sum / float64(len(frames)) * math.Pi / (2 * math.Sqrt2)
			last = float32(dbOf(avg) - ref())
		case !playing:
			last = vuRest
		}
		return last, playing
	}
}

// chip shows, in a plugin's chip in a chain, the compressor's ratio,
// or the limiter's ceiling where the compressor is off, and a bar of
// how far the two turn the sound down, growing from the right.
type chip struct {
	label  *widget.Label
	values map[uint32]float64
	r      *reduction
	shown  *anim.Float
	group  anim.Group
}

func newChip(h plug.Host) *chip {
	c := &chip{label: widget.NewLabel(""), values: map[uint32]float64{}, r: newReduction(h), shown: anim.NewFloat(0)}
	c.label.NoWrap, c.label.MaxLines = true, 1
	for _, id := range []uint32{CompOnID, CompRatioID, LimitOnID, LimitCeilingID} {
		c.values[id] = h.Param(id)
	}
	c.label.Text = c.text()
	c.group.Add(c.shown)
	return c
}

// text is what the chip says.
func (c *chip) text() string {
	switch {
	case c.values[CompOnID] > 0.5:
		return param(CompRatioID).Text(c.values[CompRatioID])
	case c.values[LimitOnID] > 0.5:
		return param(LimitCeilingID).Text(c.values[LimitCeilingID])
	}
	return "Off"
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
		p.RRect(geom.Rc(x+chipBar-w, y, w, h), h/2, paint.Solid(HistoryCut.Get(th)))
	}
}

// Step implements [gunim.Animator]: the bar follows the reduction.
func (c *chip) Step(dt time.Duration) bool {
	db, live := c.r.read(dt)
	c.shown.Animate(db, anim.Snappy)
	return c.group.Step(dt) || live
}

// Access implements [gunim.Accessible]: the chip reads as its text.
func (c *chip) Access() access.Info { return c.label.Access() }

// Follow implements [plug.Follower].
func (c *chip) Follow(id uint32, v float64, u *gunim.UI) {
	if _, ok := c.values[id]; ok {
		c.values[id] = v
		c.label.Text = c.text()
		u.Invalidate()
	}
}

// section is a part of the plugin: its name, a switch where it can be
// switched off, and its knobs, greyed while it is off.
type section struct {
	name  string
	on    uint32
	sw    *widget.Switch
	knobs []*plugui.Knob
	size  geom.Size
}

// The sections' look: the room round and between the knobs, and the
// height of the name over them.
const (
	sectionPad  = 8
	sectionHead = 26
	sectionGap  = 10
)

func newSection(h plug.Host, name string, on uint32, ids ...uint32) *section {
	s := &section{name: name, on: on}
	for _, id := range ids {
		s.knobs = append(s.knobs, plugui.NewKnob(h, param(id)))
	}
	if on != 0 {
		s.sw = widget.NewSwitch("")
		s.sw.SetChecked(h.Param(on) > 0.5, nil)
		s.sw.OnChange = func(v bool, u *gunim.UI) gunim.Intent {
			h.Begin(on, u)
			h.Set(on, onOffValue(v), u)
			h.End(on, u)
			s.dim(v)
			return nil
		}
		s.dim(h.Param(on) > 0.5)
	}
	return s
}

func onOffValue(on bool) float64 {
	if on {
		return 1
	}
	return 0
}

// dim greys the knobs while the section is off.
func (s *section) dim(on bool) {
	for _, k := range s.knobs {
		k.Disabled = !on
	}
}

// width is how wide the section is.
func (s *section) width() float32 {
	return float32(len(s.knobs))*plugui.KnobW + 2*sectionPad
}

// follow takes a parameter set elsewhere, where it is the section's.
func (s *section) follow(id uint32, v float64, u *gunim.UI) {
	if id == s.on && s.sw != nil {
		s.sw.SetChecked(v > 0.5, u)
		s.dim(v > 0.5)
		u.Invalidate()
	}
	for _, k := range s.knobs {
		if k.Param().ID == id {
			k.Follow(v, u)
		}
	}
}

// Children implements [gunim.Composite].
func (s *section) Children() []gunim.Node {
	kids := make([]gunim.Node, 0, len(s.knobs)+1)
	for _, k := range s.knobs {
		kids = append(kids, k)
	}
	if s.sw != nil {
		kids = append(kids, s.sw)
	}
	return kids
}

// Layout implements [gunim.Node].
func (s *section) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	s.size = c.Constrain(geom.Sz(s.width(), sectionHead+plugui.KnobH+sectionPad))
	for i := range s.knobs {
		kids.At(i).Layout(gunim.Tight(geom.Sz(plugui.KnobW, plugui.KnobH)))
		kids.At(i).Place(geom.Pt(sectionPad+float32(i)*plugui.KnobW, sectionHead))
	}
	if s.sw != nil {
		sz := kids.At(len(s.knobs)).Layout(gunim.Constraints{Max: geom.Sz(60, 22)})
		kids.At(len(s.knobs)).Place(geom.Pt(s.size.W-sectionPad-sz.W, (sectionHead-sz.H)/2+2))
	}
	return s.size
}

// Paint implements [gunim.Node].
func (s *section) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	ink := widget.Ink.Get(th)
	r := geom.Rect{Max: box.Point()}
	p.RRect(r, widget.CardRadius.Get(th), paint.Solid(audioui.Faded(audioui.Ground.Get(th), 0.6)))
	name := audioui.Shaped(s.name, 10, true, false)
	name.Paint(p, geom.Pt(sectionPad, (sectionHead-name.Height())/2+2), audioui.Faded(ink, 0.55))
	for k := range kids.All {
		k.Paint(p)
	}
}

// panel is the history of the sound, beside two VU meters, of how far
// the compressor and limiter turn the sound down and of the sound that
// comes out, over the sections' knobs.
type panel struct {
	hist     *history
	gr, out  *vu
	sections []*section
	ref      float64
}

func newPanel(h plug.Host) *panel {
	r := newReduction(h)
	_, out := h.Sound()
	p := &panel{hist: newHistory(h), ref: h.Param(MeterID)}
	p.gr = newVU("GAIN REDUCTION", r.read)
	p.out = newVU("OUTPUT", vuOf(out, func() float64 { return p.ref }))
	p.sections = []*section{
		newSection(h, "INPUT", 0, InputID),
		newSection(h, "GATE", GateOnID, GateThresholdID, GateRangeID, GateAttackID, GateReleaseID),
		newSection(h, "COMPRESSOR", CompOnID, CompThresholdID, CompRatioID, CompKneeID, CompAttackID, CompReleaseID, CompMakeupID),
		newSection(h, "LIMITER", LimitOnID, LimitCeilingID, LimitReleaseID),
		newSection(h, "OUTPUT", 0, OutputID, MeterID),
	}
	return p
}

// Children implements [gunim.Composite].
func (p *panel) Children() []gunim.Node {
	kids := make([]gunim.Node, 0, 3+len(p.sections))
	kids = append(kids, p.hist, p.gr, p.out)
	for _, s := range p.sections {
		kids = append(kids, s)
	}
	return kids
}

// Layout implements [gunim.Node]: the history along the top, the
// meters stacked at its right; the sections under them, in rows as
// many as fit, each row centred.
func (p *panel) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	// The sections, in rows.
	type row struct {
		from, to int
		w        float32
	}
	var rows []row
	cur := row{}
	for i, s := range p.sections {
		w := s.width()
		if cur.to > cur.from && cur.w+sectionGap+w > size.W {
			rows = append(rows, cur)
			cur = row{from: i}
		}
		if cur.to > cur.from {
			cur.w += sectionGap
		}
		cur.w += w
		cur.to = i + 1
	}
	rows = append(rows, cur)
	sectionH := float32(sectionHead + plugui.KnobH + sectionPad)
	knobsH := float32(len(rows))*(sectionH+sectionGap) - sectionGap
	top := max(size.H-knobsH-sectionGap, 120)
	// The meters, as a meter's face is, wider than tall.
	mh := min((top-sectionGap)/2, 150)
	mw := mh / 0.62
	kids.At(0).Layout(gunim.Tight(geom.Sz(max(size.W-mw-sectionGap, 0), top)))
	kids.At(0).Place(geom.Point{})
	for i := range 2 {
		kids.At(1 + i).Layout(gunim.Tight(geom.Sz(mw, mh)))
		kids.At(1 + i).Place(geom.Pt(size.W-mw, float32(i)*(mh+sectionGap)))
	}
	y := top + sectionGap
	for _, r := range rows {
		x := (size.W - r.w) / 2
		for i := r.from; i < r.to; i++ {
			s := kids.At(3 + i).Layout(gunim.Tight(geom.Sz(p.sections[i].width(), sectionH)))
			kids.At(3 + i).Place(geom.Pt(x, y))
			x += s.W + sectionGap
		}
		y += sectionH + sectionGap
	}
	return size
}

// Paint implements [gunim.Node].
func (p *panel) Paint(pt *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(pt)
	}
}

// Follow implements [plug.Follower]: the knobs and switches glide to
// their parameters set elsewhere, unless held, and the history's lines
// move.
func (p *panel) Follow(id uint32, v float64, u *gunim.UI) {
	for _, s := range p.sections {
		s.follow(id, v, u)
	}
	p.hist.follow(id, v)
	if id == MeterID {
		p.ref = v
	}
	u.Invalidate()
}
