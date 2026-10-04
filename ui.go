package main

import (
	"fmt"
	"image/color"
	"math"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/widget"
)

// The studio's colours: near-black panels, a cool teal for what plays,
// amber for a reading near its mark and coral for one off it.
var (
	ink    = rgb(0xec, 0xee, 0xf4)
	night  = rgb(0x0c, 0x0e, 0x13)
	panel  = rgb(0x15, 0x18, 0x20)
	raised = rgb(0x1d, 0x21, 0x2b)
	teal   = rgb(0x4f, 0xd6, 0xc0)
	sky    = rgb(0x5c, 0xb8, 0xff)
	amber  = rgb(0xff, 0xc8, 0x57)
	coral  = rgb(0xff, 0x6b, 0x5f)
)

func rgb(r, g, b uint8) color.NRGBA { return color.NRGBA{R: r, G: g, B: b, A: 0xff} }

// faded is c at alpha a, from 0 to 1.
func faded(c color.NRGBA, a float32) color.NRGBA {
	c.A = uint8(float32(c.A) * min(max(a, 0), 1))
	return c
}

// mix blends a toward b by t.
func mix(a, b color.NRGBA, t float32) color.NRGBA { return anim.Mix(anim.ColorCodec, a, b, t) }

// runs holds text shaped already, for nodes that paint every frame. It
// is reached from the UI goroutine alone.
var runs = map[runKey]text.Run{}

type runKey struct {
	s    string
	size float32
	bold bool
	mono bool
}

// shaped is s shaped at size, from the cache.
func shaped(s string, size float32, bold bool) text.Run { return shapedFace(s, size, bold, false) }

// shapedFace is s shaped at size, in the monospaced face for figures
// that change, so they hold still.
func shapedFace(s string, size float32, bold, mono bool) text.Run {
	k := runKey{s, size, bold, mono}
	if r, ok := runs[k]; ok {
		return r
	}
	if len(runs) > 4000 {
		clear(runs)
	}
	face := text.GoSans(bold, false)
	if mono {
		face = text.GoMono(bold, false)
	}
	r := face.Shape(s, size)
	runs[k] = r
	return r
}

// paintFit draws s at size, its top left at at, cut short with an
// ellipsis where it is wider than room.
func paintFit(p *paint.Painter, s string, size float32, bold bool, at geom.Point, room float32, c color.NRGBA) {
	run := shaped(s, size, bold)
	if run.Advance <= room {
		run.Paint(p, at, c)
		return
	}
	rs := []rune(s)
	for n := len(rs) - 1; n > 0; n-- {
		if cut := shaped(string(rs[:n])+"…", size, bold); cut.Advance <= room {
			cut.Paint(p, at, c)
			return
		}
	}
}

// clock writes d as minutes, seconds and tenths.
func clock(d time.Duration) string {
	d = max(d, 0)
	t := int(d.Round(100*time.Millisecond) / (100 * time.Millisecond))
	return fmt.Sprintf("%d:%02d.%d", t/600, t/10%60, t%10)
}

// short writes d as minutes and seconds.
func short(d time.Duration) string {
	s := int(max(d, 0).Round(time.Second) / time.Second)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// loudnessColor colours a reading by how far it is from the target:
// teal within half a unit, amber within one and a half, coral past.
func loudnessColor(off float32) color.NRGBA {
	a := math.Abs(float64(off))
	switch {
	case a <= 0.5:
		return teal
	case a <= 1.5:
		return mix(teal, amber, float32((a-0.5)/1))
	case a <= 3:
		return mix(amber, coral, float32((a-1.5)/1.5))
	}
	return coral
}

// iconButton is a round button with an icon, lit as the pointer comes
// over it, squashed as it is pressed; primary fills it with its colour.
type iconButton struct {
	anim.Group
	ic          *icon.Icon
	press       func(*gunim.UI)
	primary     bool
	color       color.NRGBA
	hover, down *anim.Float
	held        bool
	size        geom.Size
}

func newIconButton(ic *icon.Icon, press func(*gunim.UI)) *iconButton {
	b := &iconButton{ic: ic, press: press, color: teal, hover: anim.NewFloat(0), down: anim.NewFloat(0)}
	b.Add(b.hover, b.down)
	return b
}

// Focusable implements [gunim.Focusable]: the window's own keys work
// what the buttons do.
func (b *iconButton) Focusable() bool { return false }

// Handle implements [gunim.Handler].
func (b *iconButton) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerEnter:
		b.hover.Animate(1, anim.Snappy)
	case input.PointerLeave:
		b.hover.Animate(0, anim.Gentle)
		b.down.Animate(0, anim.Gentle)
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		b.held = true
		b.down.Animate(1, anim.Spring{Response: 0.12, Damping: 1})
	case input.PointerUp:
		if !b.held {
			return false
		}
		b.held = false
		b.down.Animate(0, anim.Spring{Response: 0.4, Damping: 0.45})
		if (geom.Rect{Max: b.size.Point()}).Contains(e.Pos) {
			u.Cue(gunim.CuePress, b)
			b.press(u)
		}
	default:
		return false
	}
	u.Invalidate()
	return true
}

// Layout implements [gunim.Node].
func (b *iconButton) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	b.size = c.Max
	return c.Max
}

// Paint implements [gunim.Node].
func (b *iconButton) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	mid := geom.Pt(box.W/2, box.H/2)
	defer p.Push(paint.Scale(1-0.1*b.down.Value()+0.04*b.hover.Value(), mid))()
	whole := geom.Rect{Max: box.Point()}
	r := min(box.W, box.H) / 2
	c := faded(ink, 0.8+0.2*b.hover.Value())
	switch {
	case b.primary:
		p.ShadowRRect(whole, r, paint.Solid(b.color), paint.Shadow{Blur: 14 + 8*b.hover.Value(), Color: faded(b.color, 0.4)})
		c = night
	case b.hover.Value() > 0.01:
		p.RRect(whole, r, paint.Solid(faded(ink, 0.08*b.hover.Value())))
	}
	side := min(box.W, box.H) * 0.46
	widget.PaintIcon(p, f.Theme, b.ic, geom.Rc(mid.X-side/2, mid.Y-side/2, side, side), c)
}

// valueChip is a value to drag: a label over a figure, which a drag
// across or up moves, the wheel steps and a double-click sets back.
type valueChip struct {
	anim.Group
	label  string
	value  float64
	text   func(v float64) string
	step   float64 // per pixel dragged
	notch  float64 // per notch of the wheel
	lo, hi float64
	reset  float64
	send   func(v float64, u *gunim.UI)
	held   bool
	from   geom.Point
	start  float64
	hover  *anim.Float
	size   geom.Size
}

func newValueChip(label string, show func(float64) string, step, notch, lo, hi, reset float64, send func(float64, *gunim.UI)) *valueChip {
	c := &valueChip{label: label, text: show, step: step, notch: notch, lo: lo, hi: hi, reset: reset, send: send, hover: anim.NewFloat(0)}
	c.Add(c.hover)
	return c
}

// DragsTouch implements [gunim.TouchDragger].
func (c *valueChip) DragsTouch() bool { return c.held }

// Focusable implements [gunim.Focusable].
func (c *valueChip) Focusable() bool { return false }

func (c *valueChip) set(v float64, u *gunim.UI) {
	v = max(c.lo, min(v, c.hi))
	if v != c.value {
		c.value = v
		c.send(v, u)
	}
}

// Handle implements [gunim.Handler].
func (c *valueChip) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerEnter:
		c.hover.Animate(1, anim.Snappy)
	case input.PointerLeave:
		if !c.held {
			c.hover.Animate(0, anim.Gentle)
		}
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		if e.Clicks == 2 {
			c.set(c.reset, u)
			break
		}
		c.held, c.from, c.start = true, e.Pos, c.value
	case input.PointerMove:
		if !c.held {
			return false
		}
		d := (e.Pos.X - c.from.X) - (e.Pos.Y - c.from.Y)
		fine := 1.0
		if e.Mods.Has(input.ModShift) {
			fine = 0.1
		}
		c.set(c.start+float64(d)*c.step*fine, u)
	case input.PointerUp:
		c.held = false
	case input.Scroll:
		n := float64(e.Notches.Y)
		if n == 0 {
			n = float64(e.Delta.Y) / 40
		}
		c.set(c.value+n*c.notch, u)
	default:
		return false
	}
	u.Invalidate()
	return true
}

// Layout implements [gunim.Node].
func (c *valueChip) Layout(cs gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	c.size = cs.Max
	return cs.Max
}

// Paint implements [gunim.Node].
func (c *valueChip) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, _ gunim.Children) {
	whole := geom.Rect{Max: box.Point()}
	fill := faded(ink, 0.05+0.05*c.hover.Value())
	if c.held {
		fill = faded(teal, 0.18)
	}
	p.RRect(whole, 10, paint.Solid(fill))
	shaped(c.label, 9, true).Paint(p, geom.Pt(10, 6), faded(teal, 0.85))
	paintFit(p, c.text(c.value), 14, true, geom.Pt(10, 19), box.W-14, ink)
}

// pill is a button of words, lit while on.
type pill struct {
	anim.Group
	words      string
	press      func(*gunim.UI)
	primary    bool
	hover, lit *anim.Float
	down       *anim.Float
	held       bool
	size       geom.Size
}

func newPill(words string, press func(*gunim.UI)) *pill {
	b := &pill{words: words, press: press, hover: anim.NewFloat(0), lit: anim.NewFloat(0), down: anim.NewFloat(0)}
	b.Add(b.hover, b.lit, b.down)
	return b
}

func (b *pill) setLit(on bool) {
	b.lit.Animate(map[bool]float32{false: 0, true: 1}[on], anim.Snappy)
}

// Focusable implements [gunim.Focusable].
func (b *pill) Focusable() bool { return false }

// Handle implements [gunim.Handler].
func (b *pill) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerEnter:
		b.hover.Animate(1, anim.Snappy)
	case input.PointerLeave:
		b.hover.Animate(0, anim.Gentle)
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		b.held = true
		b.down.Animate(1, anim.Spring{Response: 0.12, Damping: 1})
	case input.PointerUp:
		if !b.held {
			return false
		}
		b.held = false
		b.down.Animate(0, anim.Spring{Response: 0.4, Damping: 0.45})
		if (geom.Rect{Max: b.size.Point()}).Contains(e.Pos) {
			u.Cue(gunim.CuePress, b)
			b.press(u)
		}
	default:
		return false
	}
	u.Invalidate()
	return true
}

// Layout implements [gunim.Node]: as wide as its words.
func (b *pill) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	b.size = c.Max
	return c.Max
}

// Paint implements [gunim.Node].
func (b *pill) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, _ gunim.Children) {
	mid := geom.Pt(box.W/2, box.H/2)
	defer p.Push(paint.Scale(1-0.05*b.down.Value(), mid))()
	whole := geom.Rect{Max: box.Point()}
	lit := b.lit.Value()
	words := mix(faded(ink, 0.8), teal, lit)
	switch {
	case b.primary:
		p.ShadowRRect(whole, box.H/2, paint.Solid(teal), paint.Shadow{Blur: 12 + 8*b.hover.Value(), Color: faded(teal, 0.35)})
		words = night
	default:
		p.RRect(whole, box.H/2, paint.Solid(faded(mix(ink, teal, lit), 0.06+0.06*b.hover.Value()+0.08*lit)))
	}
	run := shaped(b.words, 12, true)
	run.Paint(p, geom.Pt((box.W-run.Advance)/2, (box.H-15)/2), words)
}

// pillWidth is the width a pill of words takes.
func pillWidth(words string) float32 { return shaped(words, 12, true).Advance + 28 }
