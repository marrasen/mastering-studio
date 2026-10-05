package main

import (
	"image/color"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// The sidebar's parts beside the album's tracks: the A/B bar at its
// top, and the references' heading over their list at its foot.
const (
	abH      = 98
	refHeadH = 40
)

// abBar is the two sessions of listening, A and B, a card each: its
// track, and whether it plays and where. The one heard is lit; a click
// on the other switches to it. Under them, a switch plays the session
// not heard on in the background.
type abBar struct {
	anim.Group
	r *root
	// hover lights each card as the pointer comes over it, and lit the
	// one heard.
	hover, lit [2]*anim.Float
	background *pill
	size       geom.Size
}

func newABBar(r *root) *abBar {
	b := &abBar{r: r}
	for i := range 2 {
		b.hover[i], b.lit[i] = anim.NewFloat(0), anim.NewFloat(onOff(i == 0))
		b.Add(b.hover[i], b.lit[i])
	}
	b.background = newPill("Play on in background", func(u *gunim.UI) {
		u.Send(r, SetBackground{On: !r.state.Background})
	})
	return b
}

func (b *abBar) show(s Album) {
	for i := range 2 {
		b.lit[i].Animate(onOff(Side(i) == s.Side), anim.Snappy)
	}
	b.background.setLit(s.Background)
}

// sideColor is the colour of session A, or B.
func sideColor(s Side) color.NRGBA {
	if s == SideA {
		return teal
	}
	return sky
}

// card is where session s's card is.
func (b *abBar) card(s Side) geom.Rect {
	w := (b.size.W - 28) / 2
	return geom.Rc(10+float32(s)*(w+8), 8, w, 56)
}

// Children implements [gunim.Composite].
func (b *abBar) Children() []gunim.Node { return []gunim.Node{b.background} }

// Layout implements [gunim.Node].
func (b *abBar) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	b.size = c.Max
	w := pillWidth(b.background.words)
	kids.At(0).Layout(gunim.Tight(geom.Sz(w, 24)))
	kids.At(0).Place(geom.Pt(c.Max.W-10-w, 68))
	return c.Max
}

// Step implements [gunim.Animator]: the times run on while either
// session plays.
func (b *abBar) Step(dt time.Duration) bool {
	moving := b.Group.Step(dt)
	s := b.r.state
	return moving || s.Playing || (s.Background && s.Away.Playing)
}

// Focusable implements [gunim.Focusable].
func (b *abBar) Focusable() bool { return false }

// Handle implements [gunim.Handler]: a click on the session not heard
// switches to it.
func (b *abBar) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		for i := range 2 {
			b.hover[i].Animate(onOff(b.card(Side(i)).Contains(e.Pos)), anim.Snappy)
		}
	case input.PointerLeave:
		for i := range 2 {
			b.hover[i].Animate(0, anim.Gentle)
		}
	case input.PointerDown:
		other := 1 - b.r.state.Side
		if e.Button != input.ButtonPrimary || !b.card(other).Contains(e.Pos) {
			return false
		}
		u.Cue(gunim.CueSelect, b)
		u.Send(b, SwitchSide{})
	default:
		return false
	}
	u.Invalidate()
	return true
}

// Paint implements [gunim.Node].
func (b *abBar) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	s := b.r.state
	for i := range 2 {
		b.paintCard(p, f, Side(i), s)
	}
	shaped("LISTEN A / B", 10, true).Paint(p, geom.Pt(12, 73), faded(teal, 0.85))
	for k := range kids.All {
		k.Paint(p)
	}
	p.RRect(geom.Rc(10, box.H-1, box.W-20, 1), 0, paint.Solid(faded(ink, 0.06)))
}

// paintCard draws session side's card: its letter, its track, and
// whether it plays, and where.
func (b *abBar) paintCard(p *paint.Painter, f gunim.Frame, side Side, s Album) {
	r := b.card(side)
	c := sideColor(side)
	lit, hover := b.lit[side].Value(), b.hover[side].Value()
	p.RRect(r, 12, paint.Solid(faded(mix(raised, c, 0.16*lit), 0.6+0.4*lit+0.1*hover)))
	if lit > 0.01 {
		p.RRectStroke(r, 12, paint.Solid(color.NRGBA{}), paint.Stroke{Width: 1.5, Color: faded(c, 0.85*lit)})
	}
	// The letter, in a ring filled while heard.
	ring := geom.Rc(r.Min.X+8, r.Min.Y+(r.Size().H-26)/2, 26, 26)
	p.RRect(ring, 13, paint.Solid(faded(c, 0.15+0.85*lit)))
	letter := shaped([]string{"A", "B"}[side], 13, true)
	letter.Paint(p, geom.Pt(ring.Min.X+(26-letter.Advance)/2, ring.Min.Y+5), mix(c, night, lit))
	// Where the session is: the deck's own place for the one heard.
	var at time.Duration
	var id int
	var playing bool
	if side == s.Side {
		id, playing = s.Current, s.Playing
		if pos, _, pid := b.r.d.position(); pid == id {
			at = pos
		}
	} else {
		w := s.awayAt(time.Now())
		id, at, playing = w.Current, w.At, w.Playing
	}
	x := ring.Max.X + 8
	room := r.Max.X - x - 8
	t := s.find(id)
	if t == nil {
		paintFit(p, "No track yet", 12, true, geom.Pt(x, r.Min.Y+10), room, faded(ink, 0.45))
		hint := "Starts on this track"
		if len(s.References) > 0 {
			hint = "Starts on the first reference"
		}
		paintFit(p, hint, 10, false, geom.Pt(x, r.Min.Y+30), room, faded(ink, 0.35))
		return
	}
	title := t.Title
	if s.isRef(id) {
		title = "Ref · " + title
	}
	paintFit(p, title, 12, true, geom.Pt(x, r.Min.Y+10), room, faded(ink, 0.6+0.4*lit))
	// Playing, waiting to play on, or paused; and where.
	words, ic, sc := "Paused", icon.Pause, faded(ink, 0.5)
	switch {
	case playing && (side == s.Side || s.Background):
		words, ic, sc = "Playing", icon.Play, c
	case playing:
		words, ic, sc = "Waits", icon.Play, faded(c, 0.6)
	}
	widget.PaintIcon(p, f.Theme, ic, geom.Rc(x, r.Min.Y+31, 11, 11), sc)
	shapedFace(words+" · "+short(at), 10, false).Paint(p, geom.Pt(x+15, r.Min.Y+30), sc)
}

// refHead is the references' heading, over their list, with the button
// that adds to them.
type refHead struct {
	r   *root
	add *pill
}

func newRefHead(r *root) *refHead {
	return &refHead{r: r, add: newPill("+ Add", func(u *gunim.UI) { u.Send(r, ChooseReferences{}) })}
}

// Children implements [gunim.Composite].
func (h *refHead) Children() []gunim.Node { return []gunim.Node{h.add} }

// Layout implements [gunim.Node].
func (h *refHead) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	w := pillWidth(h.add.words)
	kids.At(0).Layout(gunim.Tight(geom.Sz(w, 26)))
	kids.At(0).Place(geom.Pt(c.Max.W-10-w, (c.Max.H-26)/2))
	return c.Max
}

// Paint implements [gunim.Node].
func (h *refHead) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rc(10, 0, box.W-20, 1), 0, paint.Solid(faded(ink, 0.08)))
	shaped("REFERENCES", 10, true).Paint(p, geom.Pt(12, (box.H-12)/2), faded(sky, 0.85))
	for k := range kids.All {
		k.Paint(p)
	}
}

// refsListH is how tall the references' list is: up to three rows, and
// room to drop the first.
func refsListH(n int) float32 {
	if n == 0 {
		return 64
	}
	return float32(min(n, 3))*rowH + 8
}
