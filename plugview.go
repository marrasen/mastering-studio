package main

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/plugui"
	"github.com/marrasen/gunim/audio/vst3"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
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
	// doc is the document the plugin was given last.
	doc plug.Document
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

// takeDoc gives a plugin that reads whole tracks the document of its
// slot, where the app made a new one, and tells its views.
func (v *goView) takeDoc(u *gunim.UI) {
	tp, ok := v.p.(plug.TrackPlugin)
	if !ok {
		return
	}
	got, ok := trackDocs.Load(v.slot)
	if !ok {
		return
	}
	if doc, ok := got.(plug.Document); ok && doc != v.doc {
		v.doc = doc
		tp.SetDocument(doc)
		for _, n := range v.views() {
			if f, ok := n.(plug.DocumentFollower); ok {
				f.FollowDocument(u)
			}
		}
	}
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
		v.takeDoc(u)
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
	for _, v := range slices.Clone(r.pane.shown) {
		if r.goViews[v.slot] != v {
			r.pane.leave(v, u)
		}
	}
	// The sheets' titles, as the plugins are named now, and their
	// browsers, as the patches are.
	for _, sl := range t.Chain {
		sh := r.pane.sheets[r.goViews[sl.ID]]
		if sh == nil {
			continue
		}
		if !sh.renaming {
			sh.title.Text = sl.title()
		}
		sh.follow(sl, u)
	}
	for _, v := range gone {
		r.pane.forget(v, u)
		v.close()
	}
}

// goPane is Go plugins' views over the waveform, each on a sheet of
// the plugin's own: its name and a close button along the top, and the
// view under them. It shows one plugin, or up to paneMost side by side,
// as a click opens one and a Ctrl+click adds one: two side by side,
// three as two over one, four in a grid of two by two. A sheet fades
// and rises in as it opens, and fades and falls away as it closes, and
// as another plugin's takes its place the two cross; as the sheets
// shown change, each glides to its place. Escape closes them all.
type goPane struct {
	r *root
	// shown are the views shown, in order; v is the first, or nil. sheets
	// are the sheets made, by views: one leaving still holds its views,
	// and comes back if they open again as it leaves.
	shown  []*goView
	v      *goView
	sheets map[*goView]*goSheet
	// several is how far the pane shows more than one, gliding.
	several *anim.Float
	size    geom.Size
}

// paneMost is how many plugins the pane shows at once, and paneGap the
// room between their sheets.
const (
	paneMost = 4
	paneGap  = 10
)

func newGoPane(r *root) *goPane {
	return &goPane{r: r, sheets: map[*goView]*goSheet{}, several: anim.NewFloat(0)}
}

// open shows v's views alone, of a slot of chain.
func (p *goPane) open(v *goView, chain []Slot, u *gunim.UI) {
	if len(p.shown) == 1 && p.v == v {
		return
	}
	for _, o := range slices.Clone(p.shown) {
		if o != v {
			p.leave(o, u)
		}
	}
	if !p.shows(v) {
		p.enter(v, chain, u)
	}
	u.Invalidate()
}

// toggle adds v's views to those shown, or, shown already, takes them
// away; past paneMost, the first shown gives way.
func (p *goPane) toggle(v *goView, chain []Slot, u *gunim.UI) {
	if p.shows(v) {
		p.leave(v, u)
	} else {
		if len(p.shown) >= paneMost {
			p.leave(p.shown[0], u)
		}
		p.enter(v, chain, u)
	}
	u.Invalidate()
}

// shows says v's views are shown.
func (p *goPane) shows(v *goView) bool { return slices.Contains(p.shown, v) }

// showsSlot says the views of slot id are shown.
func (p *goPane) showsSlot(id int) bool {
	return slices.ContainsFunc(p.shown, func(v *goView) bool { return v.slot == id })
}

// enter brings v's sheet in, after those shown.
func (p *goPane) enter(v *goView, chain []Slot, u *gunim.UI) {
	sh := p.sheets[v]
	if sh == nil {
		sh = newGoSheet(p, v)
		p.sheets[v] = sh
		// The view starts from the state as it is.
		if f, ok := v.paneView().(plug.StateFollower); ok {
			f.FollowState(u)
		}
	}
	sh.title.Text = v.d.Name
	for _, s := range chain {
		if s.ID == v.slot {
			sh.title.Text = s.title()
		}
	}
	// A sheet still leaving turns back.
	u.Insert(p, sh)
	p.shown = append(p.shown, v)
	p.v = p.shown[0]
	p.r.chain.showOpen(v.slot, true)
	p.arrange()
}

// shut closes the pane.
func (p *goPane) shut(u *gunim.UI) {
	if len(p.shown) > 0 {
		for _, v := range slices.Clone(p.shown) {
			p.leave(v, u)
		}
		u.Invalidate()
	}
}

// leave has v's sheet leave.
func (p *goPane) leave(v *goView, u *gunim.UI) {
	i := slices.Index(p.shown, v)
	if i < 0 {
		return
	}
	p.r.chain.showOpen(v.slot, false)
	u.Remove(p.sheets[v])
	p.shown = slices.Delete(p.shown, i, i+1)
	p.v = nil
	if len(p.shown) > 0 {
		p.v = p.shown[0]
	}
	p.arrange()
}

// forget lets the sheet of v go, as its views go.
func (p *goPane) forget(v *goView, u *gunim.UI) {
	p.leave(v, u)
	delete(p.sheets, v)
}

// isOpen says the pane shows a plugin's views.
func (p *goPane) isOpen() bool { return len(p.shown) > 0 }

// title is the name of the plugins the pane shows, as their sheets have
// them: one's, two's together, or the first's and how many more; ""
// for none.
func (p *goPane) title() string {
	var names []string
	for _, v := range p.shown {
		if sh := p.sheets[v]; sh != nil {
			names = append(names, sh.title.Text)
		}
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " · " + names[1]
	}
	return fmt.Sprintf("%s +%d", names[0], len(names)-1)
}

// cells returns where each of n sheets stands in a pane size big: one
// fills it, two stand side by side, three two over one, four two by
// two.
func cells(n int, size geom.Size) []geom.Rect {
	w2, h2 := (size.W-paneGap)/2, (size.H-paneGap)/2
	switch n {
	case 0:
		return nil
	case 1:
		return []geom.Rect{{Max: size.Point()}}
	case 2:
		return []geom.Rect{geom.Rc(0, 0, w2, size.H), geom.Rc(w2+paneGap, 0, w2, size.H)}
	case 3:
		return []geom.Rect{geom.Rc(0, 0, w2, h2), geom.Rc(w2+paneGap, 0, w2, h2), geom.Rc(0, h2+paneGap, size.W, h2)}
	}
	return []geom.Rect{geom.Rc(0, 0, w2, h2), geom.Rc(w2+paneGap, 0, w2, h2), geom.Rc(0, h2+paneGap, w2, h2),
		geom.Rc(w2+paneGap, h2+paneGap, w2, h2)}
}

// arrange has each sheet shown glide to its cell, one new to the pane
// starting there, and the pane show as one or several.
func (p *goPane) arrange() {
	cs := cells(len(p.shown), p.size)
	for i, v := range p.shown {
		p.sheets[v].moveTo(cs[i])
	}
	p.several.Animate(onOff(len(p.shown) > 1), anim.Spring{Response: 0.3, Damping: 1})
}

// Children implements [gunim.Composite]: the sheets come and go as the
// pane opens and closes.
func (p *goPane) Children() []gunim.Node { return nil }

const goPaneBar = 40

// Layout implements [gunim.Node]: each sheet in its cell, gliding there;
// a sheet leaving stays where it was.
func (p *goPane) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	if c.Max != p.size {
		p.size = c.Max
		p.arrange()
	}
	if kids.Len() == 0 {
		return geom.Size{}
	}
	for k := range kids.All {
		sh, ok := k.Node().(*goSheet)
		if !ok {
			continue
		}
		r := sh.rect()
		k.Layout(gunim.Tight(r.Size()))
		k.Place(r.Min)
	}
	return p.size
}

// under is how solid the card under the sheets is: while one plugin's
// sheet gives way to another's, twice as solid as the one more in, so
// what is under the pane does not show through between them; and
// none while one sheet comes or goes alone.
func (p *goPane) under() float32 {
	shown, most := 0, float32(0)
	for _, sh := range p.sheets {
		if v := min(max(sh.in.Value(), 0), 1); v > 0.005 {
			shown++
			most = max(most, v)
		}
	}
	if shown < 2 {
		return 0
	}
	return min(2*most, 1)
}

// Paint implements [gunim.Node]: the card under the sheets, as solid as
// it is, the window's own colour between sheets side by side, and the
// sheets.
func (p *goPane) Paint(pt *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	if under := p.under(); under > 0 {
		pal := colours(f.Theme)
		r := geom.Rect{Max: box.Point()}
		several := min(max(p.several.Value(), 0), 1)
		end := pt.Layer(paint.LayerOpts{Bounds: r, Opacity: under})
		pt.RRect(r, 10, paint.Solid(mix(pal.raised, pal.night, several)))
		if several < 0.99 {
			pal.outline(pt, r, 10, 1-several)
		}
		end()
	}
	for k := range kids.All {
		k.Paint(pt)
	}
}

// Handle implements [gunim.Handler]: the pane takes the pointer while
// open, so nothing under it does, and Escape closes it.
func (p *goPane) Handle(e input.Event, u *gunim.UI) bool {
	if !p.isOpen() {
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

// goSheet is the sheet of a plugin's views, in the pane: its title,
// the browser's button, steps to the presets beside the one playing and
// a close button along the top; the browser of presets at the left
// while it shows, with a field that keeps the plugin's settings as a
// patch under it; and the plugin's views in the rest.
type goSheet struct {
	anim.Group
	p      *goPane
	v      *goView
	title  *widget.Label
	closer *widget.IconButton
	// browse shows the browser, and prev and next step through it.
	browse, prev, next *widget.IconButton
	// browser lists the presets of src: the patches the user kept, and
	// those the plugin comes with. keep names the settings to keep as
	// a patch, and drop deletes the patch kept that plays.
	src     *goPresets
	browser *plugui.PresetBrowser
	keep    *widget.TextField
	drop    *widget.IconButton
	// browsing says the browser shows, and shown how far it has come
	// in, from 0 to 1. followed is the preset playing as the browser
	// last followed it, playing whether one played, and kept how many
	// patches of the plugin's the user kept, as last listed.
	browsing bool
	shown    *anim.Float
	followed PresetRef
	playing  bool
	kept     int
	// in is how far the sheet has come in, from 0 to 1, and leaving
	// says it goes; at is where in the pane it stands, gliding, and
	// placed says it has stood anywhere yet.
	in      *anim.Float
	leaving bool
	at      [4]*anim.Float
	placed  bool
	// scale is how far the view is drawn down from the size it is laid
	// out at, to fit a cell smaller than viewLeast.
	scale float32
	// field renames the plugin, over its title, while renaming says so.
	field    *sheetField
	renaming bool
}

func newGoSheet(p *goPane, v *goView) *goSheet {
	sh := &goSheet{p: p, v: v, title: widget.NewLabel(""), in: anim.NewFloat(0), browsing: p.r.state.Browse}
	for i := range sh.at {
		sh.at[i] = anim.NewFloat(0)
		sh.Add(sh.at[i])
	}
	sh.title.NoWrap, sh.title.MaxLines = true, 1
	sh.closer = widget.NewIconButton(icon.X, "Close")
	sh.closer.OnClick = func(u *gunim.UI) gunim.Intent {
		p.leave(v, u)
		u.Invalidate()
		return nil
	}
	sh.field = &sheetField{TextField: widget.NewTextField(), sh: sh}
	sh.field.OnCommit = func(text string, _ *gunim.UI) gunim.Intent {
		sh.renaming = false
		return sh.named(text)
	}
	sh.field.Keys = func(k input.KeyPress, u *gunim.UI) bool {
		if k.Key != input.KeyEscape {
			return false
		}
		sh.renaming = false
		u.Focus(p.r)
		u.Invalidate()
		return true
	}
	sh.src = &goPresets{sh: sh}
	sh.browser = plugui.NewPresetBrowser(sh.src)
	sh.browser.BankIcon = bankIcon
	sh.browse = widget.NewIconButton(icon.LibraryBig, "Presets")
	sh.browse.OnClick = func(u *gunim.UI) gunim.Intent { sh.toggleBrowse(u); return nil }
	sh.prev = widget.NewIconButton(icon.ChevronLeft, "The preset before")
	sh.prev.OnClick = func(u *gunim.UI) gunim.Intent { sh.browser.Skip(-1, u); return nil }
	sh.next = widget.NewIconButton(icon.ChevronRight, "The next preset")
	sh.next.OnClick = func(u *gunim.UI) gunim.Intent { sh.browser.Skip(1, u); return nil }
	sh.keep = widget.NewTextField()
	sh.keep.Icon = icon.Save
	sh.keep.Placeholder = "Keep these settings as…"
	sh.keep.OnCommit = func(text string, u *gunim.UI) gunim.Intent {
		sh.keep.SetText("", u)
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return SavePatch{Track: v.track, Slot: v.slot, Name: text}
	}
	sh.drop = widget.NewIconButton(icon.Trash2, "Delete this patch")
	sh.drop.OnClick = func(*gunim.UI) gunim.Intent {
		if !sh.playing || sh.followed.Bank != vst3.UserBank {
			return nil
		}
		return DeletePatch{Class: v.d.ID, Name: sh.followed.Name}
	}
	sh.drop.Disabled = true
	sh.shown = anim.NewFloat(onOff(sh.browsing))
	sh.Add(sh.in, sh.shown)
	return sh
}

// follow has the browser follow slot s as the application has it: the
// patches kept, and the one playing.
func (sh *goSheet) follow(s Slot, u *gunim.UI) {
	kept := 0
	for _, p := range sh.p.r.state.Patches {
		if p.Class == sh.v.d.ID {
			kept++
		}
	}
	if kept != sh.kept {
		sh.kept = kept
		sh.browser.Refresh(u)
	}
	if s.Playing != sh.playing || s.Preset != sh.followed {
		sh.playing, sh.followed = s.Playing, s.Preset
		sh.browser.Follow(u)
	}
	sh.drop.Disabled = !sh.playing || sh.followed.Bank != vst3.UserBank
}

// toggleBrowse shows the browser, or hides it; the settings keep which,
// for the plugins' views to open so.
func (sh *goSheet) toggleBrowse(u *gunim.UI) {
	sh.browsing = !sh.browsing
	if sh.browsing {
		sh.shown.Animate(1, browseIn)
		sh.browser.Refresh(u)
		sh.browser.Follow(u)
	} else {
		sh.shown.Animate(0, anim.Tween{Duration: browseOut, Ease: anim.EaseInOut})
	}
	u.Send(sh, BrowsePresets{On: sh.browsing})
	u.Invalidate()
}

// goPresets are the presets a Go plugin's browser lists: the patches
// the user kept, then those the plugin comes with.
type goPresets struct{ sh *goSheet }

// Presets implements [plugui.PresetSource].
func (s *goPresets) Presets() []plug.Preset {
	v := s.sh.v
	var out []plug.Preset
	for _, p := range v.r.state.Patches {
		if p.Class == v.d.ID {
			out = append(out, plug.Preset{Bank: vst3.UserBank, Group: patchGroup(p.Class, ""), Name: p.Name})
		}
	}
	for _, p := range v.d.Patches {
		out = append(out, plug.Preset{Bank: vst3.FactoryBank, Group: patchGroup(v.d.ID, p.Category), Name: p.Name,
			About: strings.Join(p.Tags, " ")})
	}
	return out
}

// Current implements [plugui.PresetSource]: the patch loaded last, while
// the plugin's settings are as loaded.
func (s *goPresets) Current() (plug.Preset, bool) {
	sh := s.sh
	if !sh.playing {
		return plug.Preset{}, false
	}
	return plug.Preset{Bank: sh.followed.Bank, Group: sh.followed.Group, Name: sh.followed.Name}, true
}

// Pick implements [plugui.PresetSource]: the plugin takes the patch, as
// one step to undo. The patch playing, picked again, stays.
func (s *goPresets) Pick(p plug.Preset, u *gunim.UI) {
	if cur, ok := s.Current(); ok && cur.Same(p) {
		return
	}
	v := s.sh.v
	u.Send(s.sh, LoadPatch{Track: v.track, Slot: v.slot, Name: p.Name, Own: p.Bank == vst3.FactoryBank})
}

// viewLeast is the least size a plugin's view is laid out at: in a cell
// smaller, it is drawn smaller.
var viewLeast = geom.Sz(720, 440)

// sheetField is the field a sheet's plugin is renamed in: it renames as
// Enter is pressed or it loses the keyboard, and Escape lets it go
// unchanged.
type sheetField struct {
	*widget.TextField
	sh *goSheet
}

// Handle implements [gunim.Handler].
func (f *sheetField) Handle(e input.Event, u *gunim.UI) bool {
	if _, ok := e.(input.FocusLost); ok && f.sh.renaming {
		f.sh.renaming = false
		if in := f.sh.named(f.Text()); in != nil {
			u.Send(f.sh, in)
		}
		u.Invalidate()
	}
	return f.TextField.Handle(e, u)
}

// rename starts renaming the sheet's plugin, its title in the field.
func (sh *goSheet) rename(u *gunim.UI) {
	sh.renaming = true
	sh.field.Disabled = false
	sh.field.Placeholder = sh.v.d.Name
	sh.field.SetText(sh.title.Text, u)
	sh.field.Select(0, len([]rune(sh.title.Text)))
	u.Focus(sh.field)
	u.Invalidate()
}

// named is the renaming of the sheet's plugin to text, or nil for none.
func (sh *goSheet) named(text string) gunim.Intent {
	text = strings.TrimSpace(text)
	if text == sh.title.Text {
		return nil
	}
	if text == "" {
		text = sh.v.d.Name
	}
	return RenamePlugin{Track: sh.v.track, Slot: sh.v.slot, Label: text}
}

// titleAt is where the sheet's title stands, for the double-click that
// renames it.
func (sh *goSheet) titleAt() geom.Rect { return geom.Rc(10, 4, 300, goPaneBar-8) }

// moveTo has the sheet glide to r; one not yet placed, or not in, is
// there at once.
func (sh *goSheet) moveTo(r geom.Rect) {
	to := [4]float32{r.Min.X, r.Min.Y, r.Size().W, r.Size().H}
	jump := !sh.placed || sh.in.Value() < 0.01
	sh.placed = true
	for i, a := range sh.at {
		if jump {
			a.Jump(to[i])
		} else if a.Target() != to[i] {
			a.Animate(to[i], anim.Spring{Response: 0.32, Damping: 0.9})
		}
	}
}

// rect is where the sheet stands now.
func (sh *goSheet) rect() geom.Rect {
	return geom.Rc(sh.at[0].Value(), sh.at[1].Value(), max(sh.at[2].Value(), 0), max(sh.at[3].Value(), 0))
}

// Children implements [gunim.Composite].
func (sh *goSheet) Children() []gunim.Node {
	sh.field.Disabled = !sh.renaming
	return []gunim.Node{sh.title, sh.closer, sh.v.paneView(), sh.field, sh.browse, sh.prev, sh.next,
		sh.browser, sh.keep, sh.drop}
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

// sheetSide is the most the browser takes of a sheet's width, and
// sheetSideShare the most of its share.
const (
	sheetSide      = 300
	sheetSideShare = 0.42
)

// side is how wide the browser stands in a sheet as wide as w: while it
// shows, or comes or goes.
func (sh *goSheet) side(w float32) float32 {
	if !sh.browsing && sh.shown.Value() <= 0 {
		return 0
	}
	return min(sheetSide, w*sheetSideShare)
}

// Layout implements [gunim.Node]: the title and the buttons along the
// top; under them, the browser at the left while it shows, and the
// view in the rest.
func (sh *goSheet) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	side := sh.side(size.W)
	sh.browse.Active = sh.browsing
	x := size.W - 40
	for k := range kids.All {
		switch k.Node() {
		case sh.closer:
			k.Layout(gunim.Tight(geom.Sz(32, 32)))
			k.Place(geom.Pt(x, 4))
		case sh.next, sh.prev, sh.browse:
			// Right to left before the close button.
			at := float32(3)
			switch k.Node() {
			case sh.prev:
				at = 2
			case sh.next:
				at = 1
			}
			k.Layout(gunim.Tight(geom.Sz(barButton, 32)))
			k.Place(geom.Pt(x-at*barButton, 4))
		case sh.title:
			k.Layout(gunim.Constraints{Max: geom.Sz(max(size.W-3*barButton-72, 0), 24)})
			k.Place(geom.Pt(16, 10))
		case sh.field:
			k.Layout(gunim.Tight(geom.Sz(min(260, max(size.W-3*barButton-72, 0)), 30)))
			k.Place(geom.Pt(10, 5))
		case sh.browser:
			bh := max(size.H-goPaneBar-fieldH-2*sideInset, 0)
			if side == 0 {
				bh = 0
			}
			k.Layout(gunim.Tight(geom.Sz(max(side-sideInset, 0), bh)))
			k.Place(geom.Pt(sideInset, goPaneBar))
		case sh.keep:
			w := max(side-sideInset-barButton-4, 0)
			if side == 0 {
				w = 0
			}
			k.Layout(gunim.Tight(geom.Sz(w, fieldH)))
			k.Place(geom.Pt(sideInset, size.H-sideInset-fieldH))
		case sh.drop:
			w := float32(barButton)
			if side == 0 {
				w = 0
			}
			k.Layout(gunim.Tight(geom.Sz(w, fieldH)))
			k.Place(geom.Pt(side-barButton, size.H-sideInset-fieldH))
		default:
			// The view, laid out no smaller than viewLeast, and drawn
			// down to fit where its room is smaller, the pointer
			// following.
			room := geom.Sz(max(size.W-side-32, 0), max(size.H-goPaneBar-12, 0))
			sh.scale = min(1, room.W/viewLeast.W, room.H/viewLeast.H)
			sh.scale = max(sh.scale, 0.1)
			k.Layout(gunim.Tight(geom.Sz(room.W/sh.scale, room.H/sh.scale)))
			k.Place(geom.Pt((side+16)/sh.scale, goPaneBar/sh.scale))
		}
	}
	return size
}

// Paint implements [gunim.Node]: faded and lowered as far as the sheet
// is out; the browser faded and slid as far as it is in.
func (sh *goSheet) Paint(pt *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	in := min(max(sh.in.Value(), 0), 1)
	if in < 0.005 {
		return
	}
	pal := colours(f.Theme)
	r := geom.Rect{Max: box.Point()}
	defer pt.Push(paint.Translate(geom.Pt(0, (1-in)*18)))()
	// Clipped to the sheet, so a view in a small cell keeps to it.
	end := pt.Layer(paint.LayerOpts{Bounds: r, Opacity: in, Clip: true, Radius: 10})
	defer end()
	pt.RRect(r, 10, paint.Solid(pal.raised))
	pal.outline(pt, r, 10, 1)
	side := sh.side(box.W)
	for k := range kids.All {
		switch k.Node() {
		case sh.title:
			// While renaming, the field in the title's place.
			if sh.renaming {
				continue
			}
		case sh.field:
			if !sh.renaming {
				continue
			}
		case sh.browser, sh.keep, sh.drop:
			continue
		case sh.v.paneView():
			if sh.scale < 1 {
				// The view, drawn down to its room.
				pop := pt.Push(paint.Scale(sh.scale, geom.Point{}))
				k.Paint(pt)
				pop()
				continue
			}
		}
		k.Paint(pt)
	}
	shown := min(max(sh.shown.Value(), 0), 1)
	if side <= 0 || shown <= 0 {
		return
	}
	area := geom.Rc(0, goPaneBar-sideInset, side, box.H-goPaneBar+sideInset)
	defer pt.Layer(paint.LayerOpts{Bounds: area, Opacity: shown, Clip: true})()
	defer pt.Push(paint.Translate(geom.Pt(-(1-shown)*browseGlide, 0)))()
	for k := range kids.All {
		if n := k.Node(); n == sh.browser || n == sh.keep || n == sh.drop {
			k.Paint(pt)
		}
	}
}

// Handle implements [gunim.Handler]: a double-click on the title
// renames the plugin; a sheet leaving takes nothing.
func (sh *goSheet) Handle(e input.Event, u *gunim.UI) bool {
	d, ok := e.(input.PointerDown)
	if !ok || sh.leaving || d.Button != input.ButtonPrimary || d.Clicks != 2 || !sh.titleAt().Contains(d.Pos) {
		return false
	}
	sh.rename(u)
	return true
}

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
