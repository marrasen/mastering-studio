package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strconv"

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
// legend switches them.
const (
	// CurveM is the momentary loudness, over 400 ms.
	CurveM uint8 = 1 << iota
	// CurveS is the short-term loudness, over three seconds.
	CurveS
	// CurveI is the integrated loudness of the whole track.
	CurveI
	// CurveLRA is the loudness range, as a band.
	CurveLRA
)

// loudCurves name the curves, in the legend's order.
var loudCurves = []struct {
	bit  uint8
	name string
}{{CurveM, "M"}, {CurveS, "S"}, {CurveI, "I"}, {CurveLRA, "LRA"}}

// gramTiles is a spectrogram drawn as images, at levels each four
// times coarser than the one before, for views zoomed out, in tiles of
// gramTileCols columns each.
type gramTiles struct {
	levels []gramTileLevel
}

type gramTileLevel struct {
	// per is the spectrogram's columns a column of the level spans.
	per   int
	tiles []*paint.Image
	cols  int
}

const gramTileCols = 256

// newGramTiles draws g's columns as tiles.
func newGramTiles(g *Gram) *gramTiles {
	t := &gramTiles{}
	cols, n := g.Data, g.Cols
	for per := 1; ; per *= 4 {
		t.levels = append(t.levels, gramTileLevel{per: per, tiles: gramImages(cols, n), cols: n})
		if n <= 2048 {
			break
		}
		// Four columns to one, each row its loudest.
		m := (n + 3) / 4
		next := make([]byte, m*gramRows)
		for c := range m {
			row := next[c*gramRows : (c+1)*gramRows]
			for k := 4 * c; k < min(4*c+4, n); k++ {
				for r, v := range cols[k*gramRows : (k+1)*gramRows] {
					row[r] = max(row[r], v)
				}
			}
		}
		cols, n = next, m
	}
	return t
}

// gramPalette is the colour of each byte of a spectrogram, premade.
var gramPalette = func() (p [256][4]byte) {
	for b := range p {
		c := gramColor(gramShade(float32(gramFloor + gramStep*float64(b))))
		p[b] = [4]byte{c.R, c.G, c.B, c.A}
	}
	return p
}()

// gramImages draws n columns of bytes as tiles, the highest pitch at
// the top.
func gramImages(cols []byte, n int) []*paint.Image {
	var out []*paint.Image
	for c0 := 0; c0 < n; c0 += gramTileCols {
		w := min(gramTileCols, n-c0)
		img := image.NewRGBA(image.Rect(0, 0, w, gramRows))
		for c := range w {
			col := cols[(c0+c)*gramRows : (c0+c+1)*gramRows]
			for r, v := range col {
				o := img.PixOffset(c, gramRows-1-r)
				copy(img.Pix[o:o+4], gramPalette[v][:])
			}
		}
		out = append(out, paint.NewImage(img))
	}
	return out
}

// tilesOf returns the tiles of the track's spectrogram, drawing them the
// first time, and keeping those of the tracks seen last.
func (e *editor) tilesOf(g *Gram) *gramTiles {
	if t := e.grams[g]; t != nil {
		return t
	}
	if e.grams == nil {
		e.grams = map[*Gram]*gramTiles{}
	}
	if len(e.grams) >= 3 {
		clear(e.grams)
	}
	t := newGramTiles(g)
	e.grams[g] = t
	return t
}

// paintGram draws the track's spectrogram over its lanes, at the level
// that gives a column a pixel or more, and the pitches marked.
func (e *editor) paintGram(p *paint.Painter, box geom.Size) {
	g := e.track.Wave.Gram
	if g == nil || g.Cols == 0 {
		return
	}
	top, laneH := e.lanes()
	area := geom.Rc(0, top, box.W, 2*laneH)
	tiles := e.tilesOf(g)
	colSecs := float64(gramHop) / float64(g.Rate)
	perPx := float64(e.v1.Value()-e.v0.Value()) / colSecs / float64(box.W)
	lv := &tiles.levels[0]
	for i := range tiles.levels {
		if float64(tiles.levels[i].per) <= max(perPx, 1) {
			lv = &tiles.levels[i]
		}
	}
	end := p.Layer(paint.LayerOpts{Bounds: area, Opacity: 1, Clip: true})
	p.RRect(area, 0, paint.Solid(night))
	for i, img := range lv.tiles {
		w, _ := img.Size()
		c0 := i * gramTileCols * lv.per
		x0 := e.xOf(float64(c0) * colSecs)
		x1 := e.xOf(float64(c0+w*lv.per) * colSecs)
		if x1 < 0 || x0 > box.W {
			continue
		}
		p.Image(img, geom.Rc(x0, area.Min.Y, x1-x0, area.Size().H), paint.ImageOpts{Opacity: 1})
	}
	for _, hz := range []float64{100, 1000, 10000} {
		y := area.Max.Y - area.Size().H*float32(math.Log(hz/20)/math.Log(1000))
		p.RRect(geom.Rc(0, y, box.W, 1), 0, paint.Solid(faded(ink, 0.08)))
		label := fmt.Sprintf("%.0f", hz)
		if hz >= 1000 {
			label = fmt.Sprintf("%.0fk", hz/1000)
		}
		shaped(label, 9, false).Paint(p, geom.Pt(6, y-12), faded(ink, 0.5))
	}
	end()
	e.paintOutside(p, box)
}

// paintOutside shades the file outside the cut away.
func (e *editor) paintOutside(p *paint.Painter, box geom.Size) {
	start, end := e.span()
	sx, ex := e.xOf(start), e.xOf(end)
	if sx > 0 {
		p.RRect(geom.Rc(0, rulerH+1, sx, box.H-rulerH), 0, paint.Solid(faded(night, 0.45)))
	}
	if ex < box.W {
		p.RRect(geom.Rc(ex, rulerH+1, box.W-ex, box.H-rulerH), 0, paint.Solid(faded(night, 0.55)))
	}
}

// The loudness's scale over the lanes, in LUFS, the top and the bottom.
const curveTop, curveBottom = 0, -42

// pink is the short-term loudness's colour, which no part of the
// waveform or the spectrogram takes.
var pink = rgb(0xff, 0x6a, 0xd5)

// curveRGBA is the colour of a curve, at alpha a: the momentary
// loudness faint ink, the short-term pink, the integrated amber, and
// the range sky blue.
func curveRGBA(bit uint8, a float32) color.NRGBA {
	switch bit {
	case CurveM:
		return faded(ink, 0.7*a)
	case CurveS:
		return faded(pink, a)
	case CurveI:
		return faded(amber, a)
	}
	return faded(sky, a)
}

// paintCurves draws the loudness the track was measured at, along it,
// over the lanes: as switched, the momentary and short-term loudness as
// lines, the integrated as a line across, and the range as a band;
// faint where the track changed since.
func (e *editor) paintCurves(p *paint.Painter, box geom.Size) {
	m, on := e.track.Measure, e.r.state.Curves
	if !e.track.Measured || on == 0 {
		return
	}
	alpha := float32(1)
	if e.track.Stale {
		alpha = 0.4
	}
	top, laneH := e.lanes()
	area := geom.Rc(0, top, box.W, 2*laneH)
	yOf := func(l float64) float32 {
		u := (l - curveBottom) / (curveTop - curveBottom)
		return area.Max.Y - area.Size().H*float32(max(0, min(u, 1)))
	}
	end := p.Layer(paint.LayerOpts{Bounds: area, Opacity: 1, Clip: true})
	defer end()
	if on&CurveLRA != 0 && m.Ranged {
		y0, y1 := yOf(float64(m.High)), yOf(float64(m.Low))
		p.RRect(geom.Rc(0, y0, box.W, y1-y0), 0, paint.Solid(curveRGBA(CurveLRA, 0.12*alpha)))
		p.RRect(geom.Rc(0, y0, box.W, 1), 0, paint.Solid(curveRGBA(CurveLRA, 0.5*alpha)))
		p.RRect(geom.Rc(0, y1, box.W, 1), 0, paint.Solid(curveRGBA(CurveLRA, 0.5*alpha)))
		shapedFace(fmt.Sprintf("LRA %.1f", m.LRA), 10, true, true).Paint(p, geom.Pt(40, y1+3), curveRGBA(CurveLRA, alpha))
	}
	// The render's time, of the curves, as the file's.
	start, _ := e.span()
	fileT := func(rt float64) float64 { return start + rt - e.gap() }
	// line draws a curve of loudness, each point step seconds after the
	// last, the first at lead, over a dark edge so it reads on any
	// ground.
	line := func(bit uint8, ls []float64, lead, step float64, width float32) {
		var prev geom.Point
		was := false
		lastX := float32(-1e9)
		for k, l := range ls {
			t := fileT(lead + float64(k)*step)
			x := e.xOf(t)
			if x < -20 || x > box.W+20 {
				was = false
				continue
			}
			if x-lastX < 1 && k+1 < len(ls) {
				continue
			}
			if l < curveBottom {
				was = false
				continue
			}
			pt := geom.Pt(x, yOf(l))
			if was {
				segment(p, prev, pt, width+2, faded(night, 0.55*alpha))
				segment(p, prev, pt, width, curveRGBA(bit, alpha))
			}
			prev, was, lastX = pt, true, x
		}
	}
	if on&CurveM != 0 {
		line(CurveM, loudnesses(m.blocks), 0.4, 0.1, 1)
	}
	if on&CurveS != 0 {
		line(CurveS, loudnesses(m.shorts), 3, 0.1, 2)
	}
	if on&CurveI != 0 && m.Loud {
		// The integrated loudness as it grows, from the start to each
		// second: a passage that lifts it shows as a rise.
		grown := make([]float64, len(m.running))
		for i, l := range m.running {
			grown[i] = float64(l)
		}
		line(CurveI, grown, 1, 1, 2)
		y := yOf(float64(m.LUFS))
		for x := float32(0); x < box.W; x += 10 {
			p.RRect(geom.Rc(x, y-0.75, 6, 1.5), 0.75, paint.Solid(curveRGBA(CurveI, alpha)))
		}
		run := shapedFace(fmt.Sprintf("I %.1f", m.LUFS), 10, true, true)
		run.Paint(p, geom.Pt(box.W-zoomW-run.Advance-8, y-14), curveRGBA(CurveI, alpha))
	}
	// The target, faint, for the loudness to be read against, and the
	// scale, at the right.
	if on&(CurveS|CurveM|CurveI) != 0 {
		y := yOf(float64(e.r.state.Target))
		p.RRect(geom.Rc(0, y, box.W, 1), 0, paint.Solid(faded(ink, 0.18*alpha)))
		for l := -10; l >= -30; l -= 10 {
			y := yOf(float64(l))
			run := shapedFace(strconv.Itoa(l), 9, false, true)
			x := box.W - zoomW - run.Advance - 6
			p.RRect(geom.Rc(x-4, y, run.Advance+8, 1), 0, paint.Solid(faded(ink, 0.25)))
			run.Paint(p, geom.Pt(x, y-11), faded(ink, 0.45))
		}
	}
}

// loudnesses are powers' loudnesses.
func loudnesses(powers []float64) []float64 {
	out := make([]float64, len(powers))
	for i, pw := range powers {
		out[i] = lufsOf(pw)
	}
	return out
}

// lufsOf is the loudness of a mean weighted power.
func lufsOf(power float64) float64 {
	if power <= 0 {
		return math.Inf(-1)
	}
	return -0.691 + 10*math.Log10(power)
}

// legendRects are where the legend's switches are, at the editor's top
// right, under the ruler.
func (e *editor) legendRects() []geom.Rect {
	out := make([]geom.Rect, len(loudCurves))
	x := e.size.W - zoomW - 8
	for i := len(loudCurves) - 1; i >= 0; i-- {
		w := shaped(loudCurves[i].name, 10, true).Advance + 26
		x -= w
		out[i] = geom.Rc(x, rulerH+8, w, 20)
		x -= 4
	}
	return out
}

// legendAt is the curve whose switch is at p, or 0.
func (e *editor) legendAt(p geom.Point) uint8 {
	for i, r := range e.legendRects() {
		if r.Contains(p) {
			return loudCurves[i].bit
		}
	}
	return 0
}

// paintLegend draws the curves' switches: each a dot of its colour and
// its name, lit while it is drawn.
func (e *editor) paintLegend(p *paint.Painter) {
	on := e.r.state.Curves
	for i, r := range e.legendRects() {
		c := loudCurves[i]
		lit := on&c.bit != 0
		fill := faded(night, 0.75)
		p.RRect(r, 10, paint.Solid(fill))
		dot := curveRGBA(c.bit, 0.35)
		if lit {
			dot = curveRGBA(c.bit, 1)
			p.RRectStroke(r, 10, paint.Solid(color.NRGBA{}), paint.Stroke{Width: 1, Color: curveRGBA(c.bit, 0.6)})
		}
		p.RRect(geom.Rc(r.Min.X+8, r.Min.Y+7, 6, 6), 3, paint.Solid(dot))
		words := faded(ink, 0.45)
		if lit {
			words = ink
		}
		shaped(c.name, 10, true).Paint(p, geom.Pt(r.Min.X+18, r.Min.Y+4), words)
	}
}

// gramShade is a level, in decibels, as a spectrogram's colour runs,
// from 0 to 1: from -96 dB to -6, eased at either end.
func gramShade(db float32) float32 {
	t := min(max((db+96)/90, 0), 1)
	return t * t * (3 - 2*t)
}
