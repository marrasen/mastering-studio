package main

import (
	"fmt"
	"math"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// The meters' input and output, as a mastering suite shows them: what
// goes into the track's chain, after the gain in, and what comes out,
// after the gain out, each a pair of bars, left and right, of their
// peak and RMS, the peak held a moment, a fader beside them for the
// gain, and over them the peak and the short-term loudness.

// ioLevels are a pair of channels' levels as the bars show them, in
// decibels.
type ioLevels struct {
	peak, rms, hold [2]float32
	held            [2]time.Duration
	// ms is each channel's mean square over the last 300 ms or so, as a
	// meter's RMS is taken: over a frame's sound alone it flickers.
	ms [2]float64
	// top is the highest peak since the track started, of each channel.
	top [2]float32
	lm  *audio.LoudnessMeter
	// rate is the rate lm measures at.
	rate int
}

func newIOLevels() ioLevels {
	l := ioLevels{}
	for ch := range 2 {
		l.peak[ch], l.rms[ch], l.hold[ch], l.top[ch] = -90, -90, -90, -90
	}
	return l
}

// take takes frames heard, at rate: their peaks rise at once, their RMS
// eases, and both fall back as the sound quietens.
func (l *ioLevels) take(frames []float32, rate int, dt time.Duration) {
	if l.lm == nil || l.rate != rate {
		l.lm, l.rate = audio.NewLoudnessMeter(rate), rate
	}
	l.lm.Write(frames)
	sec := float32(dt.Seconds())
	for ch := range 2 {
		var peak, ss float32
		n := 0
		for i := ch; i < len(frames); i += 2 {
			v := frames[i]
			peak = max(peak, float32(math.Abs(float64(v))))
			ss += v * v
			n++
		}
		p := float32(-90)
		if n > 0 {
			p = float32(dB(float64(peak)))
		}
		// The peak at once, falling at 12 dB a second; the RMS over its
		// window.
		l.peak[ch] = max(p, l.peak[ch]-12*sec)
		if n > 0 {
			k := 1 - math.Exp(-dt.Seconds()/0.3)
			l.ms[ch] += (float64(ss/float32(n)) - l.ms[ch]) * k
		}
		l.rms[ch] = float32(dB(math.Sqrt(l.ms[ch])))
		l.top[ch] = max(l.top[ch], p)
		// The hold, for a second and a half, then falling.
		if p >= l.hold[ch] {
			l.hold[ch], l.held[ch] = p, 0
		} else if l.held[ch] += dt; l.held[ch] > 1500*time.Millisecond {
			l.hold[ch] = max(l.peak[ch], l.hold[ch]-30*sec)
		}
	}
}

// quiet lets the levels fall, as nothing new is heard: the loudness
// keeps what it measured.
func (l *ioLevels) quiet(dt time.Duration) {
	sec := float32(dt.Seconds())
	for ch := range 2 {
		l.peak[ch] = max(-90, l.peak[ch]-12*sec)
		l.ms[ch] *= math.Exp(-dt.Seconds() / 0.3)
		l.rms[ch] = float32(dB(math.Sqrt(l.ms[ch])))
		if l.held[ch] += dt; l.held[ch] > 1500*time.Millisecond {
			l.hold[ch] = max(l.peak[ch], l.hold[ch]-30*sec)
		}
	}
}

// meterAt is where on a meter, from 0 at the bottom to 1 at the top, a
// level in decibels is: the top of the scale given more room, as a
// mastering meter gives it.
func meterAt(db float32) float32 {
	u := (max(db, -60) + 60) / 60
	return float32(math.Pow(float64(u), 1.8))
}

// meterTicks are the levels the scale is marked at.
var meterTicks = []float32{0, -3, -6, -10, -15, -20, -30, -40, -60}

// ioFader is the gain in or out, a fader beside its meter: a drag sets
// it, the wheel steps it, a double-click sets it to 0 dB.
type ioFader struct {
	anim.Group
	r     *root
	out   bool
	hover *anim.Float
	held  bool
	from  geom.Point
	start float32
	size  geom.Size
}

func newIOFader(r *root, out bool) *ioFader {
	f := &ioFader{r: r, out: out, hover: anim.NewFloat(0)}
	f.Add(f.hover)
	return f
}

// faderRange is how far a fader goes either way, in decibels.
const faderRange = 24

func (f *ioFader) value() float32 {
	if f.out {
		return f.r.editor.edit.Out
	}
	return f.r.editor.edit.Gain
}

func (f *ioFader) set(v float32, u *gunim.UI) {
	v = max(-faderRange, min(v, faderRange))
	v = float32(math.Round(float64(v)*10) / 10)
	if v == f.value() || f.r.editor.track.ID == 0 {
		return
	}
	e := f.r.editor.edit
	if f.out {
		e.Out = v
	} else {
		e.Gain = v
	}
	f.r.editor.send(e, u)
}

// Focusable implements [gunim.Focusable].
func (f *ioFader) Focusable() bool { return false }

// DragsTouch implements [gunim.TouchDragger].
func (f *ioFader) DragsTouch() bool { return f.held }

// Handle implements [gunim.Handler].
func (f *ioFader) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerEnter:
		f.hover.Animate(1, anim.Snappy)
	case input.PointerLeave:
		if !f.held {
			f.hover.Animate(0, anim.Gentle)
		}
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		if e.Clicks == 2 {
			f.set(0, u)
			break
		}
		f.held, f.from, f.start = true, e.Pos, f.value()
	case input.PointerMove:
		if !f.held {
			return false
		}
		per := 2 * faderRange / max(f.size.H-24, 1)
		if e.Mods.Has(input.ModShift) {
			per /= 10
		}
		f.set(f.start-(e.Pos.Y-f.from.Y)*per, u)
	case input.PointerUp:
		f.held = false
	case input.Scroll:
		n := e.Notches.Y
		if n == 0 {
			n = e.Delta.Y / 40
		}
		f.set(f.value()+0.5*n, u)
	default:
		return false
	}
	u.Invalidate()
	return true
}

// Layout implements [gunim.Node].
func (f *ioFader) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	f.size = c.Max
	return c.Max
}

// Paint implements [gunim.Node]: the fader's track, its 0 dB marked,
// and its cap, at the gain.
func (f *ioFader) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, _ gunim.Children) {
	mid := box.W / 2
	top, bottom := float32(12), box.H-12
	p.RRect(geom.Rc(mid-2, top, 4, bottom-top), 2, paint.Solid(faded(night, 0.9)))
	zero := top + (bottom-top)/2
	p.RRect(geom.Rc(mid-7, zero, 14, 1), 0, paint.Solid(faded(ink, 0.3)))
	v := f.value()
	y := zero - (bottom-top)/2*v/faderRange
	lit := f.hover.Value()
	if f.held {
		lit = 1
	}
	knob := geom.Rc(mid-box.W/2+2, y-9, box.W-4, 18)
	p.ShadowRRect(knob, 5, paint.Solid(mix(raised, mix(raised, ink, 0.25), lit)),
		paint.Shadow{Blur: 6, Color: faded(night, 0.6)})
	p.RRect(geom.Rc(knob.Min.X+5, y-0.75, knob.Size().W-10, 1.5), 0.75, paint.Solid(faded(ink, 0.8)))
}

// paintIO draws the input's and output's meters, their faders placed by
// Layout, from y, and returns where they end.
func (m *meters) paintIO(p *paint.Painter, box geom.Size, y float32) float32 {
	shaped("IN", 10, true).Paint(p, geom.Pt(16, y), faded(teal, 0.85))
	run := shaped("OUT", 10, true)
	run.Paint(p, geom.Pt(box.W-16-run.Advance, y), faded(teal, 0.85))
	ioBars := m.ioArea(box)
	top, bottom := ioBars.Min.Y, ioBars.Max.Y
	yOf := func(db float32) float32 { return bottom - (bottom-top)*meterAt(db) }
	// The scale, between the two.
	for _, t := range meterTicks {
		label := fmt.Sprintf("%.0f", t)
		if t == -60 {
			label = "-inf"
		}
		r := shapedFace(label, 9, false, true)
		r.Paint(p, geom.Pt(box.W/2-r.Advance/2, yOf(t)-6), faded(ink, 0.4))
	}
	for side, l := range []*ioLevels{&m.in, &m.out} {
		x := m.barsX(box, side == 1)
		// Over the bars: the highest peaks, and the loudness.
		for ch := range 2 {
			c := faded(ink, 0.7)
			if l.top[ch] > -1 {
				c = coral
			}
			words := "—"
			if l.top[ch] > -89 {
				words = fmt.Sprintf("%.1f", l.top[ch])
			}
			r := shapedFace(words, 8, true, true)
			r.Paint(p, geom.Pt(x+float32(ch)*barPitch+barW/2-r.Advance/2, top-34), c)
		}
		loud := "—"
		if l.lm != nil {
			if s := l.lm.ShortTerm(); s > -70 {
				loud = fmt.Sprintf("%.1f", s)
			}
		}
		r := shapedFace(loud, 11, true, true)
		r.Paint(p, geom.Pt(x+(barPitch+barW)/2-r.Advance/2, top-20), ink)
		for ch := range 2 {
			bx := x + float32(ch)*barPitch
			p.RRect(geom.Rc(bx, top, barW, bottom-top), 2, paint.Solid(faded(night, 0.85)))
			// The RMS solid, the peak over it fainter, the hold a line.
			ry, py := yOf(l.rms[ch]), yOf(l.peak[ch])
			if l.peak[ch] > -59 {
				p.RRect(geom.Rc(bx, py, barW, bottom-py), 2, paint.Solid(faded(ink, 0.3)))
			}
			if l.rms[ch] > -59 {
				p.RRect(geom.Rc(bx, ry, barW, bottom-ry), 2, paint.Solid(faded(rgb(0xc8, 0xcd, 0xd8), 0.9)))
			}
			if l.hold[ch] > -59 {
				hc := ink
				if l.hold[ch] > -0.1 {
					hc = coral
				}
				p.RRect(geom.Rc(bx, yOf(l.hold[ch])-1, barW, 2), 1, paint.Solid(hc))
			}
		}
	}
	// The faders' values, under them.
	for side, f := range []*ioFader{m.inFader, m.outFader} {
		fx := m.faderX(box, side == 1)
		r := shapedFace(fmt.Sprintf("%+.1f", f.value()), 9, false, true)
		r.Paint(p, geom.Pt(fx+12-r.Advance/2, bottom+6), faded(ink, 0.7))
	}
	return bottom + 22
}

// The I/O section's places: its bars' area from the top to the bottom,
// and the bars' and faders' left edges.
func (m *meters) ioArea(box geom.Size) geom.Rect { return geom.Rc(0, 16+56, box.W, 150) }

// A meter's bars: each barW wide, barPitch from one to the next.
const barW, barPitch = 18, 28

func (m *meters) barsX(box geom.Size, out bool) float32 {
	if out {
		return box.W - 16 - 10 - barPitch - barW
	}
	return 16 + 10
}

func (m *meters) faderX(box geom.Size, out bool) float32 {
	if out {
		return m.barsX(box, true) - 36
	}
	return m.barsX(box, false) + barPitch + barW + 12
}
