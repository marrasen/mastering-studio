package dynamics

import (
	"fmt"
	"image/color"
	"math"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
	"github.com/marrasen/mastering-studio/plug"
	"github.com/marrasen/mastering-studio/plug/plugui"
)

// The history's colours: the sound in, how far the compressor and
// limiter turn it down, and how far the gate does. The sound out is the
// theme's accent.
var (
	HistoryIn   = theme.Color("dynamics.history.in", color.NRGBA{R: 0x9a, G: 0xa3, B: 0xb6, A: 0xc0})
	HistoryCut  = theme.Color("dynamics.history.cut", color.NRGBA{R: 0xff, G: 0x5a, B: 0x4f, A: 0xff})
	HistoryGate = theme.Color("dynamics.history.gate", color.NRGBA{R: 0x6c, G: 0x7c, B: 0xff, A: 0xff})
)

// The history: a column for each colFrames frames, histCols of them
// kept and shownCols across; levels from histTop down to histBottom.
const (
	colFrames           = 2 * plug.TraceFrames
	histCols            = 1024
	shownCols           = 420
	histTop, histBottom = 0, -48
	historyCatchUp      = 8
)

// column is a stretch of the sound: its peaks in and out, as ratios,
// and how far each part turned it down, in decibels.
type column struct {
	in, out         float32
	gate, comp, lim float32
}

// history draws the sound in and out as it plays, scrolling from the
// right, the louder higher, with how far the compressor and limiter turn
// it down hanging from the top in red, the gate's in blue, and lines at
// the thresholds and the ceiling.
type history struct {
	h       plug.Host
	in, out plug.Listener
	cols    [histCols]column
	// n is how many columns have been made, cur the one being made, of
	// curN frames from curAt, and end the frame after the last heard.
	n          int64
	cur        column
	curN       int
	curAt, end int64
	// shown is how far the scroll has come, in columns: it moves on
	// with time, between the chunks the sound comes in, so it scrolls
	// smoothly, and runs no further than the columns made.
	shown float64
	rate  int
	// thresholds and the ceiling, as drawn, with whether each part is on.
	gateAt, compAt, ceiling float64
	gateOn, compOn, limOn   bool
	size                    geom.Size
}

func newHistory(h plug.Host) *history {
	in, out := h.Sound()
	hs := &history{h: h, in: plug.Listener{Sound: in}, out: plug.Listener{Sound: out}}
	for _, id := range []uint32{GateOnID, GateThresholdID, CompOnID, CompThresholdID, LimitOnID, LimitCeilingID} {
		hs.follow(id, h.Param(id))
	}
	return hs
}

// follow takes a parameter the history draws a line of.
func (hs *history) follow(id uint32, v float64) {
	switch id {
	case GateOnID:
		hs.gateOn = v > 0.5
	case GateThresholdID:
		hs.gateAt = v
	case CompOnID:
		hs.compOn = v > 0.5
	case CompThresholdID:
		hs.compAt = v
	case LimitOnID:
		hs.limOn = v > 0.5
	case LimitCeilingID:
		hs.ceiling = v
	}
}

// Step implements [gunim.Animator]: the columns of the sound heard
// since, and the scroll moving on.
func (hs *history) Step(dt time.Duration) bool {
	out, rate, playing := hs.out.Hear(colFrames*16, dt)
	in, _, _ := hs.in.Hear(colFrames*16, dt)
	if len(out) > 0 {
		end := hs.out.At()
		start := end - int64(len(out)/2)
		switch {
		case start == hs.end:
		case start > hs.end && hs.n > 0 && start-hs.end < 2*int64(rate):
			// Frames missed, as the window stalled: the history goes on
			// past them.
			hs.skip(start - hs.end)
		default:
			// A jump: the history starts again from here.
			hs.n, hs.curN, hs.shown = 0, 0, 0
			hs.curAt = start
		}
		hs.end, hs.rate = end, rate
		hs.take(in, out)
	}
	if hs.rate > 0 && playing {
		hs.shown += dt.Seconds() * float64(hs.rate) / colFrames
	}
	made := float64(hs.n)
	hs.shown = max(min(hs.shown, made), made-historyCatchUp)
	if !playing {
		hs.shown = made
	}
	return playing
}

// take adds the frames heard, in and out, to the columns.
func (hs *history) take(in, out []float32) {
	for i := 0; i+1 < len(out); i += 2 {
		if hs.curN == 0 {
			hs.cur = column{}
		}
		o := max(abs32(out[i]), abs32(out[i+1]))
		hs.cur.out = max(hs.cur.out, o)
		if i+1 < len(in) {
			hs.cur.in = max(hs.cur.in, abs32(in[i]), abs32(in[i+1]))
		}
		hs.curN++
		if hs.curN == colFrames {
			hs.endColumn()
		}
	}
}

// endColumn ends the column being made, with how far each part turned
// it down.
func (hs *history) endColumn() {
	hs.cur.gate = hs.traced(GateGRID)
	hs.cur.comp = hs.traced(CompGRID)
	hs.cur.lim = hs.traced(LimitGRID)
	hs.cols[hs.n%histCols] = hs.cur
	hs.n++
	hs.curN = 0
	hs.curAt += colFrames
}

// skip moves the history on over n frames missed, as silence.
func (hs *history) skip(n int64) {
	for ; n > 0; n-- {
		if hs.curN == 0 {
			hs.cur = column{}
		}
		hs.curN++
		if hs.curN == colFrames {
			hs.endColumn()
		}
	}
}

// traced is how far part id turned the column being made down, the
// most over its frames, or 0 where the plugin kept none.
func (hs *history) traced(id uint32) float32 {
	least := float32(0)
	for f := hs.curAt; f < hs.curAt+colFrames; f += plug.TraceFrames {
		if v, ok := hs.h.Traced(id, f); ok {
			least = min(least, v)
		}
	}
	return least
}

func abs32(v float32) float32 { return float32(math.Abs(float64(v))) }

// Layout implements [gunim.Node].
func (hs *history) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	hs.size = c.Max
	return c.Max
}

// y is where on the history a level is, in decibels.
func (hs *history) y(db float32) float32 {
	t := (db - histBottom) / (histTop - histBottom)
	return hs.size.H * (1 - max(0, min(t, 1)))
}

// Paint implements [gunim.Node].
func (hs *history) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	ink, accent := widget.Ink.Get(th), widget.Accent.Get(th)
	radius := widget.CardRadius.Get(th)
	whole := geom.Rect{Max: box.Point()}
	p.RRect(whole, radius, paint.Solid(audioui.Ground.Get(th)))
	end := p.Layer(paint.LayerOpts{Bounds: whole, Opacity: 1, Clip: true, Radius: radius})
	defer end()
	for db := float32(histTop - 6); db > histBottom; db -= 6 {
		y := float32(math.Round(float64(hs.y(db))))
		p.RRect(geom.Rc(0, y, box.W, 1), 0, paint.Solid(audioui.Faded(ink, 0.06)))
		label := audioui.Shaped(fmt.Sprintf("%.0f", db), 9, false, true)
		label.Paint(p, geom.Pt(box.W-label.Advance-6, y+2), audioui.Faded(ink, 0.35))
	}
	colW := box.W / shownCols
	in := HistoryIn.Get(th)
	cut, gate := HistoryCut.Get(th), HistoryGate.Get(th)
	// each runs fn over the columns shown, with where each is.
	each := func(fn func(c column, x0, w float32)) {
		for k := max(0, hs.n-shownCols-1); k < hs.n; k++ {
			// Whole pixels, each column starting where the last ended.
			x1 := float32(math.Round(float64(box.W - float32(hs.shown-float64(k+1))*colW)))
			x0 := float32(math.Round(float64(box.W - float32(hs.shown-float64(k))*colW)))
			if x0 >= box.W || x1 <= 0 || x1 <= x0 {
				continue
			}
			fn(hs.cols[k%histCols], x0, x1-x0)
		}
	}
	// The sound in, faint, and out over it; what the gate, and the
	// compressor and limiter, took away, hanging from the top.
	each(func(c column, x0, w float32) {
		if y := hs.y(float32(dbOf(float64(c.in)))); y < box.H {
			p.RRect(geom.Rc(x0, y, w, box.H-y), 0, paint.Solid(audioui.Faded(in, 0.35)))
		}
		if y := hs.y(float32(dbOf(float64(c.out)))); y < box.H {
			p.RRect(geom.Rc(x0, y, w, box.H-y), 0, paint.Solid(audioui.Faded(accent, 0.22)))
		}
		if c.gate < -0.1 {
			p.RRect(geom.Rc(x0, 0, w, hs.y(c.gate)), 0, paint.Solid(audioui.Faded(gate, 0.3)))
		}
		if cl := c.comp + c.lim; cl < -0.1 {
			p.RRect(geom.Rc(x0, 0, w, hs.y(cl)), 0, paint.Solid(audioui.Faded(cut, 0.25)))
		}
	})
	// Darkened under the controls over it, and the lines over the dark:
	// the bright top edges, in and out, between them what the plugin
	// took away, and the line of the reduction.
	plugui.Darken(p, whole, 1)
	lines := p.Layer(paint.LayerOpts{Bounds: whole, Opacity: plugui.Lines(1)})
	defer lines()
	each(func(c column, x0, w float32) {
		if y := hs.y(float32(dbOf(float64(c.in)))); y < box.H {
			p.RRect(geom.Rc(x0, y, w, 1.5), 0, paint.Solid(in))
		}
		if y := hs.y(float32(dbOf(float64(c.out)))); y < box.H {
			p.RRect(geom.Rc(x0, y, w, 1.5), 0, paint.Solid(audioui.Faded(accent, 0.9)))
		}
		if cl := c.comp + c.lim; cl < -0.1 {
			p.RRect(geom.Rc(x0, hs.y(cl)-1, w, 2), 0, paint.Solid(cut))
		}
	})
	line := func(on bool, db float64, name string, c color.NRGBA) {
		if !on {
			return
		}
		y := hs.y(float32(db))
		for x := float32(0); x < box.W-40; x += 8 {
			p.RRect(geom.Rc(x, y, 4, 1), 0, paint.Solid(audioui.Faded(c, 0.6)))
		}
		label := audioui.Shaped(name, 9, true, false)
		// Over its line, or under it where there is no room over.
		ly := y - label.Height() - 1
		if ly < 2 {
			ly = y + 3
		}
		label.Paint(p, geom.Pt(6, ly), audioui.Faded(c, 0.8))
	}
	line(hs.gateOn, hs.gateAt, "GATE", gate)
	line(hs.compOn, hs.compAt, "THRESHOLD", ink)
	line(hs.limOn, hs.ceiling, "CEILING", cut)
}
