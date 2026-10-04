package main

import (
	"math"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// Loop is a stretch of a track's file, from In to Out, played over and
// over while the album loops.
type Loop struct {
	In, Out time.Duration
}

type (
	// SetLoop sets a track's loop, or, nil, takes it away.
	SetLoop struct {
		Track int
		Loop  *Loop
	}
	// SetLooping turns looping on or off.
	SetLooping struct{ On bool }
)

// loopLength is how long a loop made at a point runs.
const loopLength = 8 * time.Second

// loopKey is what the deck loops, as last told.
type loopKey struct {
	id, starts int
	file       string
	edit       Edit
	gap        time.Duration
	loop       Loop
	on         bool
}

// applyLoop tells the deck the loop of the track playing, where it
// changed since.
func (a *app) applyLoop() {
	t := a.track(a.Current)
	if t == nil || a.d.done() == nil {
		a.looped = loopKey{}
		return
	}
	k := loopKey{id: t.ID, starts: a.Starts, file: t.File, edit: t.Edit, gap: a.gapOf(t), on: a.Looping && t.Loop != nil}
	if t.Loop != nil {
		k.loop = *t.Loop
	}
	if k == a.looped {
		return
	}
	a.looped = k
	// The loop in the render's time: the silence before, then the cut.
	inRender := func(at time.Duration) time.Duration { return max(0, k.gap+at-t.Edit.Start) }
	a.d.setLoop(t.ID, t.File, k.gap, t.Edit, inRender(k.loop.In), inRender(k.loop.Out), k.on)
}

func (a *app) handleLoop(in gunim.Intent) bool {
	switch in := in.(type) {
	case SetLoop:
		t := a.track(in.Track)
		if t == nil {
			return true
		}
		if in.Loop != nil {
			l := *in.Loop
			if l.Out < l.In {
				l.In, l.Out = l.Out, l.In
			}
			if l.Out-l.In < 100*time.Millisecond {
				l.Out = l.In + 100*time.Millisecond
			}
			in.Loop = &l
		}
		t.Loop = in.Loop
		a.dirty = true
	case SetLooping:
		a.Looping = in.On
		a.dirty = true
	default:
		return false
	}
	return true
}

// The editor's half: the loop on the ruler, its chip, and its keys.

// loopButton is where "Loop" is, left of "+ Note".
func (e *editor) loopButton() geom.Rect {
	n := e.noteButton()
	w := shaped("Loop", 10, true).Advance + 32
	return geom.Rc(n.Min.X-8-w, n.Min.Y, w, n.Size().H)
}

// loopShown is the track's loop, as a drag sets it ahead of the
// application's answer.
func (e *editor) loopShown() *Loop {
	if e.dragLoop != nil {
		return e.dragLoop
	}
	return e.track.Loop
}

// loopEdge returns the edge of the loop at p on the ruler: gripLoopIn,
// gripLoopOut, or gripNone.
func (e *editor) loopEdge(p geom.Point) grip {
	l := e.loopShown()
	if l == nil || p.Y > rulerH {
		return gripNone
	}
	near := func(t time.Duration) bool { return math.Abs(float64(p.X-e.xOf(t.Seconds()))) < 6 }
	switch {
	case near(l.In):
		return gripLoopIn
	case near(l.Out):
		return gripLoopOut
	}
	return gripNone
}

// loopHere is a loop from at, loopLength long, within the file.
func (e *editor) loopHere(at time.Duration) *Loop {
	length := time.Duration(e.length() * float64(time.Second))
	in := max(0, min(at, length-loopLength))
	return &Loop{In: in, Out: min(in+loopLength, length)}
}

// toggleLoop turns looping on or off, making a loop at the playhead for
// a track with none.
func (e *editor) toggleLoop(u *gunim.UI) {
	if e.track.ID == 0 {
		return
	}
	if e.track.Loop == nil {
		u.Send(e, SetLoop{Track: e.track.ID, Loop: e.loopHere(e.newMarkAt())})
	}
	u.Send(e, SetLooping{On: !e.r.state.Looping})
}

// setLoopEnd sets the loop's in, or its out, at the playhead.
func (e *editor) setLoopEnd(out bool, u *gunim.UI) {
	if e.track.ID == 0 {
		return
	}
	at := e.newMarkAt()
	l := e.loopHere(at)
	if cur := e.track.Loop; cur != nil {
		c := *cur
		if out {
			c.Out = at
		} else {
			c.In = at
		}
		l = &c
	} else if out {
		l = &Loop{In: max(0, at-loopLength), Out: at}
	}
	u.Send(e, SetLoop{Track: e.track.ID, Loop: l})
}

// dragLoopEdge moves the edge held to t, seconds of the file.
func (e *editor) dragLoopEdge(t float64, u *gunim.UI) {
	l := e.loopShown()
	if l == nil {
		return
	}
	c := *l
	at := time.Duration(max(0, min(t, e.length())) * float64(time.Second))
	if e.held == gripLoopIn {
		c.In = min(at, c.Out-100*time.Millisecond)
	} else {
		c.Out = max(at, c.In+100*time.Millisecond)
	}
	e.dragLoop = &c
	u.Send(e, SetLoop{Track: e.track.ID, Loop: &c})
}

// paintLoop draws the loop on the ruler: a band, bright while looping,
// with its edges to drag, and over the lanes a faint light while it
// loops; and the chip that turns it on.
func (e *editor) paintLoop(p *paint.Painter, f gunim.Frame, box geom.Size) {
	on := e.r.state.Looping
	b := e.loopButton()
	fill := faded(night, 0.75)
	if on {
		fill = faded(teal, 0.25)
	}
	p.RRect(b, 10, paint.Solid(fill))
	p.RRectStroke(b, 10, paint.Solid(faded(ink, 0)), paint.Stroke{Width: 1, Color: faded(teal, 0.5)})
	widget.PaintIcon(p, f.Theme, icon.Repeat, geom.Rc(b.Min.X+8, b.Min.Y+3, 14, 14), teal)
	shaped("Loop", 10, true).Paint(p, geom.Pt(b.Min.X+26, b.Min.Y+4), ink)
	l := e.loopShown()
	if l == nil {
		return
	}
	x0, x1 := e.xOf(l.In.Seconds()), e.xOf(l.Out.Seconds())
	if x1 < 0 || x0 > box.W {
		return
	}
	alpha := float32(0.25)
	if on {
		alpha = 0.6
		p.RRect(geom.Rc(x0, rulerH, x1-x0, box.H-rulerH), 0, paint.Solid(faded(teal, 0.05)))
	}
	p.RRect(geom.Rc(x0, 2, x1-x0, rulerH-4), 4, paint.Solid(faded(teal, alpha*0.5)))
	for i, x := range []float32{x0, x1} {
		g := gripLoopIn
		if i == 1 {
			g = gripLoopOut
		}
		w := float32(3)
		if e.hot == g || e.held == g {
			w = 5
		}
		p.RRect(geom.Rc(x-w/2, 0, w, rulerH), w/2, paint.Solid(faded(teal, alpha+0.3)))
	}
}
