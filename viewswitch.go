package main

import (
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// viewSwitch says what the track's area shows, and switches it: the
// waveform, the spectrogram, and, while a plugin's pane is open over
// them, the plugin, its name with a cross that closes it. Picking the
// waveform or the spectrogram closes the plugin too. The lit part glides
// to what is shown, and the plugin's part grows in as it opens and away
// as it closes.
type viewSwitch struct {
	anim.Group
	r *root
	// at is where the lit part is, by part, gliding; plugIn how far the
	// plugin's part is in; hot the part under the pointer, or -1, and
	// onClose whether the pointer is on its cross.
	at, plugIn *anim.Float
	hot        int
	onClose    bool
	// plugName is the plugin's name, kept as its part goes.
	plugName string
	tip      widget.PartTip
	parts    [3]geom.Rect
	size     geom.Size
}

// The switch's parts: the waveform, the spectrogram, and the plugin.
const (
	partWave = iota
	partGram
	partPlugin
)

var partNames = [2]string{"Wave", "Spectrogram"}

// switchH is how tall the switch is, and switchClose how wide the
// plugin's cross.
const (
	switchH     = 30
	switchClose = 20
)

func newViewSwitch(r *root) *viewSwitch {
	s := &viewSwitch{r: r, at: anim.NewFloat(0), plugIn: anim.NewFloat(0), hot: -1}
	s.Add(s.at, s.plugIn)
	return s
}

// shown returns the part shown now: the plugin's while its pane is
// open, and else the view's.
func (s *viewSwitch) shown() int {
	switch {
	case s.r.pane.isOpen():
		return partPlugin
	case s.r.state.View == ViewGram:
		return partGram
	}
	return partWave
}

// Step implements [gunim.Animator]: the lit part glides to what is
// shown, and the plugin's part comes and goes with its pane.
func (s *viewSwitch) Step(dt time.Duration) bool {
	open := s.r.pane.isOpen()
	if open {
		s.plugName = s.r.pane.title()
	}
	sp := anim.Spring{Response: 0.28, Damping: 0.86}
	if to := float32(s.shown()); s.at.Target() != to {
		s.at.Animate(to, sp)
	}
	if to := onOff(open); s.plugIn.Target() != to {
		s.plugIn.Animate(to, sp)
	}
	return s.Group.Step(dt)
}

// partW is how wide each part is, the plugin's as far in as it is.
func (s *viewSwitch) partW(i int) float32 {
	if i == partPlugin {
		return (pillWidth(s.plugName) + switchClose) * min(max(s.plugIn.Value(), 0), 1)
	}
	return pillWidth(partNames[i])
}

// width is how wide the switch is now.
func (s *viewSwitch) width() float32 { return s.partW(0) + s.partW(1) + s.partW(2) }

// Layout implements [gunim.Node].
func (s *viewSwitch) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	s.size = c.Constrain(geom.Sz(s.width(), switchH))
	x := float32(0)
	for i := range s.parts {
		w := s.partW(i)
		s.parts[i] = geom.Rc(x, 0, w, switchH)
		x += w
	}
	return s.size
}

// litAt is where the lit part is, between the parts as it glides.
func (s *viewSwitch) litAt() geom.Rect {
	at := min(max(s.at.Value(), 0), 2)
	i := min(int(at), 1)
	t := at - float32(i)
	a, b := s.parts[i], s.parts[i+1]
	lerp := func(x, y float32) float32 { return x + (y-x)*t }
	return geom.Rc(lerp(a.Min.X, b.Min.X), 0, lerp(a.Size().W, b.Size().W), switchH)
}

// Paint implements [gunim.Node]: a pill of the parts, the one shown lit.
func (s *viewSwitch) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	pal := colours(f.Theme)
	whole := geom.Rect{Max: box.Point()}
	p.RRect(whole, switchH/2, paint.Solid(faded(pal.ink, 0.06)))
	pal.outline(p, whole, switchH/2, 1)
	if s.hot >= 0 && s.hot != s.shown() {
		p.RRect(s.parts[s.hot].Inset(geom.Uniform(2)), switchH/2-2, paint.Solid(faded(pal.ink, 0.07)))
	}
	p.RRect(s.litAt().Inset(geom.Uniform(2)), switchH/2-2, paint.Solid(faded(pal.teal, 0.9)))
	lit := s.at.Value()
	for i := range s.parts {
		r := s.parts[i]
		if r.Size().W < 4 {
			continue
		}
		on := 1 - min(abs32(lit-float32(i)), 1)
		ink := mix(pal.quiet(0.8), pal.night, on)
		if i < partPlugin {
			run := shaped(partNames[i], 12, true)
			run.Paint(p, geom.Pt(r.Min.X+(r.Size().W-run.Advance)/2, (box.H-15)/2), ink)
			continue
		}
		// The plugin's name, and its cross, as far in as the part is.
		in := min(max(s.plugIn.Value(), 0), 1)
		end := p.Layer(paint.LayerOpts{Bounds: r, Opacity: in, Clip: true, Radius: switchH / 2})
		run := shaped(s.plugName, 12, true)
		run.Paint(p, geom.Pt(r.Min.X+14, (box.H-15)/2), ink)
		cross := s.closeAt()
		if s.onClose {
			p.RRect(cross.Inset(geom.Uniform(-3)), cross.Size().W, paint.Solid(faded(pal.night, 0.2)))
		}
		widget.PaintIcon(p, f.Theme, icon.X, cross, ink)
		end()
	}
}

// closeAt is where the plugin's cross is.
func (s *viewSwitch) closeAt() geom.Rect {
	r := s.parts[partPlugin]
	return geom.Rc(r.Max.X-switchClose-6, (switchH-12)/2, 12, 12)
}

// at returns the part at pos, or -1, and whether pos is on the cross.
func (s *viewSwitch) partAt(pos geom.Point) (int, bool) {
	for i, r := range s.parts {
		if r.Size().W > 4 && r.Contains(pos) {
			return i, i == partPlugin && pos.X >= s.closeAt().Min.X-6
		}
	}
	return -1, false
}

// tipOf is the tooltip of part i, or its cross.
func (s *viewSwitch) tipOf(i int, cross bool) string {
	switch {
	case i == partWave:
		return "Show the track's waveform"
	case i == partGram:
		return "Show the track's spectrogram: its pitches, over time"
	case cross:
		return "Close " + s.plugName
	case i == partPlugin:
		return s.plugName + " is open over the track"
	}
	return ""
}

// pick shows part i: the waveform or the spectrogram, closing the
// plugin's pane; or, on the plugin's cross, closes it.
func (s *viewSwitch) pick(i int, cross bool, u *gunim.UI) {
	switch {
	case i == partWave || i == partGram:
		u.Cue(gunim.CueSelect, s)
		s.r.pane.shut(u)
		want := ViewWave
		if i == partGram {
			want = ViewGram
		}
		if s.r.state.View != want {
			u.Send(s, SetView{View: want})
		}
	case cross:
		u.Cue(gunim.CueSelect, s)
		s.r.pane.shut(u)
	}
	u.Invalidate()
}

// Focusable implements [gunim.Focusable].
func (s *viewSwitch) Focusable() bool { return true }

// Handle implements [gunim.Handler]: a click picks a part, and the
// arrow keys step between the waveform and the spectrogram.
func (s *viewSwitch) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		i, cross := s.partAt(e.Pos)
		s.tip.Handle(e, u, s, s.tipOf(i, cross))
		if i != s.hot || cross != s.onClose {
			s.hot, s.onClose = i, cross
			u.Invalidate()
		}
	case input.PointerLeave:
		s.tip.Handle(e, u, s, "")
		s.hot, s.onClose = -1, false
		u.Invalidate()
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		s.tip.Hide(u)
		i, cross := s.partAt(e.Pos)
		s.pick(i, cross, u)
	case input.KeyPress:
		switch e.Key {
		case input.KeyLeft:
			s.pick(partWave, false, u)
		case input.KeyRight:
			s.pick(partGram, false, u)
		default:
			return false
		}
	default:
		return false
	}
	return true
}

// Access implements [gunim.Accessible]: a list of tabs, the one shown
// picked.
func (s *viewSwitch) Access() access.Info {
	info := access.Info{Role: access.RoleTabList, Name: "View"}
	names := []string{partNames[0], partNames[1]}
	if s.r.pane.isOpen() {
		names = append(names, s.plugName)
	}
	for i, n := range names {
		part := access.Info{Role: access.RoleTab, Name: n, Description: s.tipOf(i, false), Bounds: s.parts[i],
			Actions: []string{access.ActionPress}}
		if i == s.shown() {
			part.State = access.StateSelected
		}
		info.Parts = append(info.Parts, part)
	}
	if s.r.pane.isOpen() {
		info.Parts = append(info.Parts, access.Info{Role: access.RoleButton, Name: "Close " + s.plugName,
			Bounds: s.closeAt(), Actions: []string{access.ActionPress}})
	}
	return info
}

// AccessAct implements [gunim.AccessActor]: pressing a part picks it.
func (s *viewSwitch) AccessAct(r access.Request, u *gunim.UI) bool {
	if r.Action != access.ActionPress || r.Part < 0 || r.Part > 3 {
		return false
	}
	s.pick(min(r.Part, partPlugin), r.Part == 3, u)
	return true
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
