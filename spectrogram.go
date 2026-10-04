package main

import (
	"image"
	"image/color"
	"math"

	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
)

// spectrogram is the spectrum heard over the last seconds: a column for
// each moment, its pitches up it, coloured by how loud. The columns
// are kept in tiles of gramTile each: an image never changes once made,
// so a column added makes only the newest tile anew, and the older ones
// slide along as they are.
type spectrogram struct {
	// tiles are the tiles filled, the oldest first; fill is the tile
	// being filled, n columns of it, and newest its image as filled.
	tiles  []*paint.Image
	fill   *image.RGBA
	n      int
	newest *paint.Image
	keep   int
}

// gramTile is how many columns a tile holds.
const gramTile = 32

// push adds a column of levels, from 0 to 1, lowest pitch first.
func (g *spectrogram) push(levels []float32) {
	h := len(levels)
	if g.fill == nil || g.fill.Rect.Dy() != h {
		g.fill = image.NewRGBA(image.Rect(0, 0, gramTile, h))
		g.n = 0
	}
	for i, v := range levels {
		g.fill.SetRGBA(g.n, h-1-i, gramColor(v))
	}
	g.n++
	g.newest = paint.NewImage(g.fill)
	if g.n == gramTile {
		g.tiles = append(g.tiles, g.newest)
		g.fill, g.newest, g.n = nil, nil, 0
		if over := len(g.tiles) - g.keep; g.keep > 0 && over > 0 {
			g.tiles = g.tiles[over:]
		}
	}
}

// paint draws the columns into r, the newest at its right edge, each
// col pixels wide.
func (g *spectrogram) paint(p *paint.Painter, r geom.Rect, col float32) {
	g.keep = int(r.Size().W/(col*gramTile)) + 2
	end := p.Layer(paint.LayerOpts{Bounds: r, Opacity: 1, Clip: true, Radius: 10})
	defer end()
	x := r.Max.X
	if g.newest != nil {
		w := float32(g.n) * col
		h := float32(g.fill.Rect.Dy())
		p.Image(g.newest, geom.Rc(x-w, r.Min.Y, w, r.Size().H), paint.ImageOpts{Src: geom.Rc(0, 0, float32(g.n), h), Opacity: 1})
		x -= w
	}
	for i := len(g.tiles) - 1; i >= 0 && x > r.Min.X; i-- {
		w := gramTile * col
		p.Image(g.tiles[i], geom.Rc(x-w, r.Min.Y, w, r.Size().H), paint.ImageOpts{Opacity: 1})
		x -= w
	}
}

// gramStops are the colours of a level, from silence to full.
var gramStops = []color.NRGBA{rgb(0x0c, 0x0e, 0x13), rgb(0x1a, 0x2a, 0x66), rgb(0x2c, 0x8f, 0xa8), teal, amber, rgb(0xff, 0xf4, 0xe0)}

// gramColor is the colour of level v, from 0 to 1.
func gramColor(v float32) color.RGBA {
	v = min(max(v, 0), 1) * float32(len(gramStops)-1)
	i := min(int(v), len(gramStops)-2)
	c := anim.Mix(anim.ColorCodec, gramStops[i], gramStops[i+1], v-float32(i))
	return color.RGBA{R: c.R, G: c.G, B: c.B, A: 0xff}
}

// gramLevel is a spectrum's level, in decibels, as a spectrogram's,
// from 0 to 1: its lowest quarter faded out, so the noise floor stays
// dark.
func gramLevel(db float32) float32 {
	t := (db - specBottom) / (specTop - specBottom)
	return float32(math.Max(0, float64(t-0.2)/0.8))
}
