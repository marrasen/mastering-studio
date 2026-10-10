package eq

import (
	"fmt"
	"math"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// strip is the band picked on the graph, under it: its kind, as a pill
// for each to click; its frequency, gain and Q, or slope, as values to
// drag up or right, or step with the wheel, and a double-click sets one
// back; and buttons to turn it off and to delete it. Its every action
// has a key on the graph, which keeps the keyboard.
type strip struct {
	graph *graph
	// slotOf is where each of the strip's parts is, by its slot.
	slotOf map[int]geom.Rect
	// held is the value being dragged, and from where the drag began.
	held     int
	heldFrom geom.Point
	heldBand audio.Band
	hot      int
	size     geom.Size
}

// The strip's slots: a kind for each of 0 to 5, then the values and
// the buttons.
const (
	slotFreq = 10 + iota
	slotGain
	slotQ
	slotOn
	slotDelete
)

func newStrip(g *graph) *strip {
	return &strip{graph: g, slotOf: map[int]geom.Rect{}, held: -1, hot: -1}
}

// picked returns the band picked, and its place, or nil.
func (s *strip) picked() (b *audio.Band, place int) { return s.graph.band(s.graph.sel) }

// oneRow is how wide the strip is on one row.
func oneRow(th *theme.Live) float32 {
	return float32(len(kindNames))*(PillWidth.Get(th)+4) + 16 + 3*(ValueWidth.Get(th)+6) +
		2*(widget.ButtonHeight.Get(th)+12)
}

// height returns how tall the strip is at width w: one row where it
// fits, and two where not.
func (s *strip) height(w float32, th *theme.Live) float32 {
	h := StripHeight.Get(th)
	if w < oneRow(th) {
		return 2*h - 16
	}
	return h
}

// Focusable implements [gunim.Focusable]: the graph keeps the keyboard
// for the strip.
func (s *strip) Focusable() bool { return false }

// DragsTouch implements [gunim.TouchDragger]: a finger on a value
// drags it.
func (s *strip) DragsTouch() bool { return s.held >= slotFreq && s.held <= slotQ }

// Layout implements [gunim.Node]: the kinds in a row, then the values
// and the buttons, on one line where there is room and two where not.
func (s *strip) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	s.size = c.Max
	clear(s.slotOf)
	twoRows := s.size.W < oneRow(th)
	pillH, valueW, button := widget.ControlHeight.Get(th), ValueWidth.Get(th), widget.ButtonHeight.Get(th)
	valueH := button + 10
	x, y := float32(0), (StripHeight.Get(th)-valueH)/2+(valueH-pillH)/2
	pillW := PillWidth.Get(th)
	if twoRows {
		pillW = min(pillW, (s.size.W-5*4)/6)
	}
	for k := range kindNames {
		s.slotOf[k] = geom.Rc(x, y, pillW, pillH)
		x += pillW + 4
	}
	if twoRows {
		x, y = 0, StripHeight.Get(th)-16
	} else {
		x += 16
		y = (StripHeight.Get(th) - valueH) / 2
	}
	for _, slot := range []int{slotFreq, slotGain, slotQ} {
		s.slotOf[slot] = geom.Rc(x, y, valueW, valueH)
		x += valueW + 6
	}
	s.slotOf[slotOn] = geom.Rc(x+6, y+5, button, button)
	s.slotOf[slotDelete] = geom.Rc(x+button+18, y+5, button, button)
	return s.size
}

// slotAt returns the slot at p, or -1.
func (s *strip) slotAt(p geom.Point) int {
	for slot, r := range s.slotOf {
		if r.Contains(p) {
			return slot
		}
	}
	return -1
}

// Handle implements [gunim.Handler].
func (s *strip) Handle(e input.Event, u *gunim.UI) bool {
	g := s.graph
	b, i := s.picked()
	switch e := e.(type) {
	case input.PointerMove:
		if s.held >= slotFreq && s.held <= slotQ && b != nil {
			s.slide(b, e.Pos, u)
			break
		}
		s.hot = s.slotAt(e.Pos)
	case input.PointerLeave:
		s.hot = -1
	case input.PointerDown:
		if e.Button != input.ButtonPrimary || b == nil {
			return false
		}
		slot := s.slotAt(e.Pos)
		switch {
		case slot >= 0 && slot < len(kindNames):
			u.Cue(gunim.CueSelect, s)
			g.setKind(b.ID, audio.FilterKind(slot), u)
		case slot == slotOn:
			g.toggle(b.ID, u)
		case slot == slotDelete:
			g.remove(b.ID, u)
		case slot >= slotFreq && slot <= slotQ && e.Clicks == 2:
			s.reset(b, i, slot, u)
		case slot >= slotFreq && slot <= slotQ:
			s.release(u)
			s.held, s.heldFrom, s.heldBand = slot, e.Pos, *b
			name := fmt.Sprintf("Move band %d", i+1)
			if slot == slotQ {
				name = widthName(b, i)
			}
			g.host.BeginEdit(name, u)
		default:
			return false
		}
	case input.PointerUp:
		s.release(u)
	case input.Scroll:
		slot := s.slotAt(e.Pos)
		if b == nil || slot < slotFreq || slot > slotQ {
			return false
		}
		n := e.Notches.Y
		if n == 0 {
			n = e.Delta.Y / 40
		}
		switch slot {
		case slotFreq:
			g.nudge(b.ID, float64(n)/12, 0, 0, u)
		case slotGain:
			g.nudge(b.ID, 0, n*0.5, 0, u)
		default:
			g.nudge(b.ID, 0, 0, n, u)
		}
	default:
		return false
	}
	u.Invalidate()
	return true
}

// release lets go of the value held, which ends its drag's gesture.
func (s *strip) release(u *gunim.UI) {
	if s.held < 0 {
		return
	}
	s.held = -1
	s.graph.host.EndEdit(u)
}

// reset sets the value in slot of band b, at place i, back, as a
// gesture.
func (s *strip) reset(b *audio.Band, i, slot int, u *gunim.UI) {
	s.graph.edit(fmt.Sprintf("Reset band %d", i+1), true, u, func() bool {
		was := *b
		switch slot {
		case slotGain:
			if hasGain(b.Kind) {
				b.Gain = 0
			}
		case slotQ:
			b.Q, b.Slope = 1, 24
			if isCut(b.Kind) || b.Kind == audio.LowShelf || b.Kind == audio.HighShelf {
				b.Q = 0.707
			}
			if !isCut(b.Kind) {
				b.Slope = was.Slope
			}
		case slotFreq:
			b.Freq = 1000
		}
		return *b != was
	})
}

// slide moves the value held as the pointer goes up, or right, from
// where the drag began.
func (s *strip) slide(b *audio.Band, p geom.Point, u *gunim.UI) {
	d := (p.X - s.heldFrom.X) - (p.Y - s.heldFrom.Y)
	from, was := s.heldBand, *b
	switch s.held {
	case slotFreq:
		b.Freq = clampHz(float64(from.Freq) * math.Exp2(float64(d)/80))
	case slotGain:
		if hasGain(b.Kind) {
			b.Gain = clampDB(from.Gain + d/8)
		}
	case slotQ:
		if isCut(b.Kind) {
			b.Slope = max(12, min(max(from.Slope, 12)+12*int(d/30), 48))
		} else {
			b.Q = clampQ(float64(from.Q) * math.Exp2(float64(d)/80))
		}
	}
	if *b != was {
		s.graph.commit(false, u)
	}
}

// Paint implements [gunim.Node].
func (s *strip) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	ink, ground := widget.Ink.Get(th), audioui.Ground.Get(th)
	label, readout := LabelSize.Get(th), ReadoutSize.Get(th)
	b, i := s.picked()
	if b == nil {
		msg := "Pick a band to change it. Drag a point to move it, scroll on it to change its width, and right-click it for more."
		paintFit(p, msg, readout, false, geom.Pt(4, (StripHeight.Get(th)-readout)/2), box.W-8, audioui.Faded(ink, 0.5))
		return
	}
	c := bandColor(th, i)
	for k, name := range kindNames {
		r := s.slotOf[k]
		sel := audio.FilterKind(k) == b.Kind
		fill, text := audioui.Faded(ink, 0.06), audioui.Faded(ink, 0.8)
		switch {
		case sel:
			fill, text = c, ground
		case s.hot == k:
			fill = audioui.Faded(ink, 0.12)
		}
		p.RRect(r, r.Size().H/2, paint.Solid(fill))
		top := r.Min.Y + (r.Size().H-label)/2 - 1
		run := audioui.Shaped(name, label+1, sel, false)
		if run.Advance > r.Size().W-8 {
			paintFit(p, name, label+1, sel, geom.Pt(r.Min.X+4, top), r.Size().W-8, text)
		} else {
			run.Paint(p, geom.Pt(r.Min.X+(r.Size().W-run.Advance)/2, top), text)
		}
	}
	values := map[int][2]string{
		slotFreq: {"FREQ", hzText(b.Freq)},
		slotGain: {"GAIN", fmt.Sprintf("%+.1f dB", b.Gain)},
		slotQ:    {"Q", fmt.Sprintf("%.2f", b.Q)},
	}
	if !hasGain(b.Kind) {
		values[slotGain] = [2]string{"GAIN", "—"}
	}
	if isCut(b.Kind) {
		values[slotQ] = [2]string{"SLOPE", fmt.Sprintf("%d dB/oct", max(b.Slope, 12))}
	}
	radius := widget.CardRadius.Get(th)
	for _, slot := range []int{slotFreq, slotGain, slotQ} {
		r := s.slotOf[slot]
		fill := audioui.Faded(ink, 0.06)
		if s.held == slot {
			fill = audioui.Faded(c, 0.25)
		} else if s.hot == slot {
			fill = audioui.Faded(ink, 0.1)
		}
		p.RRect(r, radius, paint.Solid(fill))
		v := values[slot]
		audioui.Shaped(v[0], label-1, true, false).Paint(p, r.Min.Add(geom.Pt(10, 7)), audioui.Faded(c, 0.9))
		paintFit(p, v[1], readout+2, true, r.Min.Add(geom.Pt(10, 7+label+4)), r.Size().W-14, ink)
	}
	on := s.slotOf[slotOn]
	pc := audioui.Faded(ink, 0.4)
	if b.On {
		pc = c
	}
	p.RRect(on, on.Size().W/2, paint.Solid(audioui.Faded(ink, 0.06)))
	widget.PaintIcon(p, th, icon.Power, on.Inset(geom.Uniform(on.Size().W/4)), pc)
	del := s.slotOf[slotDelete]
	p.RRect(del, del.Size().W/2, paint.Solid(audioui.Faded(ink, 0.06)))
	widget.PaintIcon(p, th, icon.Trash2, del.Inset(geom.Uniform(del.Size().W/4)), audioui.Faded(ink, 0.7))
}
