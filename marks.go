package main

import (
	"slices"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/widget"
)

// Notes at a time of a track: what to fix there. "+ Note" in the
// editor writes one at the playhead, or the middle of the view; each
// shows as a mark on the track at its time, its words on hover, with a
// button to take it away; a click on it seeks there, a double-click
// writes it anew.

// Mark is a note at a time of a track's file.
type Mark struct {
	ID   int
	At   time.Duration
	Text string
}

type (
	// AddMark writes a note at a time of a track.
	AddMark struct {
		Track int
		At    time.Duration
		Text  string
	}
	// SetMark writes a note anew.
	SetMark struct {
		Track, ID int
		Text      string
	}
	// RemoveMark takes a note away.
	RemoveMark struct{ Track, ID int }
)

// markField is the field a note is written in, over its mark: it keeps
// the note as Enter is pressed or it loses the keyboard, and Escape lets
// it go. A press that takes the keyboard from it only closes it.
type markField struct {
	*widget.TextField
	e *editor
}

// Handle implements [gunim.Handler].
func (f *markField) Handle(ev input.Event, u *gunim.UI) bool {
	if _, ok := ev.(input.FocusLost); ok && f.e.writing {
		f.e.keptAt = u.Now()
		f.e.keepMark(u)
	}
	return f.TextField.Handle(ev, u)
}

func newMarkField(e *editor) *markField {
	f := &markField{TextField: widget.NewTextField(), e: e}
	f.Placeholder = "What is there"
	f.Disabled = true
	f.OnCommit = func(string, *gunim.UI) gunim.Intent { return nil }
	f.Keys = func(k input.KeyPress, u *gunim.UI) bool {
		switch k.Key {
		case input.KeyEnter:
			e.keepMark(u)
		case input.KeyEscape:
			e.writing = false
			f.Disabled = true
			u.Focus(e.r)
		default:
			return false
		}
		u.Invalidate()
		return true
	}
	return f
}

// The marks' measures: each a pin of markSize at the lanes' top.
const markSize = 18

// markAt is where mark m's pin is.
func (e *editor) markAt(m Mark) geom.Rect {
	x := e.xOf(m.At.Seconds())
	return geom.Rc(x-markSize/2, rulerH-markSize/2+2, markSize, markSize)
}

// noteButton is where "+ Note" is, left of the legend.
func (e *editor) noteButton() geom.Rect {
	first := e.legendRects()[0]
	w := shaped("Note", 10, true).Advance + 32
	return geom.Rc(first.Min.X-8-w, first.Min.Y, w, first.Size().H)
}

// pinGlow returns how lit note id's pin is, animated.
func (e *editor) pinGlow(id int) *anim.Float {
	g := e.markGlow[id]
	if g == nil {
		g = anim.NewFloat(0)
		e.Add(g)
		e.markGlow[id] = g
	}
	return g
}

// setHotMark makes mark i, or none, the one under the pointer: its pin
// lights and its card comes in, the last's going.
func (e *editor) setHotMark(i int) {
	was := e.hotMark
	e.hotMark = i
	e.lightMarks()
	if i < 0 || i >= len(e.track.Marks) {
		e.cardIn.Animate(0, anim.Spring{Response: 0.22, Damping: 1})
		return
	}
	if was >= 0 && was != i {
		// From one card straight to the next: it comes in from halfway.
		e.cardIn.Jump(min(e.cardIn.Value(), 0.5))
	}
	e.cardID = e.track.Marks[i].ID
	e.cardIn.Animate(1, anim.Spring{Response: 0.28, Damping: 0.75})
}

// lightMarks lights the pins of the note under the pointer and the note
// written, and puts out the rest.
func (e *editor) lightMarks() {
	for i, m := range e.track.Marks {
		on := i == e.hotMark || (e.writing && e.writeID == m.ID)
		e.pinGlow(m.ID).Animate(onOff(on), anim.Spring{Response: 0.18, Damping: 0.7})
	}
}

// markUnder returns the mark whose pin is at p, or -1.
func (e *editor) markUnder(p geom.Point) int {
	for i, m := range e.track.Marks {
		if e.markAt(m).Inset(geom.Uniform(-3)).Contains(p) {
			return i
		}
	}
	return -1
}

// The note card's measures: its widest, and the room its text leaves
// for the padding and the button that takes the note away.
const (
	cardMaxW = 320
	cardPadW = 52
)

// noteText is mark m's note laid out in lines as wide as its card
// lets it be, all of it, kept while it is shown.
func (e *editor) noteText(m Mark) text.Paragraph {
	if e.noteLaid.text == m.Text {
		return e.noteLaid.para
	}
	e.noteLaid.text = m.Text
	e.noteLaid.para = text.GoSans(false, false).Layout(m.Text, text.Style{Size: 12}, cardMaxW-cardPadW)
	return e.noteLaid.para
}

// cardOf is where mark i's card shows, under its pin, as tall as its
// note needs, and its button that takes it away.
func (e *editor) cardOf(i int) (card, remove geom.Rect) {
	m := e.track.Marks[i]
	pin := e.markAt(m)
	para := e.noteText(m)
	w := min(max(para.Size.W+cardPadW, 120), cardMaxW)
	x := max(4, min(pin.Center().X-w/2, e.size.W-w-4))
	card = geom.Rc(x, pin.Max.Y+6, w, max(34, 19+para.Size.H))
	remove = geom.Rc(card.Max.X-30, card.Min.Y+5, 24, 24)
	return card, remove
}

// startMark opens the field to write a note: anew at at, or over mark
// id.
func (e *editor) startMark(at time.Duration, id int, note string, u *gunim.UI) {
	if e.track.ID == 0 {
		return
	}
	e.writing, e.writeAt, e.writeID = true, at, id
	e.lightMarks()
	e.markField.Disabled = false
	e.markField.SetText(note, u)
	e.markField.Select(0, len([]rune(note)))
	u.Focus(e.markField)
	u.Invalidate()
}

// keepMark keeps the note written: a new one, or one written anew; a
// new one written empty is let go, and one emptied taken away.
func (e *editor) keepMark(u *gunim.UI) {
	if !e.writing {
		return
	}
	e.writing = false
	e.lightMarks()
	e.markField.Disabled = true
	note := strings.TrimSpace(e.markField.Text())
	switch {
	case e.writeID == 0 && note != "":
		u.Send(e, AddMark{Track: e.track.ID, At: e.writeAt, Text: note})
	case e.writeID != 0 && note != "":
		u.Send(e, SetMark{Track: e.track.ID, ID: e.writeID, Text: note})
	case e.writeID != 0:
		u.Send(e, RemoveMark{Track: e.track.ID, ID: e.writeID})
	}
	u.Focus(e.r)
	u.Invalidate()
}

// newMarkAt is where a new note goes: at the playhead, or the middle of
// the view.
func (e *editor) newMarkAt() time.Duration {
	t, ok := e.playhead()
	if !ok {
		t = float64(e.v0.Value()+e.v1.Value()) / 2
	}
	return time.Duration(max(0, min(t, e.length())) * float64(time.Second))
}

// handleMarks takes the pointer for the marks and "+ Note"; it returns
// whether it took the event.
func (e *editor) handleMarks(ev input.Event, u *gunim.UI) bool {
	switch ev := ev.(type) {
	case input.PointerMove:
		hot := e.markUnder(ev.Pos)
		// The card stays while the pointer is over it, for its button.
		if hot < 0 && e.hotMark >= 0 && e.hotMark < len(e.track.Marks) {
			if card, _ := e.cardOf(e.hotMark); card.Inset(geom.Uniform(-4)).Contains(ev.Pos) {
				hot = e.hotMark
			}
		}
		if hot != e.hotMark {
			e.setHotMark(hot)
			u.Invalidate()
		}
		return hot >= 0 || e.noteButton().Contains(ev.Pos) || e.loopButton().Contains(ev.Pos)
	case input.PointerDown:
		if ev.Button != input.ButtonPrimary {
			return false
		}
		if e.loopButton().Contains(ev.Pos) {
			u.Cue(gunim.CueTick, e)
			e.toggleLoop(u)
			return true
		}
		if e.noteButton().Contains(ev.Pos) {
			u.Cue(gunim.CueTick, e)
			e.startMark(e.newMarkAt(), 0, "", u)
			return true
		}
		if i := e.hotMark; i >= 0 && i < len(e.track.Marks) {
			m := e.track.Marks[i]
			if _, remove := e.cardOf(i); remove.Contains(ev.Pos) {
				u.Cue(gunim.CueTick, e)
				u.Send(e, RemoveMark{Track: e.track.ID, ID: m.ID})
				e.setHotMark(-1)
				return true
			}
			if e.markAt(m).Inset(geom.Uniform(-3)).Contains(ev.Pos) {
				if e.seekMark != nil {
					e.seekMark()
					e.seekMark = nil
				}
				if ev.Clicks == 2 {
					e.startMark(m.At, m.ID, m.Text, u)
					return true
				}
				// A click seeks to the note, once it is plain no second
				// click follows to write it anew.
				start, _ := e.span()
				to := SeekTo{At: e.renderTime(max(start, m.At.Seconds()))}
				e.seekMark = u.After(doubleClick, func(u *gunim.UI) {
					e.seekMark = nil
					u.Send(e, to)
				})
				return true
			}
			if card, _ := e.cardOf(i); card.Contains(ev.Pos) {
				return true
			}
		}
	}
	return false
}

// paintMarks draws the notes' pins, the hot one's card, and "+ Note".
func (e *editor) paintMarks(p *paint.Painter, f gunim.Frame, box geom.Size) {
	b := e.noteButton()
	p.RRect(b, 10, paint.Solid(faded(night, 0.75)))
	p.RRectStroke(b, 10, paint.Solid(faded(ink, 0)), paint.Stroke{Width: 1, Color: faded(amber, 0.5)})
	widget.PaintIcon(p, f.Theme, icon.MessageSquarePlus, geom.Rc(b.Min.X+8, b.Min.Y+3, 14, 14), amber)
	shaped("Note", 10, true).Paint(p, geom.Pt(b.Min.X+26, b.Min.Y+4), ink)
	top, laneH := e.lanes()
	for _, m := range e.track.Marks {
		pin := e.markAt(m)
		if pin.Max.X < 0 || pin.Min.X > box.W {
			continue
		}
		x := pin.Center().X
		lit := e.pinGlow(m.ID).Value()
		// Its line down the lanes, brighter while lit.
		p.RRect(geom.Rc(x-0.5, pin.Max.Y, 1, top+2*laneH-pin.Max.Y), 0, paint.Solid(faded(amber, 0.25+0.35*lit)))
		// The pin, filling with amber and swelling as it lights, glowing.
		end := p.Push(paint.Scale(1+0.18*lit, pin.Center()))
		fill := mix(mix(night, amber, 0.25), faded(amber, 0.9), lit)
		p.ShadowRRect(pin, markSize/2, paint.Solid(fill), paint.Shadow{Blur: 6 + 8*lit,
			Color: mix(faded(night, 0.6), faded(amber, 0.5), lit)})
		widget.PaintIcon(p, f.Theme, icon.MessageSquareText, pin.Inset(geom.Uniform(3)), mix(amber, night, lit))
		end()
	}
	// The card of the note under the pointer, fading and sliding in under
	// its pin, and out again.
	in := e.cardIn.Value()
	if i := slices.IndexFunc(e.track.Marks, func(m Mark) bool { return m.ID == e.cardID }); i >= 0 && in > 0.01 && !e.writing {
		m := e.track.Marks[i]
		card, remove := e.cardOf(i)
		end := p.Layer(paint.LayerOpts{Bounds: card.Inset(geom.Uniform(-16)), Opacity: min(in, 1)})
		defer end()
		defer p.Push(paint.Translate(geom.Pt(0, -8*(1-in))))()
		defer p.Push(paint.Scale(0.94+0.06*in, geom.Pt(card.Center().X, card.Min.Y)))()
		p.ShadowRRect(card, 10, paint.Solid(raised), paint.Shadow{Blur: 14, Color: faded(night, 0.7)})
		shapedFace(clock(m.At), 9, false).Paint(p, geom.Pt(card.Min.X+10, card.Min.Y+3), faded(amber, 0.9))
		e.noteText(m).Paint(p, geom.Pt(card.Min.X+10, card.Min.Y+15), ink)
		widget.PaintIcon(p, f.Theme, icon.Trash2, remove.Inset(geom.Uniform(5)), faded(coral, 0.9))
	}
}

// The application's half: a track's notes.

func (a *app) handleMarks(in gunim.Intent) bool {
	switch in := in.(type) {
	case AddMark:
		t := a.track(in.Track)
		if t == nil {
			return true
		}
		a.markIDs++
		t.Marks = append(t.Marks, Mark{ID: a.markIDs, At: in.At, Text: in.Text})
		sortMarks(t.Marks)
		a.dirty = true
	case SetMark:
		if t := a.track(in.Track); t != nil {
			for i := range t.Marks {
				if t.Marks[i].ID == in.ID {
					t.Marks[i].Text = in.Text
					a.dirty = true
				}
			}
		}
	case RemoveMark:
		if t := a.track(in.Track); t != nil {
			for i := range t.Marks {
				if t.Marks[i].ID == in.ID {
					t.Marks = append(t.Marks[:i:i], t.Marks[i+1:]...)
					a.dirty = true
					break
				}
			}
		}
	default:
		return false
	}
	return true
}

// sortMarks puts notes in the order of their times.
func sortMarks(ms []Mark) {
	for i := 1; i < len(ms); i++ {
		for j := i; j > 0 && ms[j].At < ms[j-1].At; j-- {
			ms[j], ms[j-1] = ms[j-1], ms[j]
		}
	}
}

// doubleClick is how long a click waits for a second, to be a
// double-click.
const doubleClick = 350 * time.Millisecond
