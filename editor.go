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
	// inFrom and outFrom are the fades' curves before their last
	// change, and inMorph and outMorph how far they have turned into the
	// new ones.
	inFrom, outFrom   Curve
	inMorph, outMorph *anim.Float
	glow              *anim.Float
	size              geom.Size
}

func newEditor(r *root) *editor {
	e := &editor{r: r, v0: anim.NewFloat(-1), v1: anim.NewFloat(10), inMorph: anim.NewFloat(1),
		outMorph: anim.NewFloat(1), glow: anim.NewFloat(0)}
	e.Add(e.v0, e.v1, e.inMorph, e.outMorph, e.glow)
	return e
}

// length is the file's length, in seconds.
func (e *editor) length() float64 {
	if e.track.Format.SampleRate == 0 {
		return 1
	}
	return float64(e.track.Frames) / float64(e.track.Format.SampleRate)
}

// gap is the album's silence before every track, in seconds.
func (e *editor) gap() float64 { return e.r.state.Gap.Seconds() }

// fit returns the view showing the whole file and the silence before
// its cut.
func (e *editor) fit() (v0, v1 float64) {
	l := e.length()
	from := min(0, e.edit.Start.Seconds()-e.gap())
	pad := (l - from) * 0.02
	return from - pad, l + pad
}

func (e *editor) show(was Album, t Track) {
	fresh := t.ID != e.track.ID || t.Scanned != e.track.Scanned
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
	switch {
	case near(in):
		return gripFadeIn
	case near(out):
		return gripFadeOut
	case p.Y > rulerH && math.Abs(float64(p.X-e.xOf(start))) < 7:
		return gripStart
	case p.Y > rulerH && math.Abs(float64(p.X-e.xOf(end))) < 7:
		return gripEnd
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
		if ev.Clicks == 2 && e.gripAt(ev.Pos) == gripSeek {
			v0, v1 := e.fit()
			e.v0.Animate(float32(v0), anim.Gentle)
			e.v1.Animate(float32(v1), anim.Gentle)
			break
		}
		e.held = e.gripAt(ev.Pos)
		e.heldAt = e.tAt(ev.Pos.X)
		if e.held == gripSeek {
			u.Send(e, SeekTo{At: e.renderTime(e.tAt(ev.Pos.X))})
		}
	case input.PointerUp:
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
	v0, v1 := float64(e.v0.Target()), float64(e.v1.Target())
	span := v1 - v0
	if ev.Mods.Has(input.ModShift) || ev.Delta.X != 0 {
		d := float64(ev.Delta.Y+ev.Delta.X) / float64(e.size.W) * span
		e.v0.Animate(float32(v0-d), anim.Spring{Response: 0.2, Damping: 1})
		e.v1.Animate(float32(v1-d), anim.Spring{Response: 0.2, Damping: 1})
		return
	}
	n := float64(ev.Notches.Y)
	if n == 0 {
		n = float64(ev.Delta.Y) / 40
	}
	k := math.Pow(1.25, -n)
	f0, f1 := e.fit()
	k = max(0.05/span, min(k, (f1-f0)*1.2/span))
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

// Step implements [gunim.Animator]: the playhead moves while a track
// plays, and the view follows it past the edge.
func (e *editor) Step(dt time.Duration) bool {
	moving := e.Group.Step(dt)
	if !e.r.state.Playing {
		return moving
	}
	if t, ok := e.playhead(); ok && e.held == gripNone {
		v0, v1 := float64(e.v0.Target()), float64(e.v1.Target())
		if t > v1 || t < v0 {
			span := v1 - v0
			to := t - span*0.1
			e.v0.Animate(float32(to), anim.Gentle)
			e.v1.Animate(float32(to+span), anim.Gentle)
		}
	}
	return true
}

// playhead returns the file's time the speakers play, and false while
// the track picked is not the one playing.
func (e *editor) playhead() (float64, bool) {
	at, _, id := e.r.d.position()
	if id != e.track.ID || id == 0 {
		return 0, false
	}
	start, _ := e.span()
	return start + at.Seconds() - e.gap(), true
}

// Layout implements [gunim.Node].
func (e *editor) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	e.size = c.Max
	return c.Max
}

// Paint implements [gunim.Node].
func (e *editor) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, _ gunim.Children) {
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
	e.paintWave(p, box)
	e.paintFades(p, box)
	e.paintCut(p, box)
	e.paintPlayhead(p, box)
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
	run := shaped(label, 11, true)
	if run.Advance < x1-x0-8 {
		run.Paint(p, geom.Pt((x0+x1-run.Advance)/2, top+8), faded(sky, 0.85))
	}
}

// paintWave draws both channels' waveforms, a column a pixel: the file
// faint, and the sound as edited bright over it.
func (e *editor) paintWave(p *paint.Painter, box geom.Size) {
	w := e.track.Wave
	top, laneH := e.lanes()
	length := e.length()
	buckets := len(w.Peak[0])
	start, end := e.span()
	for ch := range 2 {
		mid := top + laneH*float32(ch) + laneH/2
		half := laneH/2 - 4
		p.RRect(geom.Rc(0, mid, box.W, 1), 0, paint.Solid(faded(ink, 0.06)))
		for x := float32(0); x < box.W; x++ {
			t0, t1 := e.tAt(x), e.tAt(x+1)
			if t1 < 0 || t0 > length {
				continue
			}
			b0 := max(0, int(t0/length*float64(buckets)))
			b1 := min(buckets-1, max(b0, int(t1/length*float64(buckets))))
			var peak, rms float32
			for b := b0; b <= b1; b++ {
				peak = max(peak, w.Peak[ch][b])
				rms = max(rms, w.RMS[ch][b])
			}
			if peak <= 0 {
				continue
			}
			ph := max(0.5, peak*half)
			p.RRect(geom.Rc(x, mid-ph, 1, 2*ph), 0, paint.Solid(faded(ink, 0.12)))
			tm := (t0 + t1) / 2
			if tm < start || tm > end {
				continue
			}
			g := float32(e.envelope(tm))
			eh := min(ph*g, half)
			rh := min(rms*half*g, half)
			p.RRect(geom.Rc(x, mid-eh, 1, 2*eh), 0, paint.Solid(faded(teal, 0.55)))
			p.RRect(geom.Rc(x, mid-rh, 1, 2*rh), 0, paint.Solid(faded(mix(teal, ink, 0.4), 0.85)))
		}
	}
	// Outside the cut, the file is shaded away.
	sx, ex := e.xOf(start), e.xOf(end)
	if sx > 0 {
		p.RRect(geom.Rc(0, rulerH+1, sx, box.H-rulerH), 0, paint.Solid(faded(night, 0.35)))
	}
	if ex < box.W {
		p.RRect(geom.Rc(ex, rulerH+1, box.W-ex, box.H-rulerH), 0, paint.Solid(faded(night, 0.45)))
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
			for lane := range 2 {
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
