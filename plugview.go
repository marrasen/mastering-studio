package main

import (
	"bytes"
	"maps"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
	"github.com/marrasen/mastering-studio/plug"
)

// The window's half of the Go plugins: each slot's plugin made again
// for its views, a chip in its card and a pane over the waveform, which
// tell the application of each change through a goHost.

// goView is the views of a slot's Go plugin, and the plugin they show,
// made for them: it never processes.
type goView struct {
	r           *root
	track, slot int
	d           plug.Descriptor
	p           plug.Plugin
	// values and state are the settings as the views show them.
	values map[uint32]float64
	state  []byte
	// held are the parameters in a gesture, and sent the values sent
	// whose news has yet to come back: until it does, news of an older
	// value is not shown. editing and sentState are the same of the
	// state.
	held      map[uint32]bool
	sent      map[uint32]float64
	editing   bool
	sentState []byte
	waiting   bool
	// gesture is the gesture under way, and edit its name.
	gesture int
	edit    string
	chip    gunim.Node
	pane    gunim.Node
}

// newGoView returns the views of slot s, on track, or nil where its
// plugin is not built in.
func newGoView(r *root, track int, s Slot) *goView {
	d, ok := plug.Lookup(s.Class)
	if !ok {
		return nil
	}
	v := &goView{r: r, track: track, slot: s.ID, d: d, values: maps.Clone(s.Values), state: s.GoState,
		held: map[uint32]bool{}, sent: map[uint32]float64{}}
	if v.values == nil {
		v.values = goSettings{}.values(&d)
	}
	v.p = d.New(goHost{v})
	_ = v.p.SetState(s.GoState)
	if vw, ok := v.p.(plug.Viewer); ok {
		v.chip = vw.View(plug.Chip, goHost{v})
	}
	return v
}

// paneView returns the view for the pane: the plugin's own in full, or
// as a panel, or the studio's, a slider for each parameter.
func (v *goView) paneView() gunim.Node {
	if v.pane != nil {
		return v.pane
	}
	if vw, ok := v.p.(plug.Viewer); ok {
		v.pane = vw.View(plug.Full, goHost{v})
		if v.pane == nil {
			v.pane = vw.View(plug.Panel, goHost{v})
		}
	}
	if v.pane == nil {
		v.pane = newParamsView(&v.d, goHost{v})
	}
	return v.pane
}

// views are the views made.
func (v *goView) views() []gunim.Node {
	var out []gunim.Node
	for _, n := range []gunim.Node{v.chip, v.pane} {
		if n != nil {
			out = append(out, n)
		}
	}
	return out
}

// take takes slot s's settings, as the application has them: each
// change the views did not make themselves, they follow.
func (v *goView) take(s Slot, u *gunim.UI) {
	for id, val := range s.Values {
		if v.held[id] {
			continue
		}
		if was, ok := v.sent[id]; ok {
			if was == val {
				delete(v.sent, id)
			}
			continue
		}
		if v.values[id] == val {
			continue
		}
		v.values[id] = val
		for _, n := range v.views() {
			if f, ok := n.(plug.Follower); ok {
				f.Follow(id, val, u)
			}
		}
	}
	switch {
	case v.editing:
	case v.waiting:
		if bytes.Equal(s.GoState, v.sentState) {
			v.waiting = false
		}
	case !bytes.Equal(s.GoState, v.state):
		v.state = s.GoState
		_ = v.p.SetState(s.GoState)
		for _, n := range v.views() {
			if f, ok := n.(plug.StateFollower); ok {
				f.FollowState(u)
			}
		}
	}
}

// close lets the plugin go.
func (v *goView) close() { v.p.Close() }

// goHost is the host of a Go plugin's views: it tells the application
// of each change.
type goHost struct{ v *goView }

func (h goHost) Param(id uint32) float64 { return h.v.values[id] }

// Begin implements [plug.Host]: a gesture of its own, to undo.
func (h goHost) Begin(id uint32, _ *gunim.UI) {
	h.v.r.gestures++
	h.v.gesture = h.v.r.gestures
	h.v.held[id] = true
}

// Set implements [plug.Host]: the other views follow, and the
// application sets it.
func (h goHost) Set(id uint32, val float64, u *gunim.UI) {
	v := h.v
	p, ok := v.d.Param(id)
	if !ok {
		return
	}
	if v.gesture == 0 {
		v.r.gestures++
		v.gesture = v.r.gestures
	}
	val = p.Clamp(val)
	v.values[id], v.sent[id] = val, val
	for _, n := range v.views() {
		if f, ok := n.(plug.Follower); ok {
			f.Follow(id, val, u)
		}
	}
	u.Send(nil, SetGoParam{Track: v.track, Slot: v.slot, ID: id, Value: val, Gesture: v.gesture})
	u.Invalidate()
}

// End implements [plug.Host].
func (h goHost) End(id uint32, _ *gunim.UI) {
	delete(h.v.held, id)
	h.v.gesture = 0
}

// BeginEdit implements [plug.Host].
func (h goHost) BeginEdit(name string, _ *gunim.UI) {
	h.v.r.gestures++
	h.v.gesture, h.v.edit, h.v.editing = h.v.r.gestures, name, true
}

// Changed implements [plug.Host]: the chip follows, and the application
// sets the state.
func (h goHost) Changed(u *gunim.UI) {
	v := h.v
	if v.gesture == 0 {
		v.r.gestures++
		v.gesture = v.r.gestures
	}
	st := v.p.State()
	v.state, v.sentState, v.waiting = st, st, true
	if f, ok := v.chip.(plug.StateFollower); ok {
		f.FollowState(u)
	}
	u.Send(nil, SetGoState{Track: v.track, Slot: v.slot, State: st, Name: v.edit})
	u.Invalidate()
}

// EndEdit implements [plug.Host].
func (h goHost) EndEdit(*gunim.UI) {
	h.v.editing, h.v.gesture = false, 0
}

// showGo makes and keeps the views of track t's Go plugins, and lets
// those of plugins gone go.
func (r *root) showGo(t Track, s, was Album, u *gunim.UI) {
	keep := map[int]bool{}
	for _, sl := range t.Chain {
		if !sl.goPlugin() {
			continue
		}
		keep[sl.ID] = true
		v := r.goViews[sl.ID]
		if v == nil || v.track != t.ID {
			if v = newGoView(r, t.ID, sl); v == nil {
				continue
			}
			r.goViews[sl.ID] = v
		}
		v.take(sl, u)
	}
	gone := map[int]*goView{}
	for id, v := range r.goViews {
		if !keep[id] {
			gone[id] = v
			delete(r.goViews, id)
		}
	}
	r.chain.syncChips(u)
	if s.ShowGos != was.ShowGos {
		if v := r.goViews[s.ShowGo]; v != nil {
			r.pane.open(v, t.Chain, u)
		}
	}
	if r.pane.v != nil && r.goViews[r.pane.v.slot] != r.pane.v {
		r.pane.shut(u)
	}
	for _, v := range gone {
		v.close()
	}
}

// goPane is a Go plugin's views over the waveform: its name and a close
// button along the top, and the view under them. Escape closes it.
type goPane struct {
	anim.Group
	v      *goView
	title  *widget.Label
	closer *widget.IconButton
	shown  *anim.Float
	size   geom.Size
}

func newGoPane() *goPane {
	p := &goPane{title: widget.NewLabel(""), shown: anim.NewFloat(0)}
	p.title.NoWrap, p.title.MaxLines = true, 1
	p.closer = widget.NewIconButton(icon.X, "Close")
	p.closer.OnClick = func(u *gunim.UI) gunim.Intent {
		p.shut(u)
		return nil
	}
	p.Add(p.shown)
	return p
}

// open shows v's views, of a slot of chain.
func (p *goPane) open(v *goView, chain []Slot, u *gunim.UI) {
	if p.v != nil && p.v != v {
		u.Remove(p.v.paneView())
	}
	if p.v == nil {
		u.Insert(p, p.title)
		u.Insert(p, p.closer)
	}
	u.Insert(p, v.paneView())
	p.v = v
	p.title.Text = v.d.Name
	for _, s := range chain {
		if s.ID == v.slot {
			p.title.Text = s.title()
		}
	}
	p.shown.Animate(1, anim.Snappy)
	u.Invalidate()
}

// shut closes the pane.
func (p *goPane) shut(u *gunim.UI) {
	if p.v == nil {
		return
	}
	for _, n := range []gunim.Node{p.title, p.closer, p.v.paneView()} {
		u.Remove(n)
	}
	p.v = nil
	p.shown.Jump(0)
	u.Invalidate()
}

// isOpen says the pane shows a plugin's views.
func (p *goPane) isOpen() bool { return p.v != nil }

// Children implements [gunim.Composite].
func (p *goPane) Children() []gunim.Node {
	if p.v == nil {
		return nil
	}
	return []gunim.Node{p.title, p.closer, p.v.paneView()}
}

const goPaneBar = 40

// Layout implements [gunim.Node].
func (p *goPane) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	if p.v == nil {
		p.size = geom.Size{}
		return p.size
	}
	p.size = c.Max
	body := p.v.paneView()
	for k := range kids.All {
		switch k.Node() {
		case p.closer:
			k.Layout(gunim.Tight(geom.Sz(32, 32)))
			k.Place(geom.Pt(p.size.W-40, 4))
		case p.title:
			k.Layout(gunim.Constraints{Max: geom.Sz(p.size.W-72, 24)})
			k.Place(geom.Pt(16, 10))
		case body:
			k.Layout(gunim.Tight(geom.Sz(max(p.size.W-32, 0), max(p.size.H-goPaneBar-12, 0))))
			k.Place(geom.Pt(16, goPaneBar))
		default:
			// A plugin's view, left for another's.
			k.Layout(gunim.Tight(geom.Size{}))
		}
	}
	return p.size
}

// Paint implements [gunim.Node].
func (p *goPane) Paint(pt *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	if p.v == nil {
		return
	}
	pal := colours(f.Theme)
	r := geom.Rect{Max: box.Point()}
	end := pt.Layer(paint.LayerOpts{Bounds: r, Opacity: min(max(p.shown.Value(), 0), 1)})
	defer end()
	pt.RRect(r, 10, paint.Solid(pal.raised))
	pal.outline(pt, r, 10, 1)
	for k := range kids.All {
		k.Paint(pt)
	}
}

// Handle implements [gunim.Handler]: the pane takes the pointer, so
// nothing under it does, and Escape closes it.
func (p *goPane) Handle(e input.Event, u *gunim.UI) bool {
	if p.v == nil {
		return false
	}
	switch e := e.(type) {
	case input.KeyPress:
		if e.Key != input.KeyEscape {
			return false
		}
		p.shut(u)
	case input.PointerDown, input.PointerUp, input.PointerMove, input.Scroll:
	default:
		return false
	}
	return true
}

// Step implements [gunim.Animator].
func (p *goPane) Step(dt time.Duration) bool { return p.Group.Step(dt) }

// paramsView is the studio's view of a plugin without one of its own
// for the pane: a slider for each parameter shown.
type paramsView struct {
	*widget.Flex
	rows map[uint32]*widget.SliderRow
	held map[uint32]bool
}

func newParamsView(d *plug.Descriptor, h plug.Host) *paramsView {
	v := &paramsView{rows: map[uint32]*widget.SliderRow{}, held: map[uint32]bool{}}
	var kids []gunim.Node
	for _, p := range d.Params {
		if p.Flags&(plug.Hidden|plug.ReadOnly) != 0 {
			continue
		}
		s := widget.NewSlider(float32(p.Min), float32(p.Max))
		if p.Steps > 0 {
			s.Snap = float32((p.Max - p.Min) / float64(p.Steps))
		}
		s.Rest, s.HasRest = float32(p.Default), true
		s.SetValue(float32(h.Param(p.ID)), nil)
		row := widget.NewSliderRow(p.Name, s)
		row.Format = func(x float32) string { return p.Text(float64(x)) }
		s.OnChange = func(x float32, u *gunim.UI) gunim.Intent {
			if !v.held[p.ID] {
				h.Begin(p.ID, u)
				v.held[p.ID] = true
			}
			h.Set(p.ID, float64(x), u)
			return nil
		}
		s.OnCommit = func(x float32, u *gunim.UI) gunim.Intent {
			if !v.held[p.ID] {
				h.Begin(p.ID, u)
			}
			h.Set(p.ID, float64(x), u)
			h.End(p.ID, u)
			delete(v.held, p.ID)
			return nil
		}
		v.rows[p.ID] = row
		kids = append(kids, row)
	}
	v.Flex = widget.Column(kids...)
	return v
}

// Follow implements [plug.Follower].
func (v *paramsView) Follow(id uint32, x float64, u *gunim.UI) {
	if row := v.rows[id]; row != nil && !row.Slider.Held() {
		row.Slider.SetValue(float32(x), u)
	}
}
