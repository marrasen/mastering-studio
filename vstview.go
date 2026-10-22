package main

import (
	"errors"
	"log"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/plugui"
	"github.com/marrasen/gunim/audio/vst3"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// The VST3 plugins' windows, the window's half: a bar the studio draws,
// with the browser's button, steps to the presets beside the one playing
// and the window's buttons; the browser of presets at the left while it
// shows, with a field that keeps the plugin's state as a preset under
// it; and the plugin's own editor in the rest, which the window fits.

// registerPluginView has w show a plugin's window: ed's editor, and the
// presets of class, where it is known.
func registerPluginView(w *gunim.Window, ed embedder, class *vst3.Class) {
	gunim.RegisterView(w, "plugin",
		func(s PluginWin) *pluginHost { return newPluginHost(ed, class, s) },
		func(h *pluginHost, s PluginWin, u *gunim.UI) { h.show(s, u) })
}

// pluginBarH is how tall a plugin window's bar stands.
const pluginBarH = 30

// The browser comes in fading, and gliding browseGlide leftwards out from
// the editor, on browseIn; it goes the way it came, over browseOut, and
// the window then gives its room up.
const (
	browseGlide = 48
	browseOut   = 160 * time.Millisecond
)

var browseIn = anim.Spring{Response: 0.3, Damping: 0.9}

// pluginHost fills a plugin's window.
type pluginHost struct {
	anim.Group
	state PluginWin
	class *vst3.Class
	bar   *plugBar
	slot  *plugui.EditorSlot
	// browser lists the presets of src, and keep names the state to
	// keep as one; browsing says the browser shows.
	src      *vstPresets
	browser  *plugui.PresetBrowser
	keep     *widget.TextField
	browsing bool
	// shown is how far the browser has come in, from 0 to 1; wide says
	// the window holds its room, which it keeps while the browser goes,
	// and shrink stops the window giving that room up once it has.
	shown  *anim.Float
	wide   bool
	shrink func()
	// box is the room the editor had as last laid out, and hold the room
	// it keeps until holdUntil, while the window takes its new size.
	box, hold geom.Size
	holdUntil time.Time
}

// holdFor is the longest the editor keeps its room while the window
// takes its new size.
const holdFor = 600 * time.Millisecond

func newPluginHost(ed embedder, class *vst3.Class, s PluginWin) *pluginHost {
	h := &pluginHost{state: s, class: class, browsing: s.Browse, wide: s.Browse}
	h.src = &vstPresets{h: h}
	h.shown = anim.NewFloat(onOff(s.Browse))
	h.Add(h.shown)
	h.slot = plugui.NewEditorSlot(ed)
	h.slot.Fit = true
	h.slot.Failed = func(err error, u *gunim.UI) {
		u.Send(h, EmbedFailed{Slot: s.Slot, Why: err.Error(), None: errors.Is(err, plug.ErrNoEditor)})
	}
	h.browser = plugui.NewPresetBrowser(h.src)
	h.browser.BankIcon = bankIcon
	h.keep = widget.NewTextField()
	h.keep.Icon = icon.Save
	h.keep.Placeholder = "Keep this sound as…"
	h.keep.OnCommit = func(text string, u *gunim.UI) gunim.Intent {
		h.keep.SetText("", u)
		return KeepPreset{Track: h.state.Track, Slot: h.state.Slot, Name: text}
	}
	h.bar = newPlugBar(h)
	return h
}

// bankIcon is the icon of a bank of presets.
func bankIcon(bank string) *icon.Icon {
	switch bank {
	case vst3.UserBank:
		return icon.User
	case vst3.FactoryBank:
		return icon.Factory
	}
	return icon.Library
}

// show shows the window as the application has it now.
func (h *pluginHost) show(s PluginWin, u *gunim.UI) {
	was := h.state
	h.state = s
	if s.Title != was.Title {
		h.bar.title.Text = s.Title
		u.SetTitle(s.Title)
	}
	if s.Kept != was.Kept {
		h.browser.Refresh(u)
	}
	if s.Playing != was.Playing || s.Current != was.Current {
		h.browser.Follow(u)
	}
}

// toggleBrowse opens the browser at the window's left, growing the
// window leftwards so the editor stays where it is, or closes it; the
// settings keep which, for the plugins' windows to open so. The window
// grows at once and the browser comes in; closing, the browser goes
// first, and the window shrinks after.
func (h *pluginHost) toggleBrowse(u *gunim.UI) {
	h.browsing = !h.browsing
	if h.shrink != nil {
		h.shrink()
		h.shrink = nil
	}
	if h.browsing {
		if !h.wide {
			// The browser takes the place of the edge at the left too.
			h.wide = true
			h.reshape(browseW-gunim.ResizeEdge, u)
		}
		h.shown.Animate(1, browseIn)
		h.browser.Refresh(u)
		h.browser.Follow(u)
	} else {
		h.shown.Animate(0, anim.Tween{Duration: browseOut, Ease: anim.EaseInOut})
		h.shrink = u.After(browseOut, func(u *gunim.UI) {
			h.shrink = nil
			if h.wide && !h.browsing {
				h.wide = false
				h.reshape(gunim.ResizeEdge-h.sideWidth(), u)
				u.Invalidate()
			}
		})
	}
	u.Send(h, BrowsePresets{On: h.browsing})
	u.Invalidate()
}

// reshape grows the window leftwards by by, or shrinks it where by is
// negative. The editor keeps its size while the window grows or shrinks
// round it, as the system takes a moment to.
func (h *pluginHost) reshape(by float32, u *gunim.UI) {
	h.hold, h.holdUntil = h.box, time.Now().Add(holdFor)
	_ = u.Reshape(geom.Insets{Left: by})
}

// sideWidth is how wide the browser stands, as last laid out.
func (h *pluginHost) sideWidth() float32 {
	if h.bar.side > 0 {
		return h.bar.side
	}
	return browseW
}

// Children implements [gunim.Composite].
func (h *pluginHost) Children() []gunim.Node {
	return []gunim.Node{h.bar, h.browser, h.keep, h.slot}
}

// sideInset is how far in the browser stands from the side's edges, and
// fieldH how tall the field that keeps a preset stands.
const (
	sideInset = 12
	fieldH    = 32
)

// Layout implements [gunim.Node]: the bar across the top; under it the
// browser at the left while the window holds its room, and the editor
// in the rest, gunim.ResizeEdge in from the window's other edges: the
// editor takes every press over it, and the window is sized by them.
func (h *pluginHost) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	side := float32(0)
	if h.wide {
		side = min(browseW, max(size.W-120, 0))
	}
	h.bar.side = side
	for k := range kids.All {
		switch k.Node() {
		case h.bar:
			k.Layout(gunim.Tight(geom.Sz(size.W, pluginBarH)))
			k.Place(geom.Point{})
		case h.browser:
			bh := max(size.H-pluginBarH-2*sideInset-fieldH-sideInset, 0)
			if side == 0 {
				bh = 0
			}
			k.Layout(gunim.Tight(geom.Sz(max(side-2*sideInset, 0), bh)))
			k.Place(geom.Pt(sideInset, pluginBarH+sideInset))
		case h.keep:
			if side == 0 {
				k.Layout(gunim.Tight(geom.Size{}))
			} else {
				k.Layout(gunim.Tight(geom.Sz(max(side-2*sideInset, 0), fieldH)))
			}
			k.Place(geom.Pt(sideInset, size.H-sideInset-fieldH))
		default:
			left := side
			if side == 0 {
				left = gunim.ResizeEdge
			}
			box := geom.Sz(max(size.W-left-gunim.ResizeEdge, 0), max(size.H-pluginBarH-gunim.ResizeEdge, 0))
			if h.hold != (geom.Size{}) {
				if box == h.hold || f.Now.After(h.holdUntil) {
					h.hold = geom.Size{}
				} else {
					box = h.hold
				}
			}
			h.box = box
			k.Layout(gunim.Tight(box))
			k.Place(geom.Pt(left, pluginBarH))
		}
	}
	return size
}

// Paint implements [gunim.Node]: the side under the browser, a shade
// apart from the editor, and the browser on it, both as far in as the
// browser has come.
func (h *pluginHost) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	for k := range kids.All {
		if n := k.Node(); n != h.browser && n != h.keep {
			k.Paint(p)
		}
	}
	side, in := h.bar.side, h.shown.Value()
	if side <= 0 || in <= 0 {
		return
	}
	area := geom.Rc(0, pluginBarH, side, box.H-pluginBarH)
	defer p.Layer(paint.LayerOpts{Bounds: area, Opacity: min(in, 1), Clip: true})()
	p.RRect(area, 0, paint.Solid(widget.PanelFill.Get(f.Theme)))
	defer p.Push(paint.Translate(geom.Pt((1-in)*browseGlide, 0)))()
	for k := range kids.All {
		if n := k.Node(); n == h.browser || n == h.keep {
			k.Paint(p)
		}
	}
}

// plugBar is a plugin window's bar: the browser's button and the steps
// to the presets beside the one playing at its start, the title in the
// middle, and the window's buttons at its end.
type plugBar struct {
	h                  *pluginHost
	browse, prev, next *widget.IconButton
	title              *widget.WindowTitle
	controls           *widget.WindowControls
	// side is how wide the browser stands below, as last laid out.
	side float32
}

func newPlugBar(h *pluginHost) *plugBar {
	b := &plugBar{h: h, title: widget.NewWindowTitle(h.state.Title), controls: widget.NewWindowControls()}
	b.controls.Compact, b.controls.Pin = true, true
	b.browse = widget.NewIconButton(icon.LibraryBig, "Presets")
	b.browse.OnClick = func(u *gunim.UI) gunim.Intent { h.toggleBrowse(u); return nil }
	b.prev = widget.NewIconButton(icon.ChevronLeft, "The preset before")
	b.prev.OnClick = func(u *gunim.UI) gunim.Intent { h.browser.Skip(-1, u); return nil }
	b.next = widget.NewIconButton(icon.ChevronRight, "The next preset")
	b.next.OnClick = func(u *gunim.UI) gunim.Intent { h.browser.Skip(1, u); return nil }
	return b
}

// Children implements [gunim.Composite].
func (b *plugBar) Children() []gunim.Node {
	return []gunim.Node{b.browse, b.prev, b.next, b.title, b.controls}
}

// barButton is how wide each of the bar's own buttons stands.
const barButton = 30

// Layout implements [gunim.Node].
func (b *plugBar) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	b.browse.Active = b.h.browsing
	ctl := kids.At(4).Layout(gunim.Constraints{Max: size})
	kids.At(4).Place(geom.Pt(size.W-ctl.W, 0))
	x := float32(4)
	for i := range 3 {
		kids.At(i).Layout(gunim.Tight(geom.Sz(barButton, size.H)))
		kids.At(i).Place(geom.Pt(x, 0))
		x += barButton
	}
	kids.At(3).Layout(gunim.Tight(geom.Sz(max(size.W-x-ctl.W, 0), size.H)))
	kids.At(3).Place(geom.Pt(x, 0))
	return size
}

// Paint implements [gunim.Node].
func (b *plugBar) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(widget.MenubarFill.Get(f.Theme)))
	for k := range kids.All {
		k.Paint(p)
	}
}

// vstPresets are the presets a plugin's window lists: the class's
// preset files, the user's and then those every user shares.
type vstPresets struct{ h *pluginHost }

// Presets implements [plugui.PresetSource].
func (s *vstPresets) Presets() []plug.Preset {
	if s.h.class == nil {
		return nil
	}
	return vst3.ClassPresets(*s.h.class)
}

// Current implements [plugui.PresetSource]: the preset picked last,
// while the plugin holds the state it took.
func (s *vstPresets) Current() (plug.Preset, bool) {
	w := s.h.state
	if !w.Playing {
		return plug.Preset{}, false
	}
	return plug.Preset{Bank: w.Current.Bank, Group: w.Current.Group, Name: w.Current.Name}, true
}

// Pick implements [plugui.PresetSource]: the plugin takes the preset's
// state, as one step to undo. The preset playing, picked again, stays.
func (s *vstPresets) Pick(p plug.Preset, u *gunim.UI) {
	if cur, ok := s.Current(); ok && cur.Same(p) {
		return
	}
	st, err := p.State()
	if err != nil {
		log.Printf("mastering: the preset %s: %v", p.Name, err)
		return
	}
	w := s.h.state
	u.Send(s.h, PickPreset{Track: w.Track, Slot: w.Slot, Preset: refOf(p), State: st})
}
