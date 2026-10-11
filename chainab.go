package main

import (
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// chainAB switches the track between its two chains, A and B, to
// compare them: two small parts, the one played lit, the light gliding
// to the other as it is picked. Each side keeps its own plugins and
// their settings; B starts empty, and the Chain menu copies one side to
// the other.
type chainAB struct {
	anim.Group
	r *root
	// onB says chain B is played; track is the track shown; at is where
	// the light is, from 0 at A to 1 at B, gliding; hot the side under
	// the pointer, or -1.
	onB   bool
	track int
	at    *anim.Float
	hot   int
	tip   widget.PartTip
	size  geom.Size
}

// The switch's size: each side's width, and its height.
const (
	abSideW   = 24
	abSwitchH = 24
	abSwitchW = 2 * abSideW
)

func newChainAB(r *root) *chainAB {
	s := &chainAB{r: r, at: anim.NewFloat(0), hot: -1}
	s.Add(s.at)
	return s
}

// show shows track t's side.
func (s *chainAB) show(t Track) {
	s.track, s.onB = t.ID, t.OnB
	if to := onOff(t.OnB); s.at.Target() != to {
		s.at.Animate(to, anim.Spring{Response: 0.25, Damping: 0.85})
	}
}

// Step implements [gunim.Animator].
func (s *chainAB) Step(dt time.Duration) bool { return s.Group.Step(dt) }

// Layout implements [gunim.Node].
func (s *chainAB) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	s.size = c.Constrain(geom.Sz(abSwitchW, abSwitchH))
	return s.size
}

// Paint implements [gunim.Node].
func (s *chainAB) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	pal := colours(f.Theme)
	whole := geom.Rect{Max: box.Point()}
	p.RRect(whole, abSwitchH/2, paint.Solid(faded(pal.ink, 0.06)))
	pal.outline(p, whole, abSwitchH/2, 1)
	if s.hot >= 0 && s.hot != int(onOff(s.onB)) {
		p.RRect(geom.Rc(float32(s.hot)*abSideW+2, 2, abSideW-4, abSwitchH-4), abSwitchH/2-2, paint.Solid(faded(pal.ink, 0.08)))
	}
	at := min(max(s.at.Value(), 0), 1)
	p.RRect(geom.Rc(at*abSideW+2, 2, abSideW-4, abSwitchH-4), abSwitchH/2-2, paint.Solid(faded(pal.teal, 0.9)))
	for i, name := range []string{"A", "B"} {
		on := 1 - min(abs32(at-float32(i)), 1)
		run := shaped(name, 11, true)
		run.Paint(p, geom.Pt(float32(i)*abSideW+(abSideW-run.Advance)/2, (box.H-14)/2), mix(pal.quiet(0.7), pal.night, on))
	}
}

// sideAt is the side at pos, or -1.
func (s *chainAB) sideAt(pos geom.Point) int {
	if pos.X < 0 || pos.Y < 0 || pos.X >= s.size.W || pos.Y >= s.size.H {
		return -1
	}
	if pos.X < abSideW {
		return 0
	}
	return 1
}

// pick plays side b's chain.
func (s *chainAB) pick(b bool, u *gunim.UI) {
	if s.track == 0 || b == s.onB {
		return
	}
	u.Cue(gunim.CueSelect, s)
	s.onB = b
	s.at.Animate(onOff(b), anim.Spring{Response: 0.25, Damping: 0.85})
	u.Send(s, SwitchChain{Track: s.track, B: b})
	u.Invalidate()
}

// tipOf is side i's tooltip.
func (s *chainAB) tipOf(i int) string {
	switch i {
	case 0:
		return "Play chain A. A and B each keep their own plugins and settings, to compare"
	case 1:
		return "Play chain B, with plugins of its own: empty until you add some. The Chain menu copies one side to the other"
	}
	return ""
}

// Focusable implements [gunim.Focusable].
func (s *chainAB) Focusable() bool { return true }

// Handle implements [gunim.Handler]: a click picks a side, and the arrow
// keys step between them.
func (s *chainAB) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		i := s.sideAt(e.Pos)
		s.tip.Handle(e, u, s, s.tipOf(i))
		if i != s.hot {
			s.hot = i
			u.Invalidate()
		}
	case input.PointerLeave:
		s.tip.Handle(e, u, s, "")
		s.hot = -1
		u.Invalidate()
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		s.tip.Hide(u)
		if i := s.sideAt(e.Pos); i >= 0 {
			s.pick(i == 1, u)
		}
	case input.KeyPress:
		switch e.Key {
		case input.KeyLeft:
			s.pick(false, u)
		case input.KeyRight:
			s.pick(true, u)
		default:
			return false
		}
	default:
		return false
	}
	return true
}

// Access implements [gunim.Accessible]: a list of two tabs, the side
// played picked.
func (s *chainAB) Access() access.Info {
	info := access.Info{Role: access.RoleTabList, Name: "Chain A or B"}
	for i, n := range []string{"Chain A", "Chain B"} {
		part := access.Info{Role: access.RoleTab, Name: n, Description: s.tipOf(i),
			Bounds: geom.Rc(float32(i)*abSideW, 0, abSideW, abSwitchH), Actions: []string{access.ActionPress}}
		if (i == 1) == s.onB {
			part.State = access.StateSelected
		}
		info.Parts = append(info.Parts, part)
	}
	return info
}

// AccessAct implements [gunim.AccessActor]: pressing a side picks it.
func (s *chainAB) AccessAct(r access.Request, u *gunim.UI) bool {
	if r.Action != access.ActionPress || r.Part < 0 || r.Part > 1 {
		return false
	}
	s.pick(r.Part == 1, u)
	return true
}
