package main

import (
	"fmt"
	"image/color"
	"math"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// The parts of the editor a press takes hold of.
type grip int

const (
	gripNone grip = iota
	gripStart
	gripEnd
	gripFadeIn
	gripFadeOut
	gripSeek
	gripZoom
	// gripRuler is the ruler, which a drag scrolls the view by, and a
	// click seeks at.
	gripRuler
	// gripSilence is the start of the silence before the cut, which a
	// drag sets the track's own silence by.
	gripSilence
)

// editor is the track picked, laid out along its file's time: both
// channels' waveforms as edited, the parts cut away faint, the fades
// shaping the sound, the album's silence before the cut start, and the
// playhead, which runs through the silence and on into the sound. The
// cut's ends and the fades' handles drag; the wheel zooms about the
// pointer, and with Shift pans.
type editor struct {
	anim.Group
	r     *root
	track Track
	edit  Edit
	seq   int
	// v0 and v1 are the time the view runs from and to, in seconds of
	// the file, gliding as it zooms and pans.
	v0, v1 *anim.Float
	// held is what a press took hold of, and hot what the pointer is
	// over; heldAt is where on it the press took it.
	held, hot grip
	heldAt    float64
	pointer   geom.Point
	// pressed is where a press went down, and dragged whether it has
	// moved since, for the ruler to tell a click from a drag.
	pressed geom.Point
	dragged bool
	// inFrom and outFrom are the fades' curves before their last
	// change, and inMorph and outMorph how far they have turned into the
	// new ones.
	inFrom, outFrom   Curve
	inMorph, outMorph *anim.Float
	glow              *anim.Float
	// zoom is how far the waveform is drawn louder, in decibels, to see
	// quiet sound: its slider runs up the editor's right edge.
	zoom *anim.Float
	// raw is the samples of the file in view, read for a view zoomed in
	// past the waveform's finest level.
	raw rawSamples
	// clock is the playhead, in seconds of the file, run on by the
	// frames' time and drawn gently toward where the speakers are, as
	// they tell it in uneven steps; clockID is the track it runs for.
	clock   float64
	clockID int
	// synced says the view and curves have been set from the album once.
	synced bool
	// grams are the spectrograms drawn, of the tracks seen last.
	grams map[*Gram]*gramTiles
	// silence is the silence before the track as a drag sets it, ahead
	// of the application's answer, or -1.
	silence float64
	// gram runs from 0, the waveform shown, to 1, the spectrogram, the
	// one fading into the other; curves fade each loudness curve in and
	// out, in the legend's order.
	gram   *anim.Float
	curves [4]*anim.Float
	// fling is how fast the view glides on after a flick of the ruler,
	// in seconds of the file a second; dragV is how fast a drag moves
	// it, wasV0 where it started the frame, and still how long since it
	// moved.
	fling, dragV float64
	wasV0        float32
	still        time.Duration
	// markField writes a note at a time, at writeAt, or over the note
	// writeID, while writing; hotMark is the note under the pointer.
	markField *markField
	writing   bool
	writeAt   time.Duration
	writeID   int
	hotMark   int
	size      geom.Size
}

// rawSamples is a stretch of a file's samples, read in the background.
type rawSamples struct {
	file    string
	from    int64
	data    []float32
	loading bool
	loaded  chan rawSamples
}

func newEditor(r *root) *editor {
	e := &editor{r: r, v0: anim.NewFloat(-1), v1: anim.NewFloat(10), inMorph: anim.NewFloat(1),
		outMorph: anim.NewFloat(1), glow: anim.NewFloat(0), zoom: anim.NewFloat(0)}
	e.Add(e.v0, e.v1, e.inMorph, e.outMorph, e.glow, e.zoom)
	e.raw.loaded = make(chan rawSamples, 1)
	e.silence = -1
	e.markField = newMarkField(e)
	e.hotMark = -1
	e.gram = anim.NewFloat(0)
	e.Add(e.gram)
	for i := range e.curves {
		e.curves[i] = anim.NewFloat(0)
		e.Add(e.curves[i])
	}
	e.synced = false
	return e
}

// length is the file's length, in seconds.
func (e *editor) length() float64 {
	if e.track.Format.SampleRate == 0 {
		return 1
	}
	return float64(e.track.Frames) / float64(e.track.Format.SampleRate)
}

// gap is the silence before the track, in seconds: its own, or the
// album's, or as a drag sets it.
func (e *editor) gap() float64 {
	if e.silence >= 0 {
		return e.silence
	}
	return e.r.state.gapOf(&e.track).Seconds()
}

// fit returns the view showing the whole file and the silence before
// its cut.
func (e *editor) fit() (v0, v1 float64) {
	l := e.length()
	from := min(0, e.edit.Start.Seconds()-e.gap())
	pad := (l - from) * 0.02
	return from - pad, l + pad
}

func (e *editor) show(was Album, t Track) {
	// The view and the curves glide to what the album says; the first
	// time, they are there at once.
	s := e.r.state
	move := func(f *anim.Float, to float32) {
		if !e.synced {
			f.Jump(to)
		} else {
			f.Animate(to, anim.Spring{Response: 0.35, Damping: 1})
		}
	}
	move(e.gram, onOff(s.View == ViewGram))
	for i, c := range loudCurves {
		move(e.curves[i], onOff(s.Curves&c.bit != 0))
	}
	e.synced = true
	fresh := t.ID != e.track.ID || t.Scanned != e.track.Scanned || t.File != e.track.File
	e.track = t
	// The window's own edits are newer than the application's answers.
	if t.Seq >= e.seq || fresh {
		e.morph(t.Edit)
		e.edit, e.seq = t.Edit, t.Seq
	}
	if fresh {
		v0, v1 := e.fit()
		if t.ID != was.Current || e.v1.Value() <= e.v0.Value() {
			e.v0.Jump(float32(v0))
			e.v1.Jump(float32(v1))
		} else {
			e.v0.Animate(float32(v0), anim.Gentle)
			e.v1.Animate(float32(v1), anim.Gentle)
		}
		e.glow.Jump(1)
		e.glow.Animate(0, anim.Spring{Response: 0.6, Damping: 1})
	}
}

// morph turns a fade whose curve changes into its new one.
func (e *editor) morph(to Edit) {
	if to.FadeIn.Curve != e.edit.FadeIn.Curve {
		e.inFrom = e.edit.FadeIn.Curve
		e.inMorph.Jump(0)
		e.inMorph.Animate(1, anim.Spring{Response: 0.35, Damping: 1})
	}
	if to.FadeOut.Curve != e.edit.FadeOut.Curve {
		e.outFrom = e.edit.FadeOut.Curve
		e.outMorph.Jump(0)
		e.outMorph.Animate(1, anim.Spring{Response: 0.35, Damping: 1})
	}
}

// send sets the edit, and tells the application.
func (e *editor) send(ed Edit, u *gunim.UI) {
	e.morph(ed)
	e.edit = ed
	e.seq++
	u.Send(e, SetEdit{ID: e.track.ID, Edit: ed, Seq: e.seq})
	u.Invalidate()
}

// The editor's areas: the ruler along the top, and the two channels'
// lanes under it.
const rulerH = 24

func (e *editor) lanes() (top, laneH float32) {
	top = rulerH + 6
	return top, (e.size.H - top - 8) / 2
}

// xOf and tAt turn the file's time, in seconds, to the editor's x and
// back.
func (e *editor) xOf(t float64) float32 {
	v0, v1 := float64(e.v0.Value()), float64(e.v1.Value())
	return float32((t - v0) / (v1 - v0) * float64(e.size.W))
}

func (e *editor) tAt(x float32) float64 {
	v0, v1 := float64(e.v0.Value()), float64(e.v1.Value())
	return v0 + float64(x)/float64(e.size.W)*(v1-v0)
}

// span returns the cut's start and end, in seconds.
func (e *editor) span() (start, end float64) {
	start = e.edit.Start.Seconds()
	end = e.length()
	if e.edit.End > 0 {
		end = min(e.edit.End.Seconds(), end)
	}
	return start, end
}

// envelope is the edit's gain at time t of the file, the fades and the
// gain both, with each fade's curve partway to its new one.
func (e *editor) envelope(t float64) float64 {
	start, end := e.span()
	if t < start || t > end {
		return 0
	}
	g := math.Pow(10, float64(e.edit.Gain)/20)
	if in := e.edit.FadeIn.Length.Seconds(); in > 0 && t < start+in {
		u := (t - start) / in
		m := float64(e.inMorph.Value())
		g *= e.inFrom.at(u)*(1-m) + e.edit.FadeIn.Curve.at(u)*m
	}
	if out := e.edit.FadeOut.Length.Seconds(); out > 0 && t > end-out {
		u := (end - t) / out
		m := float64(e.outMorph.Value())
		g *= e.outFrom.at(u)*(1-m) + e.edit.FadeOut.Curve.at(u)*m
	}
	return g
}

// handles returns where the fades' handles are.
func (e *editor) handles() (in, out geom.Point) {
	top, _ := e.lanes()
	start, end := e.span()
	return geom.Pt(e.xOf(start+e.edit.FadeIn.Length.Seconds()), top),
		geom.Pt(e.xOf(end-e.edit.FadeOut.Length.Seconds()), top)
}

// gripAt returns what a press at p takes hold of.
func (e *editor) gripAt(p geom.Point) grip {
	in, out := e.handles()
	near := func(h geom.Point) bool {
		d := p.Sub(h)
		return d.X*d.X+d.Y*d.Y < 12*12
	}
	start, end := e.span()
	top, laneH := e.lanes()
	switch {
	case p.X > e.size.W-zoomW && p.Y > top && p.Y < top+2*laneH && e.r.state.View == ViewWave:
		return gripZoom
	case p.Y < rulerH && !near(in) && !near(out):
		return gripRuler
	case near(in):
		return gripFadeIn
	case near(out):
		return gripFadeOut
	case p.Y > rulerH && math.Abs(float64(p.X-e.xOf(start))) < 7:
		return gripStart
	case p.Y > rulerH && math.Abs(float64(p.X-e.xOf(end))) < 7:
		return gripEnd
	case p.Y > rulerH && math.Abs(float64(p.X-e.xOf(start-e.gap()))) < 7:
		return gripSilence
	}
	return gripSeek
}

// DragsTouch implements [gunim.TouchDragger].
func (e *editor) DragsTouch() bool { return e.held != gripNone && e.held != gripSeek }

// Handle implements [gunim.Handler].
func (e *editor) Handle(ev input.Event, u *gunim.UI) bool {
	if !e.track.Scanned {
		return false
	}
	if e.held == gripNone && e.handleMarks(ev, u) {
		return true
	}
	switch ev := ev.(type) {
	case input.PointerMove:
		e.pointer = ev.Pos
		if e.held == gripNone {
			e.hot = e.gripAt(ev.Pos)
			break
		}
		e.drag(ev.Pos, ev.Mods, u)
	case input.PointerLeave:
		if e.held == gripNone {
			e.hot = gripNone
		}
	case input.PointerDown:
		if ev.Button != input.ButtonPrimary {
			return false
		}
		if bit := e.legendAt(ev.Pos); bit != 0 {
			u.Cue(gunim.CueTick, e)
			u.Send(e, SetCurves{Curves: e.r.state.Curves ^ bit})
			break
		}
		if ev.Clicks == 2 && e.gripAt(ev.Pos) == gripZoom {
			e.zoom.Animate(0, anim.Gentle)
			break
		}
		// A double-click on the silence gives the track the album's.
		if start, _ := e.span(); ev.Clicks == 2 && ev.Pos.Y > rulerH && e.track.Silence != nil &&
			e.tAt(ev.Pos.X) < start && e.tAt(ev.Pos.X) > start-e.gap() {
			u.Send(e, SetSilence{ID: e.track.ID})
			break
		}
		if ev.Clicks == 2 && e.gripAt(ev.Pos) == gripSeek {
			v0, v1 := e.fit()
			e.v0.Animate(float32(v0), anim.Gentle)
			e.v1.Animate(float32(v1), anim.Gentle)
			break
		}
		e.held = e.gripAt(ev.Pos)
		e.heldAt = e.tAt(ev.Pos.X)
		e.fling, e.dragV = 0, 0
		e.pressed, e.dragged = ev.Pos, false
		if e.held == gripZoom {
			e.zoomTo(ev.Pos.Y)
		}
		if e.held == gripSeek {
			u.Send(e, SeekTo{At: e.renderTime(e.tAt(ev.Pos.X))})
		}
	case input.PointerUp:
		// A ruler let go while it moves flings the view on.
		if e.held == gripRuler && e.dragged && e.still < 80*time.Millisecond && math.Abs(e.dragV) > 0.05 {
			e.fling = e.dragV
		}
		if e.held == gripSilence {
			e.silence = -1
		}
		if e.held == gripRuler && !e.dragged {
			u.Send(e, SeekTo{At: e.renderTime(e.tAt(ev.Pos.X))})
		}
		e.held = gripNone
	case input.Scroll:
		e.wheel(ev)
	default:
		return false
	}
	u.Invalidate()
	return true
}

// renderTime turns time t of the file into time as rendered, the
// silence before the cut counted, from zero.
func (e *editor) renderTime(t float64) time.Duration {
	start, end := e.span()
	at := e.gap() + min(t, end) - start
	return time.Duration(max(0, at) * float64(time.Second))
}

// wheel zooms the view about the pointer, or with Shift, or a wheel
// across, pans it.
func (e *editor) wheel(ev input.Scroll) {
	n := float64(ev.Notches.Y)
	if n == 0 {
		n = float64(ev.Delta.Y) / 40
	}
	// With Alt, or over the slider, the wheel zooms the waveform up: Ctrl
	// with the wheel zooms the whole window.
	if ev.Mods.Has(input.ModAlt) || e.gripAt(ev.Pos) == gripZoom {
		e.zoom.Animate(float32(max(0, min(float64(e.zoom.Target())+3*n, maxZoom))), anim.Spring{Response: 0.2, Damping: 1})
		return
	}
	v0, v1 := float64(e.v0.Target()), float64(e.v1.Target())
	span := v1 - v0
	if ev.Mods.Has(input.ModShift) || ev.Delta.X != 0 {
		d := float64(ev.Delta.Y+ev.Delta.X) / float64(e.size.W) * span
		e.v0.Animate(float32(v0-d), anim.Spring{Response: 0.2, Damping: 1})
		e.v1.Animate(float32(v1-d), anim.Spring{Response: 0.2, Damping: 1})
		return
	}
	k := math.Pow(1.25, -n)
	f0, f1 := e.fit()
	// As close as a few samples across the editor.
	least := 24 / float64(max(e.track.Format.SampleRate, 1))
	k = max(least/span, min(k, (f1-f0)*1.2/span))
	at := float64(e.tAt(ev.Pos.X))
	e.v0.Animate(float32(at-(at-v0)*k), anim.Spring{Response: 0.2, Damping: 1})
	e.v1.Animate(float32(at+(v1-at)*k), anim.Spring{Response: 0.2, Damping: 1})
}

// drag moves what a press took hold of to p, finely with Shift.
func (e *editor) drag(p geom.Point, mods input.Mods, u *gunim.UI) {
	t := e.tAt(p.X)
	if mods.Has(input.ModShift) {
		t = e.heldAt + (t-e.heldAt)*0.1
	}
	ed := e.edit
	start, end := e.span()
	sec := func(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }
	switch e.held {
	case gripStart:
		ed.Start = sec(max(0, min(t, end-0.1)))
	case gripEnd:
		v := max(start+0.1, min(t, e.length()))
		ed.End = sec(v)
		if v >= e.length()-0.0005 {
			ed.End = 0
		}
	case gripFadeIn:
		ed.FadeIn.Length = sec(max(0, min(t-start, end-start)))
	case gripFadeOut:
		ed.FadeOut.Length = sec(max(0, min(end-t, end-start)))
	case gripSeek:
		u.Send(e, SeekTo{At: e.renderTime(t)})
		return
	case gripZoom:
		e.zoomTo(p.Y)
		return
	case gripSilence:
		s := max(0, min(start-t, 10))
		e.silence = s
		d := time.Duration(s * float64(time.Second))
		u.Send(e, SetSilence{ID: e.track.ID, Silence: &d})
		return
	case gripRuler:
		// The view moves with the pointer: the time grabbed stays under
		// it.
		if d := p.X - e.pressed.X; d*d > 9 {
			e.dragged = true
		}
		if e.dragged {
			shift := e.heldAt - e.tAt(p.X)
			e.v0.Jump(e.v0.Value() + float32(shift))
			e.v1.Jump(e.v1.Value() + float32(shift))
		}
		return
	default:
		return
	}
	// The fades stay within the cut.
	s2, e2 := ed.Start.Seconds(), e.length()
	if ed.End > 0 {
		e2 = ed.End.Seconds()
	}
	ed.FadeIn.Length = min(ed.FadeIn.Length, sec(e2-s2))
	ed.FadeOut.Length = min(ed.FadeOut.Length, sec(e2-s2))
	e.send(ed, u)
}

// The vertical zoom: its slider's width, and how far it goes, in
// decibels.
const (
	zoomW   = 26
	maxZoom = 60
)

// zoomTo sets the vertical zoom where the slider is pressed, at y.
func (e *editor) zoomTo(y float32) {
	top, laneH := e.lanes()
	u := 1 - (y-top-8)/(2*laneH-16)
	e.zoom.Animate(max(0, min(u, 1))*maxZoom, anim.Spring{Response: 0.12, Damping: 1})
}

// gain is how much louder the waveform is drawn than it is.
func (e *editor) gain() float32 { return float32(math.Pow(10, float64(e.zoom.Value())/20)) }

// Step implements [gunim.Animator]: the playhead moves while a track
// plays, and the view follows it as the album says: a page on as it
// leaves the view, or with the sound sliding under it, still; and the
// samples zoomed in on come in.
func (e *editor) Step(dt time.Duration) bool {
	moving := e.Group.Step(dt)
	select {
	case got := <-e.raw.loaded:
		e.raw.file, e.raw.from, e.raw.data, e.raw.loading = got.file, got.from, got.data, false
		moving = true
	default:
	}
	moving = e.wantRaw() || moving
	e.runClock(dt)
	flinging := e.stepFling(dt)
	moving = moving || flinging
	if !e.r.state.Playing {
		return moving
	}
	if t, ok := e.playhead(); ok && e.held == gripNone && !flinging {
		v0, v1 := float64(e.v0.Target()), float64(e.v1.Target())
		span := v1 - v0
		switch e.r.state.Follow {
		case FollowJump:
			if t > v1 || t < v0 {
				to := t - span*0.1
				e.v0.Animate(float32(to), anim.Gentle)
				e.v1.Animate(float32(to+span), anim.Gentle)
			}
		case FollowScroll:
			// The playhead still, a quarter in, with the sound sliding
			// under it, but for the view's running past the sound's
			// start or end: there the playhead moves.
			lo, hi := e.fit()
			to := max(lo, min(t-span*0.25, max(lo, hi-span)))
			if math.Abs(float64(e.v0.Value())-to) > span*0.05 {
				e.v0.Animate(float32(to), anim.Snappy)
				e.v1.Animate(float32(to+span), anim.Snappy)
			} else {
				e.v0.Jump(float32(to))
				e.v1.Jump(float32(to + span))
			}
		case FollowOff, followModes:
		}
	}
	return true
}

// framesPerPixel is how many of the file's frames a column of the
// editor spans.
func (e *editor) framesPerPixel() float64 {
	return float64(e.v1.Value()-e.v0.Value()) * float64(e.track.Format.SampleRate) / float64(max(e.size.W, 1))
}

// wantRaw reads the samples in view, and a view either side, where the
// view is zoomed in past the waveform's finest level and they are not
// read yet. It returns whether they are being read.
func (e *editor) wantRaw() bool {
	if !e.track.Scanned || e.framesPerPixel() >= finest {
		return e.raw.loading
	}
	rate := float64(e.track.Format.SampleRate)
	v0, v1 := float64(e.v0.Target()), float64(e.v1.Target())
	from := max(0, int64((2*v0-v1)*rate))
	to := min(e.track.Frames, int64((2*v1-v0)*rate)+1)
	have := e.raw.file == e.track.File && e.raw.from <= max(0, int64(v0*rate)) &&
		e.raw.from+int64(len(e.raw.data)/2) >= min(e.track.Frames, int64(v1*rate)+1)
	if have || e.raw.loading {
		return e.raw.loading
	}
	e.raw.loading = true
	file, out := e.track.File, e.raw.loaded
	go func() {
		got := rawSamples{file: file, from: from}
		if src, _, closer, err := openTrack(file); err == nil {
			if src.SeekFrame(from) == nil {
				got.data = make([]float32, 2*(to-from))
				n := 0
				for n < int(to-from) {
					k, err := src.Read(got.data[2*n:])
					n += k
					if err != nil || k == 0 {
						break
					}
				}
				got.data = got.data[:2*n]
			}
			closer()
		}
		out <- got
	}()
	return true
}

// runClock runs the playhead's clock on by dt, drawn toward where the
// speakers are: gently while near, at once after a seek or a turn.
func (e *editor) runClock(dt time.Duration) {
	t, ok := e.heard()
	if !ok {
		e.clockID = 0
		return
	}
	if e.clockID != e.track.ID || !e.r.state.Playing || math.Abs(t-e.clock) > 0.25 {
		e.clock, e.clockID = t, e.track.ID
		return
	}
	e.clock += dt.Seconds()
	e.clock += (t - e.clock) * min(1, 3*dt.Seconds())
}

// playhead returns the file's time the playhead is at, run smoothly,
// and false while the track picked is not the one playing.
func (e *editor) playhead() (float64, bool) {
	if e.clockID == e.track.ID && e.clockID != 0 {
		return e.clock, true
	}
	return e.heard()
}

// heard returns the file's time the speakers play, as they tell it, and
// false while the track picked is not the one playing.
func (e *editor) heard() (float64, bool) {
	at, _, id := e.r.d.position()
	if id != e.track.ID || id == 0 {
		return 0, false
	}
	start, _ := e.span()
	return start + at.Seconds() - e.gap(), true
}

// Layout implements [gunim.Node].
func (e *editor) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	e.size = c.Max
	// The field for a note, over where it goes.
	w := float32(260)
	x := max(4, min(e.xOf(e.writeAt.Seconds())-w/2, c.Max.W-w-4))
	kids.At(0).Layout(gunim.Tight(geom.Sz(w, 32)))
	kids.At(0).Place(geom.Pt(x, rulerH+4+markSize+6))
	return c.Max
}

// Children implements [gunim.Composite]: the field a note is written
// in, there all along so it takes the keyboard at once, shown while a
// note is written.
func (e *editor) Children() []gunim.Node { return []gunim.Node{e.markField} }

// Paint implements [gunim.Node].
func (e *editor) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	whole := geom.Rect{Max: box.Point()}
	p.RRect(whole, 16, paint.Solid(panel))
	end := p.Layer(paint.LayerOpts{Bounds: whole, Opacity: 1, Clip: true, Radius: 16})
	defer end()
	if !e.track.Scanned || e.track.Wave == nil {
		msg := "Pick a track to edit it"
		if e.track.ID != 0 {
			msg = "Reading " + e.track.Title + "…"
		}
		run := shaped(msg, 14, false)
		run.Paint(p, geom.Pt((box.W-run.Advance)/2, box.H/2-8), faded(ink, 0.5))
		return
	}
	e.paintRuler(p, box)
	e.paintGap(p, box)
	// The waveform and the spectrogram, the one fading into the other.
	whole2 := geom.Rect{Max: box.Point()}
	if g := e.gram.Value(); g < 0.99 {
		end := p.Layer(paint.LayerOpts{Bounds: whole2, Opacity: 1 - max(0, g)})
		e.paintWave(p, box)
		end()
	}
	if g := e.gram.Value(); g > 0.01 {
		end := p.Layer(paint.LayerOpts{Bounds: whole2, Opacity: min(1, g)})
		e.paintGram(p, box)
		end()
	}
	e.paintCurves(p, box)
	e.paintFades(p, box)
	e.paintCut(p, box)
	e.paintPlayhead(p, box)
	if e.r.state.View == ViewWave {
		e.paintZoom(p, box)
	}
	e.paintLegend(p)
	e.paintMarks(p, f, box)
	if e.writing {
		for k := range kids.All {
			k.Paint(p)
		}
	}
	if g := e.glow.Value(); g > 0.01 {
		p.RRectStroke(whole.Inset(geom.Uniform(1)), 15, paint.Solid(color.NRGBA{}), paint.Stroke{Width: 2, Color: faded(teal, g)})
	}
}

// tick returns a step for the ruler's marks that leaves room between
// them.
func (e *editor) tick() float64 {
	span := float64(e.v1.Value() - e.v0.Value())
	for _, s := range []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 15, 30, 60} {
		if float64(e.size.W)*s/span >= 80 {
			return s
		}
	}
	return 120
}

func (e *editor) paintRuler(p *paint.Painter, box geom.Size) {
	step := e.tick()
	for t := math.Floor(float64(e.v0.Value())/step) * step; t <= float64(e.v1.Value()); t += step {
		x := e.xOf(t)
		p.RRect(geom.Rc(x, rulerH-6, 1, 6), 0, paint.Solid(faded(ink, 0.3)))
		p.RRect(geom.Rc(x, rulerH, 1, box.H-rulerH), 0, paint.Solid(faded(ink, 0.04)))
		label := short(time.Duration(t * float64(time.Second)))
		if step < 1 {
			label = fmt.Sprintf("%.2f", t)
		}
		if t < 0 {
			continue
		}
		shapedFace(label, 10, false, true).Paint(p, geom.Pt(x+4, 5), faded(ink, 0.45))
	}
	p.RRect(geom.Rc(0, rulerH, box.W, 1), 0, paint.Solid(faded(ink, 0.08)))
}

// paintGap draws the album's silence before the cut start: a band
// striped across, with its length.
func (e *editor) paintGap(p *paint.Painter, box geom.Size) {
	start, _ := e.span()
	x0, x1 := e.xOf(start-e.gap()), e.xOf(start)
	if x1-x0 < 1 {
		return
	}
	top := float32(rulerH)
	p.RRect(geom.Rc(x0, top, x1-x0, box.H-top), 0, paint.Solid(faded(sky, 0.07)))
	for x := x0 - box.H; x < x1; x += 14 {
		// Stripes across the band, cut to it.
		a, b := max(x, x0), min(x+box.H-top, x1)
		if b > a {
			segment(p, geom.Pt(a, top+(a-x)), geom.Pt(b, top+(b-x)), 1, faded(sky, 0.12))
		}
	}
	label := fmt.Sprintf("%.2f s silence", e.gap())
	own := e.track.Silence != nil || e.silence >= 0
	if own {
		label += " · own"
	}
	run := shaped(label, 11, true)
	if run.Advance < x1-x0-8 {
		run.Paint(p, geom.Pt((x0+x1-run.Advance)/2, top+8), faded(sky, 0.85))
	}
	// Its start, a handle to set the track's own silence by.
	w := float32(1.5)
	if e.hot == gripSilence || e.held == gripSilence {
		w = 3
	}
	p.RRect(geom.Rc(x0-w/2, top, w, box.H-top), w/2, paint.Solid(faded(sky, 0.4+0.4*onOff(own))))
	if e.hot == gripSilence || e.held == gripSilence {
		e.bubble(p, box, geom.Pt(x0, box.H-44), fmt.Sprintf("%.2f s", e.gap()), sky)
	}
}

// paintWave draws both channels' waveforms, a column a pixel, from
// the finest level of the waveform the zoom needs, or, zoomed in past
// it, from the samples themselves, drawn as the line they make: the
// file faint, and the sound as edited bright over it, all as loud as
// the vertical zoom draws it.
func (e *editor) paintWave(p *paint.Painter, box geom.Size) {
	top, laneH := e.lanes()
	start, end := e.span()
	rate := float64(e.track.Format.SampleRate)
	fpp := e.framesPerPixel()
	raw := fpp < finest && e.raw.file == e.track.File && len(e.raw.data) > 0
	for ch := range 2 {
		mid := top + laneH*float32(ch) + laneH/2
		half := laneH/2 - 4
		p.RRect(geom.Rc(0, mid, box.W, 1), 0, paint.Solid(faded(ink, 0.06)))
		if raw && fpp < 2 {
			e.paintLine(p, ch, mid, half, box)
			continue
		}
		lv := e.level(fpp)
		k := e.gain()
		clip := func(v float32) float32 { return max(-half, min(v*half*k, half)) }
		// The columns keep to a grid in time, so each shows the same
		// stretch of sound as the view scrolls, by whole columns: off
		// it, a stretch's loudest moment falls now in one column, now
		// in the next, and the waveform shimmers.
		per := float64(e.v1.Value()-e.v0.Value()) / float64(box.W)
		first := math.Floor(float64(e.v0.Value()) / per)
		colT := func(x float32) float64 { return (first + float64(x)) * per }
		for x := float32(0); x < box.W; x++ {
			f0, f1 := int64(math.Round(colT(x)*rate)), int64(math.Round(colT(x+1)*rate))
			if f1 <= 0 || f0 >= e.track.Frames {
				continue
			}
			f0, f1 = max(0, f0), min(e.track.Frames, max(f1, f0+1))
			var lo, hi, rms float32
			if raw {
				lo, hi, rms = e.raw.column(ch, f0, f1)
			} else {
				lo, hi, rms = lv.column(ch, f0, f1)
			}
			if hi <= lo {
				continue
			}
			y0, y1 := mid-clip(hi), mid-clip(lo)
			p.RRect(geom.Rc(x, y0, 1, max(1, y1-y0)), 0, paint.Solid(faded(ink, 0.12)))
			tm := (colT(x) + colT(x+1)) / 2
			if tm < start || tm > end {
				continue
			}
			g := float32(e.envelope(tm))
			ey0, ey1 := mid-clip(hi*g), mid-clip(lo*g)
			r := clip(rms * g)
			p.RRect(geom.Rc(x, ey0, 1, max(1, ey1-ey0)), 0, paint.Solid(faded(teal, 0.55)))
			p.RRect(geom.Rc(x, mid-r, 1, 2*r), 0, paint.Solid(faded(mix(teal, ink, 0.4), 0.85)))
		}
	}
	// Outside the cut, the file is shaded away.
	e.paintOutside(p, box)
}

// paintLine draws channel ch's samples in view as the line they make,
// with a dot at each once they stand apart: the file faint, the sound
// as edited bright.
func (e *editor) paintLine(p *paint.Painter, ch int, mid, half float32, box geom.Size) {
	rate := float64(e.track.Format.SampleRate)
	k := e.gain()
	start, end := e.span()
	f0 := max(e.raw.from, int64(e.tAt(0)*rate)-1)
	f1 := min(e.raw.from+int64(len(e.raw.data)/2), int64(e.tAt(box.W)*rate)+2)
	apart := float64(box.W) / (float64(e.v1.Value()-e.v0.Value()) * rate)
	y := func(v float32) float32 { return mid - max(-half, min(v*half*k, half)) }
	var was, wasEd geom.Point
	for f := f0; f < f1; f++ {
		t := float64(f) / rate
		v := e.raw.data[2*(f-e.raw.from)+int64(ch)]
		x := e.xOf(t)
		pt := geom.Pt(x, y(v))
		g := float32(0)
		if t >= start && t <= end {
			g = float32(e.envelope(t))
		}
		ed := geom.Pt(x, y(v*g))
		if f > f0 {
			segment(p, was, pt, 1, faded(ink, 0.25))
			segment(p, wasEd, ed, 1.5, teal)
		}
		if apart > 8 {
			p.RRect(geom.Rc(ed.X-2, ed.Y-2, 4, 4), 2, paint.Solid(teal))
		}
		was, wasEd = pt, ed
	}
}

// level returns the finest level of the waveform no finer than a column
// of fpp frames.
func (e *editor) level(fpp float64) *Level {
	ls := e.track.Wave.Levels
	if len(ls) == 0 {
		return nil
	}
	lv := &ls[0]
	for i := range ls {
		if float64(ls[i].Per) <= fpp {
			lv = &ls[i]
		}
	}
	return lv
}

// column returns the lowest and highest sample of channel ch from frame
// f0 to f1, and their RMS.
func (l *Level) column(ch int, f0, f1 int64) (lo, hi, rms float32) {
	if l == nil {
		return 0, 0, 0
	}
	n := int64(len(l.Min[ch]))
	b0 := min(f0/int64(l.Per), n-1)
	b1 := min(max(b0, (f1-1)/int64(l.Per)), n-1)
	var ms float32
	for b := b0; b <= b1; b++ {
		lo, hi = min(lo, l.Min[ch][b]), max(hi, l.Max[ch][b])
		ms = max(ms, l.RMS[ch][b])
	}
	return lo, hi, ms
}

// column returns the lowest and highest sample of channel ch from frame
// f0 to f1, and their RMS, from the samples read.
func (r *rawSamples) column(ch int, f0, f1 int64) (lo, hi, rms float32) {
	end := r.from + int64(len(r.data)/2)
	f0, f1 = max(f0, r.from), min(f1, end)
	if f1 <= f0 {
		return 0, 0, 0
	}
	lo, hi = float32(math.Inf(1)), float32(math.Inf(-1))
	var ss float32
	for f := f0; f < f1; f++ {
		v := r.data[2*(f-r.from)+int64(ch)]
		lo, hi = min(lo, v), max(hi, v)
		ss += v * v
	}
	lo, hi = min(lo, 0), max(hi, 0)
	return lo, hi, float32(math.Sqrt(float64(ss / float32(f1-f0))))
}

// paintZoom draws the vertical zoom's slider up the editor's right edge:
// filled to how far the waveform is drawn louder, with its knob, and how
// many decibels, while it is under the pointer, held or up.
func (e *editor) paintZoom(p *paint.Painter, box geom.Size) {
	top, laneH := e.lanes()
	x := box.W - zoomW/2
	y0, y1 := top+8, top+2*laneH-8
	z := e.zoom.Value() / maxZoom
	on := e.hot == gripZoom || e.held == gripZoom
	alpha := float32(0.35)
	if on {
		alpha = 0.9
	}
	p.RRect(geom.Rc(x-2, y0, 4, y1-y0), 2, paint.Solid(faded(ink, 0.12*alpha+0.04)))
	ky := y1 - (y1-y0)*z
	p.RRect(geom.Rc(x-2, ky, 4, y1-ky), 2, paint.Solid(faded(sky, alpha)))
	r := float32(5)
	if on {
		r = 7
	}
	p.ShadowRRect(geom.Rc(x-r, ky-r, 2*r, 2*r), r, paint.Solid(faded(sky, max(alpha, 0.6))),
		paint.Shadow{Blur: 8, Color: faded(sky, 0.4*alpha)})
	if on || e.zoom.Value() > 0.5 {
		words := fmt.Sprintf("+%.0f dB", e.zoom.Value())
		run := shapedFace(words, 10, true, true)
		run.Paint(p, geom.Pt(x-run.Advance-12, ky-6), faded(sky, max(alpha, 0.7)))
	}
}

// paintFades draws each fade's curve over the lanes, the sound it
// takes away shaded above it, and its handle.
func (e *editor) paintFades(p *paint.Painter, box geom.Size) {
	top, laneH := e.lanes()
	start, end := e.span()
	in, out := e.handles()
	type fade struct {
		from, to float64
		handle   geom.Point
		g        grip
	}
	for _, f := range []fade{
		{start, start + e.edit.FadeIn.Length.Seconds(), in, gripFadeIn},
		{end - e.edit.FadeOut.Length.Seconds(), end, out, gripFadeOut},
	} {
		x0, x1 := e.xOf(f.from), e.xOf(f.to)
		if x1-x0 >= 1 {
			gain := math.Pow(10, float64(e.edit.Gain)/20)
			// A curve a channel over the waveform; one over the
			// spectrogram, which is of both.
			lanes := 2
			if e.r.state.View == ViewGram {
				lanes, laneH = 1, 2*laneH
			}
			for lane := range lanes {
				lt := top + laneH*float32(lane)
				var prev geom.Point
				for x := x0; x <= x1; x += 2 {
					g := float32(e.envelope(e.tAt(x)) / gain)
					y := lt + laneH*(1-min(g, 1))
					p.RRect(geom.Rc(x, lt, 2, y-lt), 0, paint.Solid(faded(night, 0.35)))
					pt := geom.Pt(x, y)
					if x > x0 {
						segment(p, prev, pt, 2, amber)
					}
					prev = pt
				}
			}
		}
		// The handle, larger under the pointer or held.
		r := float32(6)
		if e.hot == f.g || e.held == f.g {
			r = 8
		}
		h := f.handle
		p.ShadowRRect(geom.Rc(h.X-r, h.Y-r, 2*r, 2*r), r, paint.Solid(amber), paint.Shadow{Blur: 10, Color: faded(amber, 0.5)})
		if e.held == f.g || e.hot == f.g {
			length := e.edit.FadeIn.Length
			if f.g == gripFadeOut {
				length = e.edit.FadeOut.Length
			}
			e.bubble(p, box, geom.Pt(h.X, h.Y+16), fmt.Sprintf("%.2f s", length.Seconds()), amber)
		}
	}
}

// paintCut draws the cut's start and end as lines across the lanes,
// each with a tab to take it by.
func (e *editor) paintCut(p *paint.Painter, box geom.Size) {
	start, end := e.span()
	for _, c := range []struct {
		t float64
		g grip
	}{{start, gripStart}, {end, gripEnd}} {
		x := e.xOf(c.t)
		w := float32(2)
		if e.hot == c.g || e.held == c.g {
			w = 3
		}
		p.RRect(geom.Rc(x-w/2, rulerH, w, box.H-rulerH), w/2, paint.Solid(teal))
		tab := geom.Rc(x-7, box.H-22, 14, 18)
		p.RRect(tab, 5, paint.Solid(teal))
		if e.held == c.g || e.hot == c.g {
			e.bubble(p, box, geom.Pt(x, box.H-44), clock(time.Duration(c.t*float64(time.Second))), teal)
		}
	}
}

// paintPlayhead draws where the speakers are.
func (e *editor) paintPlayhead(p *paint.Painter, box geom.Size) {
	t, ok := e.playhead()
	if !ok {
		return
	}
	x := e.xOf(t)
	if x < -2 || x > box.W+2 {
		return
	}
	p.ShadowRRect(geom.Rc(x-1, 0, 2, box.H), 1, paint.Solid(ink), paint.Shadow{Blur: 8, Color: faded(ink, 0.5)})
	p.RRect(geom.Rc(x-5, 0, 10, 8), 3, paint.Solid(ink))
}

// bubble draws words in a small pill at about at.
func (e *editor) bubble(p *paint.Painter, box geom.Size, at geom.Point, words string, c color.NRGBA) {
	run := shapedFace(words, 11, true, true)
	w, h := run.Advance+16, float32(22)
	x := max(4, min(at.X-w/2, box.W-w-4))
	p.RRect(geom.Rc(x, at.Y, w, h), h/2, paint.Solid(faded(night, 0.92)))
	p.RRectStroke(geom.Rc(x, at.Y, w, h), h/2, paint.Solid(color.NRGBA{}), paint.Stroke{Width: 1, Color: faded(c, 0.8)})
	run.Paint(p, geom.Pt(x+8, at.Y+4), ink)
}

// segment draws a straight line from a to b, width wide.
func segment(p *paint.Painter, a, b geom.Point, width float32, c color.NRGBA) {
	d := b.Sub(a)
	l := float32(math.Hypot(float64(d.X), float64(d.Y)))
	if l < 0.01 {
		return
	}
	end := p.Push(paint.Rotate(float32(math.Atan2(float64(d.Y), float64(d.X))), a))
	p.RRect(geom.Rc(a.X-width/2, a.Y-width/2, l+width, width), width/2, paint.Solid(c))
	end()
}

// Cursor implements [gunim.CursorShaper]: the ruler and the cut's ends move
// across, the slider up and down.
func (e *editor) Cursor(p geom.Point) input.Cursor {
	g := e.held
	if g == gripNone {
		g = e.gripAt(p)
	}
	switch g {
	case gripRuler, gripStart, gripEnd, gripSilence:
		return input.CursorResizeH
	case gripZoom:
		return input.CursorResizeV
	case gripNone, gripFadeIn, gripFadeOut, gripSeek:
	}
	return input.CursorInherit
}

// stepFling follows a drag of the ruler, how fast it moves the view,
// and, once it is let go moving, glides the view on, slowing, to a stop
// or the sound's end. It returns whether the view glides.
func (e *editor) stepFling(dt time.Duration) bool {
	sec := dt.Seconds()
	if e.held == gripRuler && sec > 0 {
		v0 := e.v0.Value()
		if v0 != e.wasV0 {
			v := float64(v0-e.wasV0) / sec
			e.dragV = 0.6*e.dragV + 0.4*v
			e.still = 0
		} else {
			e.still += dt
		}
		e.wasV0 = v0
		return false
	}
	e.wasV0 = e.v0.Value()
	if e.fling == 0 || sec <= 0 {
		return false
	}
	span := float64(e.v1.Value() - e.v0.Value())
	lo, hi := e.fit()
	shift := e.fling * sec
	v0 := float64(e.v0.Value()) + shift
	// The sound's ends stop it.
	if v0 < lo || v0+span > hi {
		v0 = max(lo, min(v0, hi-span))
		e.fling = 0
	}
	e.v0.Jump(float32(v0))
	e.v1.Jump(float32(v0 + span))
	e.fling *= math.Exp(-4 * sec)
	if math.Abs(e.fling) < span*0.02 {
		e.fling = 0
	}
	e.wasV0 = e.v0.Value()
	return e.fling != 0
}
