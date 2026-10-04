package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"sync"

	"github.com/marrasen/gunim/paint"
)

// iconSizes are the sizes the window's icon is drawn at, for the title
// bar, the taskbar and the switcher.
var iconSizes = []int{16, 24, 32, 48, 64, 128, 256}

// icons returns the studio's icon at each of iconSizes.
func icons() []image.Image {
	out := make([]image.Image, len(iconSizes))
	var wg sync.WaitGroup
	for i, n := range iconSizes {
		wg.Go(func() { out[i] = drawIcon(n) })
	}
	wg.Wait()
	return out
}

// logo is the icon as the header draws it, drawn the first time it is
// asked for. It is reached from the UI goroutine alone.
var logo = sync.OnceValue(func() *paint.Image { return paint.NewImage(drawIcon(64)) })

// writeIcon writes the icon n pixels square to a PNG file, for the
// Windows program's icon.
func writeIcon(path string, n int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, drawIcon(n)); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// drawIcon draws the icon n pixels square: five meter bars on a night
// tile, rising from the middle out to make an M, each with its peak
// held over it, as the studio's meters hold theirs. Each pixel is
// sampled sixteen times, four at the large sizes, so the edges are
// smooth at every size.
func drawIcon(n int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	ss := 4
	if n > 64 {
		ss = 2
	}
	for py := range n {
		for px := range n {
			var r, g, b, a float64
			for sy := range ss {
				for sx := range ss {
					x := (float64(px) + (float64(sx)+0.5)/float64(ss)) / float64(n)
					y := (float64(py) + (float64(sy)+0.5)/float64(ss)) / float64(n)
					cr, cg, cb, ca := iconAt(x, y)
					r += cr * ca
					g += cg * ca
					b += cb * ca
					a += ca
				}
			}
			if a == 0 {
				continue
			}
			k := float64(ss * ss)
			// Premultiplied, as image.RGBA holds it.
			img.SetRGBA(px, py, color.RGBA{
				R: uint8(math.Round(r / k * 255)), G: uint8(math.Round(g / k * 255)),
				B: uint8(math.Round(b / k * 255)), A: uint8(math.Round(a / k * 255)),
			})
		}
	}
	return img
}

// The bars, in icon units: barTops are where each rises to, from
// barFoot, barW wide and barPitch apart from barLeft.
var barTops = [5]float64{0.25, 0.42, 0.55, 0.42, 0.25}

const (
	barLeft  = 0.2
	barW     = 0.092
	barPitch = 0.127
	barFoot  = 0.78
	// hotTop is where a bar turns amber, as a meter nears full scale.
	hotTop = 0.33
)

// iconAt is the icon's colour at x, y, in icon units, 0 to 1 across, as
// straight red, green, blue and alpha from 0 to 1.
func iconAt(x, y float64) (r, g, b, a float64) {
	const inset, radius = 0.04, 0.22
	if tileDist(x, y, inset, 1-inset, radius) > 0 {
		return 0, 0, 0, 0
	}
	// Night, a little lighter at the top, with a teal glow rising from
	// under the bars.
	c := mixRGB([3]float64{0.11, 0.13, 0.18}, [3]float64{0.04, 0.05, 0.07}, y)
	r, g, b, a = c[0], c[1], c[2], 1
	over := func(c [3]float64, ca float64) {
		r, g, b = r+(c[0]-r)*ca, g+(c[1]-g)*ca, b+(c[2]-b)*ca
	}
	teal := [3]float64{0.31, 0.84, 0.75}
	sky := [3]float64{0.36, 0.72, 1}
	amber := [3]float64{1, 0.78, 0.34}
	ink := [3]float64{0.93, 0.94, 0.96}
	if d := math.Hypot((x-0.5)*0.9, y-0.86); d < 0.5 {
		over(teal, 0.2*(1-d/0.5)*(1-d/0.5))
	}
	// The floor the bars stand on.
	if y > barFoot+0.03 && y < barFoot+0.045 && x > barLeft-0.02 && x < 1-barLeft+0.02 {
		over(ink, 0.22)
	}
	for i, top := range barTops {
		x0 := barLeft + float64(i)*barPitch
		// The bar, teal at its foot to sky at its top, amber past hot.
		if d := roundRect(x, y, x0, top, x0+barW, barFoot, 0.022); d < 0 {
			t := (barFoot - y) / (barFoot - barTops[0])
			c := mixRGB(teal, sky, t)
			if y < hotTop {
				c = amber
			}
			over(c, 1)
			// A sheen down its left edge.
			if x-x0 < barW*0.3 {
				over(ink, 0.12)
			}
		} else if d < 0.03 {
			over(teal, 0.25*(1-d/0.03))
		}
		// The peak held, a line over it.
		if roundRect(x, y, x0, top-0.065, x0+barW, top-0.04, 0.0125) < 0 {
			over(ink, 0.92)
		}
	}
	return r, g, b, a
}

// mixRGB blends c0 toward c1 by t, held within 0 to 1.
func mixRGB(c0, c1 [3]float64, t float64) [3]float64 {
	t = math.Max(0, math.Min(t, 1))
	return [3]float64{c0[0] + (c1[0]-c0[0])*t, c0[1] + (c1[1]-c0[1])*t, c0[2] + (c1[2]-c0[2])*t}
}

// roundRect is how far x, y lies outside a rectangle from x0, y0 to x1,
// y1 with corners of radius, negative inside.
func roundRect(x, y, x0, y0, x1, y1, radius float64) float64 {
	cx, cy := (x0+x1)/2, (y0+y1)/2
	hx, hy := (x1-x0)/2-radius, (y1-y0)/2-radius
	dx, dy := math.Abs(x-cx)-hx, math.Abs(y-cy)-hy
	return math.Hypot(math.Max(dx, 0), math.Max(dy, 0)) + math.Min(math.Max(dx, dy), 0) - radius
}

// tileDist is how far x, y lies outside a rounded square from lo to hi
// on both axes, negative inside.
func tileDist(x, y, lo, hi, radius float64) float64 {
	return roundRect(x, y, lo, lo, hi, hi, radius)
}
