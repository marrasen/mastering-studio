package main

import (
	"math"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// newThemePreview is what the theme editor's preview shows: a corner of
// the studio, painted in the studio's colours, over a few of its
// controls, so every value the editor puts first shows there.
func newThemePreview() gunim.Node {
	export := widget.NewButton("Export…")
	export.Kind = widget.ButtonPrimary
	dither := widget.NewCheckbox("Dither")
	dither.SetChecked(true, nil)
	controls := widget.Row(export, widget.NewButton("Add tracks"), dither)
	controls.Cross = widget.CrossCenter
	pad := widget.NewPad(controls)
	pad.Padding = widget.CardPadding
	col := widget.Column(&studioCorner{}, pad)
	col.Cross = widget.CrossStretch
	return col
}

// studioCorner paints a track as the studio shows it: its row, its
// waveform with a fade in and the short-term loudness over it, a
// reference, and loudness bars on, near and off their target. size is
// its size, as last laid out.
type studioCorner struct{ size geom.Size }

// cornerHeight is how tall the corner is.
const cornerHeight = 236

// Layout implements [gunim.Node].
func (s *studioCorner) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	s.size = c.Constrain(geom.Sz(c.Max.W, cornerHeight))
	return s.size
}

// Paint implements [gunim.Node].
func (*studioCorner) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	pal := colours(f.Theme)
	p.RRect(geom.Rect{Max: box.Point()}, 10, paint.Solid(pal.night))
	panel := geom.Rc(10, 10, box.W-20, box.H-20)
	p.RRect(panel, 10, paint.Solid(pal.panel))
	pal.outline(p, panel, 10, 1)
	x, w := panel.Min.X+12, panel.Size().W-24

	// The track's row.
	row := geom.Rc(x, panel.Min.Y+12, w, 40)
	p.RRect(row, 8, paint.Solid(pal.raised))
	pal.outline(p, row, 8, 1)
	p.RRect(geom.Rc(row.Min.X+13, row.Min.Y+15, 10, 10), 5, paint.Solid(pal.teal))
	paintFit(p, "01  Opening", 13, true, geom.Pt(row.Min.X+32, row.Min.Y+5), w-140, pal.ink)
	paintFit(p, "3:42 · −14.0 LUFS", 11, false, geom.Pt(row.Min.X+32, row.Min.Y+22), w-140, pal.quiet(0.6))
	ref := shaped("REFERENCE", 10, true)
	chip := geom.Rc(row.Max.X-ref.Advance-26, row.Min.Y+11, ref.Advance+16, 18)
	p.RRect(chip, 9, paint.Solid(faded(pal.sky, 0.14)))
	ref.Paint(p, geom.Pt(chip.Min.X+8, chip.Min.Y+3), pal.sky)

	// The waveform, its fade in shaded, and the short-term loudness
	// over it.
	wave := geom.Rc(x, row.Max.Y+12, w, 76)
	mid := wave.Min.Y + wave.Size().H/2
	fade := wave.Size().W * 0.18
	p.RRect(geom.Rc(wave.Min.X, wave.Min.Y, fade, wave.Size().H), 0, paint.Solid(faded(pal.sky, 0.07)))
	audioui.Segment(p, geom.Pt(wave.Min.X, wave.Max.Y), geom.Pt(wave.Min.X+fade, wave.Min.Y), 1, faded(pal.sky, 0.6))
	for bx := float32(0); bx < wave.Size().W; bx += 3 {
		t := float64(bx / wave.Size().W)
		h := float32(0.25+0.6*math.Abs(math.Sin(t*19)*math.Cos(t*7))) * wave.Size().H / 2
		h *= min(1, bx/fade)
		p.RRect(geom.Rc(wave.Min.X+bx, mid-h, 2, 2*h), 1, paint.Solid(faded(pal.teal, 0.85)))
	}
	short := audioui.Short.Get(f.Theme)
	var prev geom.Point
	for i := range 41 {
		t := float32(i) / 40
		pt := geom.Pt(wave.Min.X+t*wave.Size().W, wave.Min.Y+10+14*float32(1-math.Sin(float64(t)*5)*0.5)*min(1, 3*t+0.2))
		if i > 0 {
			audioui.Segment(p, prev, pt, 1.5, short)
		}
		prev = pt
	}

	// Loudness on its target, near it and off it.
	y := wave.Max.Y + 14
	for _, b := range []struct {
		name string
		v    float32
	}{{"On the mark", -14}, {"Near it", -15.2}, {"Off it", -10.5}} {
		paintFit(p, b.name, 11, false, geom.Pt(x, y-2), 90, pal.quiet(0.6))
		audioui.PaintLoudnessBar(p, f.Theme, geom.Rc(x+96, y, w-96, 10), b.v, -14)
		y += 22
	}
}
