package main

import (
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// themeName is the window's theme until one is picked: dark, with the
// studio's teal.
const themeName = "mastering"

// studioTheme is a theme to pick from: Name is the name it is
// registered and saved under, Label the words a menu shows for it.
type studioTheme struct {
	Name, Label string
	Theme       theme.Theme
}

// studioThemes is every theme, in the order a menu lists them.
var studioThemes = []studioTheme{
	{"mastering", "Dark", darkTheme()},
	{"light", "Light", lightTheme()},
	{"contrast", "High contrast", contrastTheme()},
	{"dim", "Dim", dimTheme()},
}

// themeNamed is name where it names a theme, and the default theme's
// name for any other, such as one saved by a later version.
func themeNamed(name string) string {
	for _, t := range studioThemes {
		if t.Name == name {
			return name
		}
	}
	return themeName
}

// The studio's colours in each theme. The dark one's are the tokens'
// defaults.
var (
	darkColours = palette{
		ink: rgb(0xec, 0xee, 0xf4), night: rgb(0x0c, 0x0e, 0x13), panel: rgb(0x15, 0x18, 0x20), raised: rgb(0x1d, 0x21, 0x2b),
		teal: rgb(0x4f, 0xd6, 0xc0), sky: rgb(0x5c, 0xb8, 0xff), amber: rgb(0xff, 0xc8, 0x57), coral: rgb(0xff, 0x6b, 0x5f),
	}
	// lightColours are soft greys under near-black ink, with every
	// colour darkened to read on them at 4.5 to 1 or more.
	lightColours = palette{
		ink: rgb(0x1b, 0x1f, 0x27), night: rgb(0xdd, 0xe1, 0xe7), panel: rgb(0xec, 0xee, 0xf2), raised: rgb(0xf7, 0xf8, 0xfa),
		teal: rgb(0x08, 0x66, 0x5a), sky: rgb(0x1a, 0x5a, 0xa0), amber: rgb(0x84, 0x52, 0x00), coral: rgb(0xa3, 0x2b, 0x25),
		lift: 0.4,
	}
	// contrastColours are white and full colours on black, every text
	// at 7 to 1 or more.
	contrastColours = palette{
		ink: rgb(0xff, 0xff, 0xff), night: rgb(0x00, 0x00, 0x00), panel: rgb(0x10, 0x10, 0x10), raised: rgb(0x1c, 0x1c, 0x1c),
		teal: rgb(0x00, 0xff, 0xd5), sky: rgb(0x66, 0xcc, 0xff), amber: rgb(0xff, 0xd2, 0x00), coral: rgb(0xff, 0x8a, 0x80),
		edge: rgb(0xa0, 0xa0, 0xa0), lift: 0.6,
	}
	// dimColours are warm browns under a dimmer, warmer ink, with less
	// blue in every colour, for late sessions.
	dimColours = palette{
		ink: rgb(0xd9, 0xcf, 0xc0), night: rgb(0x14, 0x11, 0x0e), panel: rgb(0x1b, 0x17, 0x14), raised: rgb(0x24, 0x1f, 0x1a),
		teal: rgb(0x8f, 0xc7, 0x9a), sky: rgb(0x9a, 0xa7, 0xc7), amber: rgb(0xe0, 0xa8, 0x4e), coral: rgb(0xe0, 0x7a, 0x5f),
		lift: 0.3,
	}
)

// darkTheme is the studio's own look: gunim's dark widgets in the
// studio's teal.
func darkTheme() theme.Theme {
	return theme.Make(themeName, append(darkColours.entries(),
		theme.Set(widget.Accent, darkColours.teal),
		theme.Set(widget.ButtonPrimaryFill, rgb(0x1f, 0x8f, 0x80)),
		theme.Set(widget.ButtonPrimaryHover, rgb(0x2a, 0xa3, 0x92)))...)
}

// lightTheme is gunim's light widgets, at the dark theme's sizes, on
// the studio's soft greys, in a darker teal.
func lightTheme() theme.Theme {
	c := lightColours
	th := widget.Light().With(append(c.entries(),
		theme.Set(audioui.Short, rgb(0x9c, 0x24, 0x78)),
		theme.Set(widget.Ink, c.ink),
		theme.Set(widget.Background, c.night),
		theme.Set(widget.Accent, c.teal),
		theme.Set(widget.ButtonPrimaryFill, c.teal),
		theme.Set(widget.ButtonPrimaryHover, rgb(0x0b, 0x7a, 0x6b)),
		theme.Set(widget.DialogFill, c.raised),
		theme.Set(widget.Selection, faded(c.teal, 0.25)),
		theme.Set(widget.MenuHot, faded(c.teal, 0.16)),
		theme.Set(widget.LinkInk, c.teal),
		theme.Set(widget.ToastInfoInk, c.teal),
		theme.Set(widget.SliderRowActive, faded(c.teal, 0.11)))...)
	// The dark theme's sizes, as the studio's dialogs are laid out for.
	for _, t := range []theme.Token[float32]{widget.TextSize, widget.ButtonRadius, widget.ButtonPadding,
		widget.ButtonHeight, widget.DialogRadius, widget.DialogPadding, widget.DialogWidth, widget.DialogHeight,
		widget.ListSpacing, widget.RowRadius, widget.CardRadius, widget.Gap, widget.HeadingSize,
		widget.FieldRadius, widget.FieldHeight, widget.MenuRadius, widget.CheckRadius, widget.ControlHeight} {
		th = th.With(theme.Set(t, t.Default()))
	}
	th = th.With(theme.Set(widget.Margin, widget.Margin.Default()), theme.Set(widget.CardPadding, widget.CardPadding.Default()))
	th.Name = "light"
	return th
}

// contrastTheme is white on black, its accent yellow, with a bright
// rule round every field, menu and dialog and a grey fill on every
// button, each part at 3 to 1 or more against black.
func contrastTheme() theme.Theme {
	c := contrastColours
	yellow, black, white := rgb(0xff, 0xe6, 0x00), c.night, c.ink
	return theme.Make("contrast", append(c.entries(),
		theme.Set(audioui.Short, rgb(0xff, 0x7a, 0xf0)),
		theme.Set(widget.Ink, white),
		theme.Set(widget.Background, black),
		theme.Set(widget.Accent, yellow),
		theme.Set(widget.ButtonFill, rgb(0x59, 0x59, 0x59)),
		theme.Set(widget.ButtonHover, rgb(0x40, 0x40, 0x40)),
		theme.Set(widget.ButtonPrimaryFill, yellow),
		theme.Set(widget.ButtonPrimaryHover, rgb(0xff, 0xf2, 0x80)),
		theme.Set(widget.ButtonPrimaryInk, black),
		theme.Set(widget.ButtonDangerFill, rgb(0xff, 0x6b, 0x6b)),
		theme.Set(widget.ButtonDangerHover, rgb(0xff, 0x8f, 0x8f)),
		theme.Set(widget.ButtonStrongInk, black),
		theme.Set(widget.DialogFill, black),
		theme.Set(widget.DialogBorder, white),
		theme.Set(widget.DialogShadow, faded(black, 0)),
		theme.Set(widget.Scrim, faded(black, 0.75)),
		theme.Set(widget.DialogProblem, c.coral),
		theme.Set(widget.DialogDangerInk, c.coral),
		theme.Set(widget.FieldFill, black),
		theme.Set(widget.FieldBorder, white),
		theme.Set(widget.Selection, faded(yellow, 0.45)),
		theme.Set(widget.Placeholder, rgb(0xc8, 0xc8, 0xc8)),
		theme.Set(widget.MenuFill, black),
		theme.Set(widget.MenuBorder, white),
		theme.Set(widget.MenuHot, faded(yellow, 0.35)),
		theme.Set(widget.MenuHint, rgb(0xe0, 0xe0, 0xe0)),
		theme.Set(widget.TooltipFill, white),
		theme.Set(widget.TooltipInk, black),
		theme.Set(widget.CheckMark, black),
		theme.Set(widget.SwitchOff, rgb(0x8a, 0x8a, 0x8a)),
		theme.Set(widget.Knob, white),
		theme.Set(widget.SliderRestMark, white),
		theme.Set(widget.ScrollbarColor, faded(white, 0.7)),
		theme.Set(widget.CardFill, black),
		theme.Set(widget.ChipFill, rgb(0x59, 0x59, 0x59)),
		theme.Set(widget.ChipHover, rgb(0x40, 0x40, 0x40)),
		theme.Set(widget.ChipLead, rgb(0xe0, 0xe0, 0xe0)),
		theme.Set(widget.SplitLine, rgb(0x8a, 0x8a, 0x8a)),
		theme.Set(widget.ProgressTrack, rgb(0x8a, 0x8a, 0x8a)),
		theme.Set(widget.MenubarFill, black),
		theme.Set(widget.MenubarHot, faded(yellow, 0.35)),
		theme.Set(widget.WindowButtonHot, faded(white, 0.3)),
		theme.Set(widget.LinkInk, yellow),
		theme.Set(widget.ToastInfoInk, c.sky),
		theme.Set(widget.ToastSuccessInk, c.teal),
		theme.Set(widget.ToastWarningInk, c.amber),
		theme.Set(widget.ToastErrorInk, c.coral))...)
}

// dimTheme is a warm, dim dark theme for late sessions: browns, a
// warm ink at lower brightness, a sage green where the dark theme has
// teal.
func dimTheme() theme.Theme {
	c := dimColours
	line, fill, hot := rgb(0x3d, 0x34, 0x2b), rgb(0x2e, 0x27, 0x21), rgb(0x3d, 0x34, 0x2b)
	hint := rgb(0xa0, 0x95, 0x86)
	return theme.Make("dim", append(c.entries(),
		theme.Set(audioui.Short, rgb(0xd4, 0x8a, 0xb8)),
		theme.Set(widget.Ink, c.ink),
		theme.Set(widget.Background, c.night),
		theme.Set(widget.Accent, c.teal),
		theme.Set(widget.ButtonFill, fill),
		theme.Set(widget.ButtonHover, hot),
		theme.Set(widget.ButtonPrimaryFill, c.teal),
		theme.Set(widget.ButtonPrimaryHover, rgb(0xa3, 0xd4, 0xad)),
		theme.Set(widget.ButtonPrimaryInk, c.night),
		theme.Set(widget.ButtonDangerFill, rgb(0xa8, 0x4a, 0x36)),
		theme.Set(widget.ButtonDangerHover, rgb(0xb8, 0x58, 0x42)),
		theme.Set(widget.DialogFill, rgb(0x22, 0x1d, 0x18)),
		theme.Set(widget.DialogBorder, line),
		theme.Set(widget.DialogProblem, c.coral),
		theme.Set(widget.DialogDangerInk, c.coral),
		theme.Set(widget.FieldFill, rgb(0x11, 0x0e, 0x0b)),
		theme.Set(widget.FieldBorder, line),
		theme.Set(widget.Selection, faded(c.teal, 0.3)),
		theme.Set(widget.Placeholder, hint),
		theme.Set(widget.MenuFill, rgb(0x24, 0x1e, 0x19)),
		theme.Set(widget.MenuBorder, line),
		theme.Set(widget.MenuHot, faded(c.teal, 0.22)),
		theme.Set(widget.MenuHint, hint),
		theme.Set(widget.TooltipFill, faded(c.ink, 0.96)),
		theme.Set(widget.TooltipInk, c.night),
		theme.Set(widget.CheckMark, c.night),
		theme.Set(widget.SwitchOff, line),
		theme.Set(widget.Knob, c.ink),
		theme.Set(widget.SliderRestMark, faded(hint, 0.75)),
		theme.Set(widget.ScrollbarColor, faded(c.ink, 0.3)),
		theme.Set(widget.CardFill, c.raised),
		theme.Set(widget.ChipFill, fill),
		theme.Set(widget.ChipHover, hot),
		theme.Set(widget.ChipLead, hint),
		theme.Set(widget.SplitLine, fill),
		theme.Set(widget.ProgressTrack, line),
		theme.Set(widget.MenubarFill, c.panel),
		theme.Set(widget.LinkInk, c.teal),
		theme.Set(widget.ToastInfoInk, c.sky),
		theme.Set(widget.ToastSuccessInk, c.teal),
		theme.Set(widget.ToastWarningInk, c.amber),
		theme.Set(widget.ToastErrorInk, c.coral))...)
}
