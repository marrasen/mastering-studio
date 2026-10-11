package eq

import (
	"fmt"
	"image/color"
	"math"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
	"github.com/marrasen/mastering-studio/plug"
)

// The EQ's sizes. Its colours are the theme's own: [widget.Ink] and
// [widget.Accent], [audioui.Ground] behind the graph, and the colours
// of [audioui] for the bands.
var (
	// DotRadius is the radius of a band's point on the graph.
	DotRadius = theme.Length("eq.dot.radius", 9)
	// DotReach is how near a point the pointer takes it.
	DotReach = theme.Length("eq.dot.reach", 16)
	// LabelSize is the size of the graph's labels and the strip's
	// captions, and ReadoutSize of the settings shown beside a point
	// and in the strip.
	LabelSize   = theme.Length("eq.label.size", 10)
	ReadoutSize = theme.Length("eq.readout.size", 12)
	// CurveWidth is how wide the graph draws the curve.
	CurveWidth = theme.Length("eq.curve.width", 2.2)
	// StripHeight is the height of one row of the strip under the
	// graph, PillWidth the width of a kind's pill in it, and ValueWidth
	// of a value.
	StripHeight = theme.Length("eq.strip.height", 76)
	PillWidth   = theme.Length("eq.pill.width", 74)
	ValueWidth  = theme.Length("eq.value.width", 84)
)

// kindNames names the kinds of band, in their order.
var kindNames = []string{"Bell", "Low shelf", "High shelf", "Low cut", "High cut", "Notch"}

// bandColor returns the colour of the band at place i, from the
// theme's colours for sound.
func bandColor(th *theme.Live, i int) color.NRGBA {
	switch i % maxBands {
	case 0:
		return audioui.Over.Get(th)
	case 1:
		return audioui.Near.Get(th)
	case 2:
		return audioui.Mix(audioui.Near.Get(th), audioui.Sound.Get(th), 0.5)
	case 3:
		return audioui.Sound.Get(th)
	case 4:
		return audioui.Spread.Get(th)
	case 5:
		return widget.Accent.Get(th)
	case 6:
		return audioui.Mix(audioui.Spread.Get(th), audioui.Short.Get(th), 0.5)
	default:
		return audioui.Short.Get(th)
	}
}

// hzText writes a frequency as a studio does: 80 Hz, 1.25 kHz.
func hzText(hz float32) string {
	switch {
	case hz >= 10000:
		return fmt.Sprintf("%.1f kHz", hz/1000)
	case hz >= 1000:
		return fmt.Sprintf("%.2f kHz", hz/1000)
	}
	return fmt.Sprintf("%.0f Hz", hz)
}

// bandText describes a band's settings in one line.
func bandText(b audio.Band) string {
	switch {
	case isCut(b.Kind):
		return fmt.Sprintf("%s  ·  %d dB/oct", hzText(b.Freq), max(b.Slope, 12))
	case b.Kind == audio.Notch:
		return fmt.Sprintf("%s  ·  Q %.2f", hzText(b.Freq), b.Q)
	}
	return fmt.Sprintf("%s  ·  %+.1f dB  ·  Q %.2f", hzText(b.Freq), b.Gain, b.Q)
}

// paintFit draws s at size, its top left at at, cut short with an
// ellipsis where it is wider than room.
func paintFit(p *paint.Painter, s string, size float32, bold bool, at geom.Point, room float32, c color.NRGBA) {
	run := audioui.Shaped(s, size, bold, false)
	if run.Advance <= room {
		run.Paint(p, at, c)
		return
	}
	rs := []rune(s)
	for n := len(rs) - 1; n > 0; n-- {
		if cut := audioui.Shaped(string(rs[:n])+"…", size, bold, false); cut.Advance <= room {
			cut.Paint(p, at, c)
			return
		}
	}
}

// chipPoints is how many frequencies the chip works the curve out at.
const chipPoints = 64

// chip is the EQ's curve, drawn across a plugin's chip in a chain:
// flat where there are no bands, or the EQ is bypassed.
type chip struct {
	p      *plugin
	bypass bool
	// amount scales the gains, as a ratio.
	amount float64
	// curve is the bands' response at chipPoints frequencies, from
	// eqLow to eqHigh, worked out as the bands change.
	curve [chipPoints]float32
}

func newChip(p *plugin, h plug.Host) *chip {
	c := &chip{p: p, bypass: h.Param(BypassID) > 0.5, amount: h.Param(AmountID) / 100}
	c.read()
	return c
}

// read works the curve out again from the plugin's bands.
func (c *chip) read() {
	bands := c.p.Bands()
	for i := range bands {
		bands[i].Gain *= float32(c.amount)
	}
	for i := range c.curve {
		hz := eqLow * math.Pow(eqHigh/eqLow, float64(i)/(chipPoints-1))
		c.curve[i] = float32(audio.Response(bands, hz))
	}
}

// Follow implements [plug.Follower]: the curve flattens while the EQ
// is bypassed, and scales with the amount.
func (c *chip) Follow(id uint32, v float64, u *gunim.UI) {
	switch id {
	case BypassID:
		c.bypass = v > 0.5
	case AmountID:
		c.amount = v / 100
		c.read()
	default:
		return
	}
	u.Invalidate()
}

// FollowState implements [plug.StateFollower].
func (c *chip) FollowState(u *gunim.UI) {
	c.read()
	u.Invalidate()
}

// Layout implements [gunim.Node]: the chip takes the room it is given.
func (c *chip) Layout(cs gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return cs.Max
}

// Paint implements [gunim.Node]: the line of 0 dB, faint, and the
// curve over it, from -eqRange at the bottom to eqRange at the top.
func (c *chip) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	ink, accent := widget.Ink.Get(th), widget.Accent.Get(th)
	width := CurveWidth.Get(th)
	mid := box.H / 2
	half := max(mid-width, 1)
	p.RRect(geom.Rc(0, mid, box.W, 1), 0, paint.Solid(audioui.Faded(ink, 0.15)))
	var prev geom.Point
	for i, db := range c.curve {
		if c.bypass {
			db = 0
		}
		y := mid - max(-1, min(db/eqRange, 1))*half
		// The line's round ends stay inside the box.
		pt := geom.Pt(width/2+(box.W-width)*float32(i)/(chipPoints-1), y)
		if i > 0 {
			audioui.Segment(p, prev, pt, width, accent)
		}
		prev = pt
	}
}
