package main

import (
	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// The editor's views of the track: its waveform, or its spectrogram;
// and over either, the loudness it was measured at along it.

// View is how the editor shows the track.
type View int

const (
	// ViewWave shows its channels' waveforms.
	ViewWave View = iota
	// ViewGram shows its spectrogram: its pitches over its length.
	ViewGram
)

// The loudness curves the editor may draw over the track, as its
// legend switches them, as bits of Album.Curves.
const (
	CurveM   = audioui.CurveM
	CurveS   = audioui.CurveS
	CurveI   = audioui.CurveI
	CurveLRA = audioui.CurveLRA
)

// tilesOf returns the tiles of the track's spectrogram, drawing them the
// first time, and keeping those of the tracks seen last.
func (e *editor) tilesOf(g *audioui.Gram) *audioui.GramTiles {
	if t := e.grams[g]; t != nil {
		return t
	}
	if e.grams == nil {
		e.grams = map[*audioui.Gram]*audioui.GramTiles{}
	}
	if len(e.grams) >= 3 {
		clear(e.grams)
	}
	t := audioui.NewGramTiles(g)
	e.grams[g] = t
	return t
}

// paintGram draws the track's spectrogram over its lanes, and the
// pitches marked.
func (e *editor) paintGram(p *paint.Painter, f gunim.Frame, box geom.Size) {
	g := e.track.Wave.Gram
	if g == nil || g.Cols == 0 {
		return
	}
	top, laneH := e.lanes()
	e.tilesOf(g).Paint(p, f.Theme, geom.Rc(0, top, box.W, 2*laneH), e.xOf)
	e.paintOutside(p, colours(f.Theme), box)
}

// paintOutside shades the file outside the cut away.
func (e *editor) paintOutside(p *paint.Painter, pal palette, box geom.Size) {
	start, end := e.span()
	sx, ex := e.xOf(start), e.xOf(end)
	if sx > 0 {
		p.RRect(geom.Rc(0, rulerH+1, sx, box.H-rulerH), 0, paint.Solid(faded(pal.night, 0.45)))
	}
	if ex < box.W {
		p.RRect(geom.Rc(ex, rulerH+1, box.W-ex, box.H-rulerH), 0, paint.Solid(faded(pal.night, 0.55)))
	}
}

// paintCurves draws the loudness the track was measured at, along it,
// over the lanes, as the legend switches the curves; faint where the
// track changed since.
func (e *editor) paintCurves(p *paint.Painter, f gunim.Frame, box geom.Size) {
	if !e.track.Measured {
		return
	}
	v := audioui.CurveView{Alpha: 1, Target: e.r.state.Target, Right: box.W - zoomW}
	if e.track.Stale {
		v.Alpha = 0.4
	}
	for i := range v.Fade {
		v.Fade[i] = e.curves[i].Value()
	}
	top, laneH := e.lanes()
	v.Area = geom.Rc(0, top, box.W, 2*laneH)
	// The render's time, of the curves, as the file's.
	start, _ := e.span()
	v.X = func(rt float64) float32 { return e.xOf(start + rt - e.gap()) }
	c := e.track.Measure.curves()
	c.Paint(p, f.Theme, v)
}

// legendRects are where the legend's switches are, at the editor's top
// right, under the ruler.
func (e *editor) legendRects() []geom.Rect {
	return audioui.LegendRects(e.size.W-zoomW-8, rulerH+18)
}

// legendAt is the curve whose switch is at p, or 0.
func (e *editor) legendAt(p geom.Point) uint8 {
	for i, r := range e.legendRects() {
		if r.Contains(p) {
			return audioui.CurveNames[i].Bit
		}
	}
	return 0
}
