package plugui

import (
	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
	"github.com/marrasen/mastering-studio/plug"
)

// Section is a part of a plugin's controls, on a card of glass: its
// name; a switch, where it can be switched off; its knobs, greyed while
// it is off; and after them any other controls it has, as a selector or
// a button.
type Section struct {
	Name   string
	On     uint32
	Switch *widget.Switch
	Knobs  []*Knob
	extras []extra
	size   geom.Size
}

// extra is a control of a section's other than its knobs, and how wide
// it stands.
type extra struct {
	n gunim.Node
	w float32
}

// The sections' look: the room round and between their controls, the
// height of the name over them, and the room between sections.
const (
	SectionPad  = 8
	SectionHead = 26
	SectionGap  = 10
	SectionH    = SectionHead + KnobH + SectionPad
)

// NewSection returns the section name of d's parameters ids, as knobs
// working them through h; on, where not 0, is the parameter its switch
// works.
func NewSection(h plug.Host, d *plug.Descriptor, name string, on uint32, ids ...uint32) *Section {
	s := &Section{Name: name, On: on}
	for _, id := range ids {
		p, _ := d.Param(id)
		s.Knobs = append(s.Knobs, NewKnob(h, p))
	}
	if on != 0 {
		s.Switch = widget.NewSwitch("")
		s.Switch.SetChecked(h.Param(on) > 0.5, nil)
		s.Switch.OnChange = func(v bool, u *gunim.UI) gunim.Intent {
			h.Begin(on, u)
			h.Set(on, float64(onOff(v)), u)
			h.End(on, u)
			s.dim(v)
			return nil
		}
		s.dim(h.Param(on) > 0.5)
	}
	return s
}

// Add puts n after the section's knobs, w wide.
func (s *Section) Add(n gunim.Node, w float32) *Section {
	s.extras = append(s.extras, extra{n: n, w: w})
	return s
}

// dim greys the knobs while the section is off.
func (s *Section) dim(on bool) {
	for _, k := range s.Knobs {
		k.Disabled = !on
	}
}

// Width is how wide the section is.
func (s *Section) Width() float32 {
	w := float32(len(s.Knobs))*KnobW + 2*SectionPad
	for _, e := range s.extras {
		w += e.w + SectionPad
	}
	return w
}

// Follow takes a parameter set elsewhere, where it is the section's.
func (s *Section) Follow(id uint32, v float64, u *gunim.UI) {
	if id == s.On && s.Switch != nil {
		s.Switch.SetChecked(v > 0.5, u)
		s.dim(v > 0.5)
		u.Invalidate()
	}
	for _, k := range s.Knobs {
		if k.Param().ID == id {
			k.Follow(v, u)
		}
	}
}

// Children implements [gunim.Composite].
func (s *Section) Children() []gunim.Node {
	kids := make([]gunim.Node, 0, len(s.Knobs)+len(s.extras)+1)
	for _, k := range s.Knobs {
		kids = append(kids, k)
	}
	for _, e := range s.extras {
		kids = append(kids, e.n)
	}
	if s.Switch != nil {
		kids = append(kids, s.Switch)
	}
	return kids
}

// Layout implements [gunim.Node]: the knobs in a row under the name,
// the other controls after them, each in the middle of the knobs'
// height, and the switch at the top right.
func (s *Section) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	s.size = c.Constrain(geom.Sz(s.Width(), SectionH))
	x := float32(SectionPad)
	for i := range s.Knobs {
		kids.At(i).Layout(gunim.Tight(geom.Sz(KnobW, KnobH)))
		kids.At(i).Place(geom.Pt(x, SectionHead))
		x += KnobW
	}
	for i, e := range s.extras {
		x += SectionPad
		k := kids.At(len(s.Knobs) + i)
		sz := k.Layout(gunim.Constraints{Min: geom.Sz(e.w, 0), Max: geom.Sz(e.w, KnobH)})
		k.Place(geom.Pt(x, SectionHead+(KnobH-sz.H)/2))
		x += e.w
	}
	if s.Switch != nil {
		k := kids.At(kids.Len() - 1)
		sz := k.Layout(gunim.Constraints{Max: geom.Sz(60, 22)})
		k.Place(geom.Pt(s.size.W-SectionPad-sz.W, (SectionHead-sz.H)/2+2))
	}
	return s.size
}

// Paint implements [gunim.Node].
func (s *Section) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	ink := widget.Ink.Get(th)
	PaintGlass(p, th, geom.Rect{Max: box.Point()}, widget.CardRadius.Get(th))
	name := audioui.Shaped(s.Name, 10, true, false)
	name.Paint(p, geom.Pt(SectionPad, (SectionHead-name.Height())/2+2), audioui.Faded(ink, 0.55))
	for k := range kids.All {
		k.Paint(p)
	}
}

// PlaceSections places sections along the foot of a view size big, in
// from its edges by inset: in rows as many as fit, each row centred,
// reaching as near the edges as half the inset. It tells place where
// each goes, and returns where the first row's top is.
func PlaceSections(size geom.Size, inset float32, sections []*Section, place func(i int, r geom.Rect)) float32 {
	type row struct {
		from, to int
		w        float32
	}
	var rows []row
	cur := row{}
	for i, s := range sections {
		w := s.Width()
		if cur.to > cur.from && cur.w+SectionGap+w > size.W-inset {
			rows = append(rows, cur)
			cur = row{from: i}
		}
		if cur.to > cur.from {
			cur.w += SectionGap
		}
		cur.w += w
		cur.to = i + 1
	}
	rows = append(rows, cur)
	top := size.H - inset - (float32(len(rows))*(SectionH+SectionGap) - SectionGap)
	y := top
	for _, r := range rows {
		x := (size.W - r.w) / 2
		for i := r.from; i < r.to; i++ {
			w := sections[i].Width()
			place(i, geom.Rc(x, y, w, SectionH))
			x += w + SectionGap
		}
		y += SectionH + SectionGap
	}
	return top
}
