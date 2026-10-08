package main

import (
	"image/color"
	"math"
	"testing"

	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// The dark theme keeps the look the studio had before it had themes:
// its colours are the tokens' defaults, and the audio pieces' too.
func TestDarkThemeIsTheDefaults(t *testing.T) {
	if got := colours(nil); got != darkColours {
		t.Errorf("the tokens' defaults are %+v, the dark theme's colours %+v", got, darkColours)
	}
	l := theme.NewLive(darkTheme())
	if got := colours(l); got != darkColours {
		t.Errorf("the dark theme gives %+v, want %+v", got, darkColours)
	}
	for _, tok := range []theme.Token[color.NRGBA]{audioui.Ground, audioui.Raised, audioui.Sound,
		audioui.Near, audioui.Over, audioui.Spread} {
		if tok.Get(l) != tok.Default() {
			t.Errorf("the dark theme gives %s %v, want its default %v", tok.Key(), tok.Get(l), tok.Default())
		}
	}
}

// Each theme has a name of its own, which themeNamed knows, and an
// unknown name falls back to the dark one.
func TestThemeNames(t *testing.T) {
	seen := map[string]bool{}
	for _, th := range studioThemes {
		if th.Name != th.Theme.Name {
			t.Errorf("%s is registered as %q", th.Name, th.Theme.Name)
		}
		if seen[th.Name] {
			t.Errorf("%s twice", th.Name)
		}
		seen[th.Name] = true
		if got := themeNamed(th.Name); got != th.Name {
			t.Errorf("themeNamed(%q) = %q", th.Name, got)
		}
	}
	if studioThemes[0].Name != themeName {
		t.Errorf("the first theme is %q, want the default %q", studioThemes[0].Name, themeName)
	}
	for _, name := range []string{"", "neon"} {
		if got := themeNamed(name); got != themeName {
			t.Errorf("themeNamed(%q) = %q, want %q", name, got, themeName)
		}
	}
}

// Text reads on every surface: the light and dim themes at 4.5 to 1
// or more, the high contrast one at 7 to 1, quieter text included.
func TestThemeContrast(t *testing.T) {
	for _, c := range []struct {
		name  string
		least float64
		quiet float32
	}{{"light", 4.5, 0.45}, {"contrast", 7, 0.35}, {"dim", 4.5, 0.45}} {
		var th theme.Theme
		for _, s := range studioThemes {
			if s.Name == c.name {
				th = s.Theme
			}
		}
		l := theme.NewLive(th)
		pal := colours(l)
		for _, bg := range []color.NRGBA{pal.night, pal.panel, pal.raised} {
			for what, fg := range map[string]color.NRGBA{"ink": pal.ink, "teal": pal.teal, "sky": pal.sky,
				"amber": pal.amber, "coral": pal.coral, "quiet ink": over(pal.quiet(c.quiet), bg)} {
				if r := contrast(fg, bg); r < c.least {
					t.Errorf("%s: %s %v on %v is %.2f to 1, want %.1f", c.name, what, fg, bg, r, c.least)
				}
			}
		}
		if r := contrast(widget.Ink.Get(l), widget.DialogFill.Get(l)); r < c.least {
			t.Errorf("%s: a dialog's text is %.2f to 1, want %.1f", c.name, r, c.least)
		}
	}
}

// over is c laid over the opaque bg.
func over(c, bg color.NRGBA) color.NRGBA {
	a := float64(c.A) / 255
	ch := func(f, b uint8) uint8 { return uint8(math.Round(float64(f)*a + float64(b)*(1-a))) }
	return color.NRGBA{R: ch(c.R, bg.R), G: ch(c.G, bg.G), B: ch(c.B, bg.B), A: 0xff}
}

// contrast is the contrast ratio of two opaque colours, as WCAG reckons
// it.
func contrast(a, b color.NRGBA) float64 {
	lum := func(c color.NRGBA) float64 {
		ch := func(v uint8) float64 {
			s := float64(v) / 255
			if s <= 0.04045 {
				return s / 12.92
			}
			return math.Pow((s+0.055)/1.055, 2.4)
		}
		return 0.2126*ch(c.R) + 0.7152*ch(c.G) + 0.0722*ch(c.B)
	}
	x, y := lum(a), lum(b)
	return (max(x, y) + 0.05) / (min(x, y) + 0.05)
}
