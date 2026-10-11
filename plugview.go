package main

import (
	"bytes"
	"maps"

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

// Param implements [plug.Host]: of a ReadOnly parameter, what the slot's
// plugin playing reports.
func (h goHost) Param(id uint32) float64 {
	if p, ok := h.v.d.Param(id); ok && p.Flags&plug.ReadOnly != 0 {
		return h.v.reported(p)
	}
	return h.v.values[id]
}

// reported returns what the slot's plugin playing reports of p, or p's
// default where none plays.
func (v *goView) reported(p plug.Param) float64 {
	if r, ok := v.playing().(plug.Reporter); ok {
		return r.Report(p.ID)
	}
	return p.Default
}

// playing returns the processor of the slot's plugin playing, or nil.
func (v *goView) playing() plug.Processor {
	if got, ok := livePlugins.Load(v.slot); ok {
		if lp, ok := got.(*livePlugin); ok {
			if e, ok := lp.p.(*goEngine); ok {
				return e.proc
			}
		}
	}
	return nil
}

// Traced implements [plug.Host].
func (h goHost) Traced(id uint32, f int64) (float32, bool) {
	if t, ok := h.v.playing().(plug.Tracer); ok {
		if tr := t.Trace(id); tr != nil {
			return tr.Get(f)
		}
	}
	return 0, false
}

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

// Sound implements [plug.Host]: the sound the slot's plugin plays, as
// it is heard, wherever the track plays.
func (h goHost) Sound() (in, out plug.Sound) {
	return heardSound{slot: h.v.slot, d: h.v.r.d}, heardSound{slot: h.v.slot, d: h.v.r.d, out: true}
}

// heardSound is the sound fed into a slot's plugin, or, out, the sound
// that comes out, read from its taps up to the frame heard. The plugin
// is found anew at each read, so a chain loaded since, or anew, is
// heard as well.
type heardSound struct {
	slot int
	d    *deck
	out  bool
}

// Heard implements [plug.Sound].
func (s heardSound) Heard(dst []float32) (frames int, at int64, rate int) {
	v, ok := livePlugins.Load(s.slot)
	if !ok {
		return 0, 0, 0
	}
	lp, ok := v.(*livePlugin)
	if !ok {
		return 0, 0, 0
	}
	upto, rate, ok := s.d.heardFed()
	if !ok {
		return 0, 0, 0
	}
	t := lp.listen()
	tap := t.in
	if s.out {
		tap = t.out
	}
	n := int64(len(dst) / 2)
	got := tap.read(dst[:0], upto-n, upto)
	return len(got) / 2, upto, rate
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
		r.pane.forget(v, u)
		v.close()
	}
}

// goPane is a Go plugin's views over the waveform, on a sheet of the
// plugin's own: its name and a close button along the top, and the
// view under them. A sheet fades and rises in as the pane opens, and
// fades and falls away as it closes, and as another plugin's takes its
// place the two cross. Escape closes it.
type goPane struct {
	r *root
	// v is the views shown, and sheets the sheets made, by views: one
	// leaving still holds its views, and comes back if they open again
	// as it leaves.
	v      *goView
	sheets map[*goView]*goSheet
	size   geom.Size
}

func newGoPane(r *root) *goPane { return &goPane{r: r, sheets: map[*goView]*goSheet{}} }

// open shows v's views, of a slot of chain.
func (p *goPane) open(v *goView, chain []Slot, u *gunim.UI) {
	if p.v == v {
		return
	}
	if p.v != nil {
		p.leave(u)
	}
	sh := p.sheets[v]
	if sh == nil {
		sh = newGoSheet(p, v)
		p.sheets[v] = sh
	}
	sh.title.Text = v.d.Name
	for _, s := range chain {
		if s.ID == v.slot {
			sh.title.Text = s.title()
		}
	}
	// A sheet still leaving turns back.
	u.Insert(p, sh)
	p.v = v
	p.r.chain.showOpen(v.slot, true)
	u.Invalidate()
}

// shut closes the pane.
func (p *goPane) shut(u *gunim.UI) {
	if p.v != nil {
		p.leave(u)
		u.Invalidate()
	}
}

// leave has the sheet shown leave.
func (p *goPane) leave(u *gunim.UI) {
	p.r.chain.showOpen(p.v.slot, false)
	u.Remove(p.sheets[p.v])
	p.v = nil
}

// forget lets the sheet of v go, as its views go.
func (p *goPane) forget(v *goView, u *gunim.UI) {
	if p.v == v {
		p.leave(u)
	}
	delete(p.sheets, v)
}

// isOpen says the pane shows a plugin's views.
func (p *goPane) isOpen() bool { return p.v != nil }

// Children implements [gunim.Composite]: the sheets come and go as the
// pane opens and closes.
func (p *goPane) Children() []gunim.Node { return nil }

const goPaneBar = 40

// Layout implements [gunim.Node]: each sheet, the one shown and those
// leaving, fills the pane.
func (p *goPane) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	p.size = c.Max
	if kids.Len() == 0 {
		return geom.Size{}
	}
	for k := range kids.All {
		k.Layout(gunim.Tight(p.size))
		k.Place(geom.Point{})
	}
	return p.size
}

// Paint implements [gunim.Node].
func (p *goPane) Paint(pt *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(pt)
	}
}

// Handle implements [gunim.Handler]: the pane takes the pointer while
// open, so nothing under it does, and Escape closes it.
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

// goSheet is the sheet of a plugin's views, in the pane.
type goSheet struct {
	anim.Group
	p      *goPane
	v      *goView
	title  *widget.Label
	closer *widget.IconButton
	// in is how far the sheet has come in, from 0 to 1, and leaving
	// says it goes.
	in      *anim.Float
	leaving bool
}

func newGoSheet(p *goPane, v *goView) *goSheet {
	sh := &goSheet{p: p, v: v, title: widget.NewLabel(""), in: anim.NewFloat(0)}
	sh.title.NoWrap, sh.title.MaxLines = true, 1
	sh.closer = widget.NewIconButton(icon.X, "Close")
	sh.closer.OnClick = func(u *gunim.UI) gunim.Intent {
		p.shut(u)
		return nil
	}
	sh.Add(sh.in)
	return sh
}

// Children implements [gunim.Composite].
func (sh *goSheet) Children() []gunim.Node {
	return []gunim.Node{sh.title, sh.closer, sh.v.paneView()}
}

// Transition implements [gunim.Transitioner]: in with a little spring,
// and away a little quicker.
func (sh *goSheet) Transition(pr gunim.Presence, _ gunim.Frame) bool {
	switch pr {
	case gunim.Entering:
		sh.leaving = false
		sh.in.Animate(1, anim.Spring{Response: 0.32, Damping: 0.86})
	case gunim.Exiting:
		sh.leaving = true
		sh.in.Animate(0, anim.Spring{Response: 0.22, Damping: 1})
	case gunim.Present:
	}
	return !sh.in.Active()
}

// Layout implements [gunim.Node].
func (sh *goSheet) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	kids.At(1).Layout(gunim.Tight(geom.Sz(32, 32)))
	kids.At(1).Place(geom.Pt(size.W-40, 4))
	kids.At(0).Layout(gunim.Constraints{Max: geom.Sz(max(size.W-72, 0), 24)})
	kids.At(0).Place(geom.Pt(16, 10))
	kids.At(2).Layout(gunim.Tight(geom.Sz(max(size.W-32, 0), max(size.H-goPaneBar-12, 0))))
	kids.At(2).Place(geom.Pt(16, goPaneBar))
	return size
}

// Paint implements [gunim.Node]: faded and lowered as far as the sheet
// is out.
func (sh *goSheet) Paint(pt *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	in := min(max(sh.in.Value(), 0), 1)
	if in < 0.005 {
		return
	}
	pal := colours(f.Theme)
	r := geom.Rect{Max: box.Point()}
	defer pt.Push(paint.Translate(geom.Pt(0, (1-in)*18)))()
	end := pt.Layer(paint.LayerOpts{Bounds: r, Opacity: in})
	defer end()
	pt.RRect(r, 10, paint.Solid(pal.raised))
	pal.outline(pt, r, 10, 1)
	for k := range kids.All {
		k.Paint(pt)
	}
}

// Handle implements [gunim.Handler]: a sheet leaving takes nothing.
func (sh *goSheet) Handle(_ input.Event, _ *gunim.UI) bool { return false }

// Focusable implements [gunim.Focusable].
func (sh *goSheet) Focusable() bool { return false }

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
