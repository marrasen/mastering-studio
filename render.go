package main

import (
	"io"
	"math"
	"time"

	"github.com/marrasen/gunim/audio"
)

// Curve is the shape a fade takes.
type Curve int

// The shapes of fade.
const (
	// Linear rises in a straight line.
	Linear Curve = iota
	// Natural rises evenly in decibels, as the ear hears loudness, from
	// 60 dB down: the shape a long fade-out sounds smoothest in.
	Natural
	// Smooth eases in and out, an S.
	Smooth
	// Fast rises quickly and settles, as an equal-power fade does.
	Fast
	// Slow starts gently and rises late.
	Slow
)

// curveNames names the shapes, in their order.
var curveNames = []string{"Linear", "Natural", "Smooth", "Fast", "Slow"}

// at returns a fade's gain t of the way in, from 0 to 1, rising.
func (c Curve) at(t float64) float64 {
	t = max(0, min(t, 1))
	switch c {
	case Natural:
		// 60 dB of range, tapered to silence over its last tenth.
		g := math.Pow(10, -3*(1-t))
		if t < 0.1 {
			g *= t / 0.1
		}
		return g
	case Smooth:
		return 0.5 - 0.5*math.Cos(math.Pi*t)
	case Fast:
		return math.Sin(math.Pi / 2 * t)
	case Slow:
		return t * t * t
	case Linear:
	}
	return t
}

// Fade is a fade in or out: how long, and its shape.
type Fade struct {
	Length time.Duration
	Curve  Curve
}

// Edit is what is done to a track's file: where it is cut, how it
// fades in and out, and its gain.
type Edit struct {
	// Start and End are where the track is cut, in its file; End of
	// zero is the file's end.
	Start, End      time.Duration
	FadeIn, FadeOut Fade
	// Gain is the gain into the chain, and Out the gain after it, in
	// decibels.
	Gain float32
	Out  float32 `json:",omitempty"`
}

// span returns the edit's start and end, in frames of a file of length
// frames at rate.
func (e Edit) span(rate int, length int64) (start, end int64) {
	start = max(0, min(frames(e.Start, rate), length))
	end = length
	if e.End > 0 {
		end = max(start, min(frames(e.End, rate), length))
	}
	return start, end
}

func frames(d time.Duration, rate int) int64 { return int64(math.Round(d.Seconds() * float64(rate))) }

func duration(n int64, rate int) time.Duration {
	return time.Duration(float64(n) / float64(rate) * float64(time.Second))
}

// render plays a track as it will be exported, at its file's rate: the
// album's silence before it, then the file from the edit's start to its
// end, faded in and out and at its gain. Playing, measuring and
// exporting all read it, so what is measured is what is exported.
type render struct {
	src            audio.Seeker
	rate           int
	gap            int64
	start, end     int64
	fadeIn, fadeOu int64
	inCurve, outCv Curve
	gain           float32
	// at is the next frame, counted from the start of the silence.
	at int64
}

func newRender(src audio.Seeker, rate int, gap time.Duration, e Edit) *render {
	start, end := e.span(rate, src.Len())
	r := &render{src: src, rate: rate, gap: frames(gap, rate), start: start, end: end,
		fadeIn: min(frames(e.FadeIn.Length, rate), end-start), fadeOu: min(frames(e.FadeOut.Length, rate), end-start),
		inCurve: e.FadeIn.Curve, outCv: e.FadeOut.Curve, gain: float32(math.Pow(10, float64(e.Gain)/20))}
	_ = src.SeekFrame(start)
	return r
}

// Len implements [audio.Seeker].
func (r *render) Len() int64 { return r.gap + r.end - r.start }

// SeekFrame implements [audio.Seeker].
func (r *render) SeekFrame(f int64) error {
	r.at = max(0, min(f, r.Len()))
	return r.src.SeekFrame(r.start + max(0, r.at-r.gap))
}

// Read implements [audio.Source].
func (r *render) Read(dst []float32) (int, error) {
	want := len(dst) / 2
	n := 0
	for n < want {
		if r.at >= r.Len() {
			return n, io.EOF
		}
		if r.at < r.gap {
			// The silence before the track.
			k := int(min(int64(want-n), r.gap-r.at))
			clear(dst[2*n : 2*(n+k)])
			n += k
			r.at += int64(k)
			if r.at == r.gap {
				_ = r.src.SeekFrame(r.start)
			}
			continue
		}
		k := int(min(int64(want-n), r.Len()-r.at))
		got, err := r.src.Read(dst[2*n : 2*(n+k)])
		for i := range got {
			g := r.gain * r.envelope(r.at+int64(i)-r.gap)
			dst[2*(n+i)] *= g
			dst[2*(n+i)+1] *= g
		}
		n += got
		r.at += int64(got)
		if r.at < r.Len() && (err != nil || got == 0) {
			// A file shorter than it said: the rest is silence.
			k := int(min(int64(want-n), r.Len()-r.at))
			clear(dst[2*n : 2*(n+k)])
			n += k
			r.at += int64(k)
		}
	}
	return n, nil
}

// envelope is the fades' gain at frame f of the cut track.
func (r *render) envelope(f int64) float32 {
	g := 1.0
	if f < r.fadeIn {
		g = r.inCurve.at(float64(f) / float64(r.fadeIn))
	}
	if left := r.end - r.start - f; left < r.fadeOu {
		g *= r.outCv.at(float64(left) / float64(r.fadeOu))
	}
	return float32(g)
}
