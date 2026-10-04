package main

import (
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
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
// it go.
type markField struct {
	*widget.TextField
	e *editor
}

// Handle implements [gunim.Handler].
func (f *markField) Handle(ev input.Event, u *gunim.UI) bool {
	if _, ok := ev.(input.FocusLost); ok && f.e.writing {
		f.e.keepMark(u)
	}
	return f.TextField.Handle(ev, u)
}

func newMarkField(e *editor) *markField {
	f := &markField{TextField: widget.NewTextField(), e: e}
	f.Placeholder = "What is there"
	f.Disabled = true
	f.OnSubmit = func(string) gunim.Intent { return nil }
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

// markUnder returns the mark whose pin is at p, or -1.
func (e *editor) markUnder(p geom.Point) int {
	for i, m := range e.track.Marks {
		if e.markAt(m).Inset(geom.Uniform(-3)).Contains(p) {
			return i
		}
	}
	return -1
}

// cardOf is where mark i's card shows, under its pin, and its button
// that takes it away.
func (e *editor) cardOf(i int) (card, remove geom.Rect) {
	m := e.track.Marks[i]
	pin := e.markAt(m)
	w := min(max(shaped(m.Text, 12, false).Advance+52, 120), 320)
	x := max(4, min(pin.Center().X-w/2, e.size.W-w-4))
	card = geom.Rc(x, pin.Max.Y+6, w, 34)
	remove = geom.Rc(card.Max.X-30, card.Min.Y+5, 24, 24)
	return card, remove
}

// startMark opens the field to write a note: anew at at, or over mark
// id.
func (e *editor) startMark(at time.Duration, id int, text string, u *gunim.UI) {
	if e.track.ID == 0 {
		return
	}
	e.writing, e.writeAt, e.writeID = true, at, id
	e.markField.Disabled = false
	e.markField.SetText(text)
	e.markField.Select(0, len([]rune(text)))
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
	e.markField.Disabled = true
	text := strings.TrimSpace(e.markField.Text())
	switch {
	case e.writeID == 0 && text != "":
		u.Send(e, AddMark{Track: e.track.ID, At: e.writeAt, Text: text})
	case e.writeID != 0 && text != "":
		u.Send(e, SetMark{Track: e.track.ID, ID: e.writeID, Text: text})
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
			e.hotMark = hot
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
				e.hotMark = -1
				return true
			}
			if e.markAt(m).Inset(geom.Uniform(-3)).Contains(ev.Pos) {
				if ev.Clicks == 2 {
					e.startMark(m.At, m.ID, m.Text, u)
				} else {
					start, _ := e.span()
					u.Send(e, SeekTo{At: e.renderTime(max(start, m.At.Seconds()))})
				}
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
	for i, m := range e.track.Marks {
		pin := e.markAt(m)
		if pin.Max.X < 0 || pin.Min.X > box.W {
			continue
		}
		x := pin.Center().X
		p.RRect(geom.Rc(x-0.5, pin.Max.Y, 1, top+2*laneH-pin.Max.Y), 0, paint.Solid(faded(amber, 0.25)))
		lit := i == e.hotMark || (e.writing && e.writeID == m.ID)
		fill := mix(night, amber, 0.25)
		if lit {
			fill = faded(amber, 0.9)
		}
		p.ShadowRRect(pin, markSize/2, paint.Solid(fill), paint.Shadow{Blur: 6, Color: faded(night, 0.6)})
		c := amber
		if lit {
			c = night
		}
		widget.PaintIcon(p, f.Theme, icon.MessageSquareText, pin.Inset(geom.Uniform(3)), c)
	}
	if i := e.hotMark; i >= 0 && i < len(e.track.Marks) && !e.writing {
		m := e.track.Marks[i]
		card, remove := e.cardOf(i)
		p.ShadowRRect(card, 10, paint.Solid(raised), paint.Shadow{Blur: 14, Color: faded(night, 0.7)})
		shapedFace(clock(m.At), 9, false, true).Paint(p, geom.Pt(card.Min.X+10, card.Min.Y+3), faded(amber, 0.9))
		paintFit(p, m.Text, 12, false, geom.Pt(card.Min.X+10, card.Min.Y+15), card.Size().W-48, ink)
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
