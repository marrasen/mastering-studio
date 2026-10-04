package main

import (
	"fmt"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// The meters' input and output, as a mastering suite shows them: what
// goes into the track's chain, after the gain in, and what comes out,
// after the gain out, each a pair of bars, left and right, of their
// peak and RMS, the peak held a moment, a fader beside them for the
// gain, and over them the peak and the short-term loudness.

// paintIO draws the input's and output's meters, their faders placed by
// Layout, from y, and returns where they end.
func (m *meters) paintIO(p *paint.Painter, f gunim.Frame, box geom.Size, y float32) float32 {
	shaped("IN", 10, true).Paint(p, geom.Pt(16, y), faded(teal, 0.85))
	run := shaped("OUT", 10, true)
	run.Paint(p, geom.Pt(box.W-16-run.Advance, y), faded(teal, 0.85))
	bars := m.ioArea(box)
	top, bottom := bars.Min.Y, bars.Max.Y
	// The scale, between the two.
	audioui.PaintMeterScale(p, f.Theme, box.W/2, top, bottom)
	for side, l := range []*audioui.Levels{&m.in, &m.out} {
		x := m.barsX(box, side == 1)
		audioui.PaintMeter(p, f.Theme, geom.Rc(x, top, audioui.MeterW, bottom-top), l)
	}
	// The faders' values, under them.
	for side, fd := range []*audioui.Fader{m.inFader, m.outFader} {
		fx := m.faderX(box, side == 1)
		r := shapedFace(fmt.Sprintf("%+.1f", fd.Value()), 9, false)
		r.Paint(p, geom.Pt(fx+12-r.Advance/2, bottom+6), faded(ink, 0.7))
	}
	return bottom + 22
}

// The I/O section's places: its bars' area from the top to the bottom,
// and the bars' and faders' left edges.
func (m *meters) ioArea(box geom.Size) geom.Rect { return geom.Rc(0, 16+56, box.W, 150) }

func (m *meters) barsX(box geom.Size, out bool) float32 {
	if out {
		return box.W - 16 - 10 - audioui.MeterW
	}
	return 16 + 10
}

func (m *meters) faderX(box geom.Size, out bool) float32 {
	if out {
		return m.barsX(box, true) - 36
	}
	return m.barsX(box, false) + audioui.MeterW + 12
}
