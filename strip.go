package main

import (
	"image/color"
	"math"
	"strconv"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// strip is the album as it plays, end to end: each track's silence and
// its sound, as long as they last, its waveform in the colour of its
// loudness against the target; the track picked lit, and the playhead
// in it. A click plays a track from there.
type strip struct {
	anim.Group
	r *root
	// x0 and x1 are where each track lies, from 0 to 1 of the strip,
	// by its ID, gliding as tracks change.
	x0, x1 map[int]*anim.Float
	thumbs map[int][]float32
	of     map[int]*Wave
	size   geom.Size
}

func newStrip(r *root) *strip {
	return &strip{r: r, x0: map[int]*anim.Float{}, x1: map[int]*anim.Float{}, thumbs: map[int][]float32{}, of: map[int]*Wave{}}
}

// lengthOf is how long a track plays, as measured, or as its file
// suggests until it is.
func lengthOf(t Track, gap time.Duration) time.Duration {
	if t.Measured {
		return t.Measure.Length
	}
	if t.Format.SampleRate > 0 {
		return gap + duration(t.Frames, t.Format.SampleRate)
	}
	return gap + time.Minute
}

func (s *strip) show(a Album) {
	var total time.Duration
	for _, t := range a.Tracks {
		total += lengthOf(t, a.Gap)
	}
	var at time.Duration
	for _, t := range a.Tracks {
		l := lengthOf(t, a.Gap)
		from, to := float32(at.Seconds()/max(total.Seconds(), 1)), float32((at+l).Seconds()/max(total.Seconds(), 1))
		at += l
		if s.x0[t.ID] == nil {
			s.x0[t.ID], s.x1[t.ID] = anim.NewFloat(from), anim.NewFloat(to)
			s.Add(s.x0[t.ID], s.x1[t.ID])
		}
		s.x0[t.ID].Animate(from, anim.Spring{Response: 0.4, Damping: 0.9})
		s.x1[t.ID].Animate(to, anim.Spring{Response: 0.4, Damping: 0.9})
		if t.Wave != nil && s.of[t.ID] != t.Wave {
			s.thumbs[t.ID], s.of[t.ID] = thumbnail(t.Wave, 160), t.Wave
		}
	}
}

// Step implements [gunim.Animator]: the playhead moves while a track
// plays.
func (s *strip) Step(dt time.Duration) bool { return s.Group.Step(dt) || s.r.state.Playing }

// Handle implements [gunim.Handler]: a click plays the track under it
// from there.
func (s *strip) Handle(e input.Event, u *gunim.UI) bool {
	d, ok := e.(input.PointerDown)
	if !ok || d.Button != input.ButtonPrimary {
		return false
	}
	u0 := d.Pos.X / s.size.W
	for _, t := range s.r.state.Tracks {
		a, b := s.x0[t.ID], s.x1[t.ID]
		if a == nil || u0 < a.Value() || u0 >= b.Value() {
			continue
		}
		frac := float64((u0 - a.Value()) / max(b.Value()-a.Value(), 1e-6))
		at := time.Duration(frac * float64(lengthOf(t, s.r.state.Gap)))
		u.Cue(gunim.CueSelect, s)
		if t.ID != s.r.state.Current {
			u.Send(s, Pick{ID: t.ID})
		}
		u.Send(s, SeekTo{At: at})
		return true
	}
	return false
}

// Layout implements [gunim.Node].
func (s *strip) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	s.size = c.Max
	return c.Max
}

// Paint implements [gunim.Node].
func (s *strip) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, _ gunim.Children) {
	whole := geom.Rect{Max: box.Point()}
	p.RRect(whole, 16, paint.Solid(panel))
	a := s.r.state
	shaped("ALBUM", 10, true).Paint(p, geom.Pt(14, 10), faded(teal, 0.85))
	band := geom.Rc(10, 28, box.W-20, box.H-38)
	at, _, playing := s.r.d.position()
	for i, t := range a.Tracks {
		x0, x1 := s.x0[t.ID], s.x1[t.ID]
		if x0 == nil {
			continue
		}
		left := band.Min.X + band.Size().W*x0.Value()
		right := band.Min.X + band.Size().W*x1.Value()
		length := lengthOf(t, a.Gap).Seconds()
		gapX := left + (right-left)*float32(a.Gap.Seconds()/max(length, 0.001))
		// The silence, then the sound.
		p.RRect(geom.Rc(left, band.Min.Y, max(gapX-left, 0), band.Size().H), 0, paint.Solid(faded(sky, 0.05)))
		block := geom.Rc(gapX, band.Min.Y, max(right-gapX-2, 1), band.Size().H)
		c := faded(ink, 0.3)
		if t.Measured && t.Measure.Loud {
			c = loudnessColor(t.Measure.LUFS - a.Target)
		}
		picked := t.ID == a.Current
		fill := float32(0.12)
		if picked {
			fill = 0.22
		}
		p.RRect(block, 6, paint.Solid(faded(c, fill)))
		if th := s.thumbs[t.ID]; th != nil {
			w := block.Size().W / float32(len(th))
			mid := block.Min.Y + block.Size().H/2
			for k, v := range th {
				h := max(1, (block.Size().H/2-6)*min(float32(math.Sqrt(float64(v)))*1.4, 1))
				p.RRect(geom.Rc(block.Min.X+float32(k)*w, mid-h, max(w-0.5, 0.5), 2*h), 0, paint.Solid(faded(c, 0.55)))
			}
		}
		if picked {
			p.RRectStroke(block, 6, paint.Solid(color.NRGBA{}), paint.Stroke{Width: 1.5, Color: c})
		}
		if block.Size().W > 24 {
			shaped(strconv.Itoa(i+1), 10, true).Paint(p, block.Min.Add(geom.Pt(5, 4)), faded(ink, 0.8))
		}
		if playing == t.ID {
			x := left + (right-left)*float32(at.Seconds()/max(length, 0.001))
			p.ShadowRRect(geom.Rc(x-1, band.Min.Y-3, 2, band.Size().H+6), 1, paint.Solid(ink), paint.Shadow{Blur: 6, Color: faded(ink, 0.5)})
		}
	}
}
