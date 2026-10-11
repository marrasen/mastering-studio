package plugui

import (
	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// Selector picks one of a few options, as a synth's panel does: a row
// of lamps, the chosen one lit, and under them the chosen option's
// name, or the name of the one under the pointer. A click on a lamp
// picks its option, and the wheel and the arrow keys step through them.
// Lamps light and dim gliding, the name turns over as it changes, and
// each lamp's tooltip says what its option does.
type Selector struct {
	anim.Group
	names, tips []string
	sel, hot    int
	// lit is how lit each lamp is, and shown the name shown, by option,
	// gliding as it changes.
	lit   []*anim.Float
	shown *anim.Float
	// OnPick picks option i, as the user chose it.
	OnPick func(i int, u *gunim.UI)
	// Locked greys the selector, and it picks nothing.
	Locked bool
	// Columns, where set, sets the lamps in rows of so many, as four
	// two by two, which takes less room across; else all in one row.
	Columns int
	tip     widget.PartTip
	cells   []geom.Rect
	size    geom.Size
}

// SelectorH is how tall a selector of one row is: a row of lamps, and
// the name
// under them.
const SelectorH = 34

// NewSelector returns a selector of the options names, each with its
// tooltip of tips, the first chosen.
func NewSelector(names, tips []string) *Selector {
	s := &Selector{names: names, tips: tips, hot: -1, shown: anim.NewFloat(0)}
	for i := range names {
		s.lit = append(s.lit, anim.NewFloat(float32(onOff(i == 0))))
		s.Add(s.lit[i])
	}
	s.Add(s.shown)
	return s
}

// Selected returns the option chosen.
func (s *Selector) Selected() int { return s.sel }

// SetSelected chooses option i, as set elsewhere, its lamp gliding lit.
func (s *Selector) SetSelected(i int, u *gunim.UI) {
	i = min(max(i, 0), len(s.names)-1)
	if i == s.sel {
		return
	}
	s.sel = i
	s.glide(u)
}

// glide moves each lamp, and the name, toward what is chosen and under
// the pointer.
func (s *Selector) glide(u *gunim.UI) {
	sp := widget.Quick.Get(u.Theme())
	for i, l := range s.lit {
		l.Animate(onOff(i == s.sel), sp)
	}
	show := s.sel
	if s.hot >= 0 {
		show = s.hot
	}
	s.shown.Animate(float32(show), sp)
	u.Invalidate()
}

// lampRow is how tall a row of lamps is.
const lampRow = 16

// grid returns how many lamps stand across, and how many rows they
// take.
func (s *Selector) grid() (cols, rows int) {
	cols = len(s.names)
	if s.Columns > 0 && s.Columns < cols {
		cols = s.Columns
	}
	return cols, (len(s.names) + cols - 1) / cols
}

// lampsH is how tall the lamps stand, all their rows.
func (s *Selector) lampsH() float32 {
	_, rows := s.grid()
	return float32(rows) * lampRow
}

// Layout implements [gunim.Node]: a lamp each, evenly across, in rows
// of Columns where it is set, and the name under them.
func (s *Selector) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	cols, _ := s.grid()
	s.size = c.Constrain(geom.Sz(max(float32(cols)*24, 48), s.lampsH()+SelectorH-lampRow))
	cw := s.size.W / float32(cols)
	s.cells = s.cells[:0]
	for i := range s.names {
		s.cells = append(s.cells, geom.Rc(float32(i%cols)*cw, float32(i/cols)*lampRow, cw, lampRow))
	}
	return s.size
}

// Paint implements [gunim.Node].
func (s *Selector) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	ink, accent := widget.Ink.Get(th), widget.Accent.Get(th)
	end := widget.Faint(p, box, onOff(s.Locked)*0.6)
	defer end()
	for i, r := range s.cells {
		lit := s.lit[i].Value()
		cx, cy := (r.Min.X+r.Max.X)/2, r.Min.Y+8
		if lit > 0.01 {
			p.ShadowRRect(geom.Rc(cx-5, cy-5, 10, 10), 5, paint.Solid(audioui.Faded(accent, 0.3*lit)),
				paint.Shadow{Blur: 8 * lit, Color: audioui.Faded(accent, 0.4*lit)})
		}
		lamp := audioui.Mix(audioui.Faded(ink, 0.16), accent, lit)
		if i == s.hot && i != s.sel {
			lamp = audioui.Faded(ink, 0.4)
		}
		p.RRect(geom.Rc(cx-3.5, cy-3.5, 7, 7), 3.5, paint.Solid(lamp))
	}
	// The name: the chosen option's, or the one under the pointer, the
	// words before sliding away as the new come.
	at := s.shown.Value()
	for i := range s.names {
		d := at - float32(i)
		if d <= -1 || d >= 1 {
			continue
		}
		alpha := (1 - abs32(d)) * 0.9
		if i != s.sel {
			alpha *= 0.7
		}
		t := audioui.Shaped(s.names[i], 10, i == s.sel, false)
		t.Paint(p, geom.Pt((box.W-t.Advance)/2-d*20, s.lampsH()+3), audioui.Faded(ink, alpha))
	}
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// at returns the option at pos, or -1: the lamp's, or, under the
// lamps, the one of the last row's above.
func (s *Selector) at(pos geom.Point) int {
	if pos.Y < 0 || pos.Y >= s.size.H {
		return -1
	}
	y := min(pos.Y, s.lampsH()-1)
	for i, r := range s.cells {
		if pos.X >= r.Min.X && pos.X < r.Max.X && y >= r.Min.Y && y < r.Max.Y {
			return i
		}
	}
	return -1
}

// pick picks option i, as the user chose it.
func (s *Selector) pick(i int, u *gunim.UI) {
	if s.Locked || i < 0 || i >= len(s.names) || i == s.sel {
		return
	}
	s.sel = i
	u.Cue(gunim.CueSelect, s)
	s.glide(u)
	if s.OnPick != nil {
		s.OnPick(i, u)
	}
}

// Focusable implements [gunim.Focusable].
func (s *Selector) Focusable() bool { return !s.Locked }

// Handle implements [gunim.Handler].
func (s *Selector) Handle(e input.Event, u *gunim.UI) bool {
	tip := ""
	if pe, ok := e.(input.PointerMove); ok {
		if i := s.at(pe.Pos); i >= 0 && i < len(s.tips) {
			tip = s.names[i] + ": " + s.tips[i]
		}
	}
	if _, ok := e.(input.PointerMove); ok || isLeave(e) {
		s.tip.Handle(e, u, s, tip)
	}
	switch e := e.(type) {
	case input.PointerMove:
		if h := s.at(e.Pos); h != s.hot {
			s.hot = h
			s.glide(u)
		}
	case input.PointerLeave:
		s.hot = -1
		s.glide(u)
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		s.tip.Hide(u)
		s.pick(s.at(e.Pos), u)
	case input.Scroll:
		step := 1
		if e.Delta.Y > 0 {
			step = -1
		}
		s.pick(min(max(s.sel+step, 0), len(s.names)-1), u)
	case input.KeyPress:
		switch e.Key {
		case input.KeyLeft, input.KeyUp:
			s.pick(s.sel-1, u)
		case input.KeyRight, input.KeyDown:
			s.pick(s.sel+1, u)
		default:
			return false
		}
	default:
		return false
	}
	return true
}

func isLeave(e input.Event) bool {
	_, ok := e.(input.PointerLeave)
	return ok
}

// Access implements [gunim.Accessible]: a list of tabs, one an
// option, as a segmented control reads.
func (s *Selector) Access() access.Info {
	info := access.Info{Role: access.RoleTabList, Value: s.names[s.sel]}
	for i, n := range s.names {
		part := access.Info{Role: access.RoleTab, Name: n, Actions: []string{access.ActionPress}}
		if i < len(s.tips) {
			part.Description = s.tips[i]
		}
		if i == s.sel {
			part.State = access.StateSelected
		}
		if i < len(s.cells) {
			part.Bounds = s.cells[i]
		}
		info.Parts = append(info.Parts, part)
	}
	return info
}

// AccessAct implements [gunim.AccessActor]: pressing an option picks
// it.
func (s *Selector) AccessAct(r access.Request, u *gunim.UI) bool {
	if r.Action != access.ActionPress || r.Part < 0 || r.Part >= len(s.names) {
		return false
	}
	s.pick(r.Part, u)
	return true
}
