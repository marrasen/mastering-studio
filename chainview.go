package main

import (
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// chainRow is the track's chain: its plugins as cards, in the order the
// sound runs through them, joined by the sound itself, flowing while it
// plays. A card's light switches the plugin on and off, a click opens
// its editor, and its menu renames, moves and removes it. Behind the
// sound's line, a band as tall as the loudness range shows how each
// plugin widens or narrows it, as last measured. After the cards, a
// button adds a plugin, found by typing its name; at the end, a menu
// copies the chain to other tracks, and keeps it as a preset, loads
// one, or deletes one.
type chainRow struct {
	anim.Group
	r    *root
	menu *widget.ContextMenu
	add  *pill
	// more is the chain's menu: copy, and the presets.
	more  *pill
	cards map[int]*chainCard
	order []int
	// track is the track shown, slots its chain, and preset the preset
	// it was loaded from or saved as.
	track  int
	slots  []Slot
	preset string
	// flow is how far the sound has run along the joins, in pixels.
	flow float32
	// inLRA is the loudness range fed into the chain, as drawn, where
	// inRanged says it has one; stale draws the ranges faint, the track
	// changed since they were measured.
	inLRA    *anim.Float
	inRanged bool
	stale    bool
	hover    int
	press    int
	picker   widget.Palette
	choices  []PluginChoice
	// addX is where the add button is.
	addX float32
	// field names a slot, renaming, over its card.
	field    *slotField
	renaming int
	size     geom.Size
}

// slotField is the field a slot is named in: it names it as Enter is
// pressed or it loses the keyboard, and Escape lets it go unchanged.
type slotField struct {
	*widget.TextField
	c *chainRow
}

// Handle implements [gunim.Handler].
func (f *slotField) Handle(e input.Event, u *gunim.UI) bool {
	if _, ok := e.(input.FocusLost); ok && f.c.renaming != 0 {
		f.c.finish(f.Text(), u)
	}
	return f.TextField.Handle(e, u)
}

// chainCard is a plugin's card, its place and look animated.
type chainCard struct {
	slot Slot
	// lra is the loudness range out of the plugin, as drawn.
	lra         *anim.Float
	x, w        *anim.Float
	appear, on  *anim.Float
	hover, open *anim.Float
	gone        bool
	placed      bool
}

const (
	chainLabelW = 70
	cardH       = 44
	cardGap     = 26
)

func newChainRow(r *root) *chainRow {
	c := &chainRow{r: r, cards: map[int]*chainCard{}, hover: -1, press: -1, inLRA: anim.NewFloat(0)}
	c.Add(c.inLRA)
	c.add = newPill("+ Plugin", c.openPicker)
	c.more = newPill("Chain", c.openMore)
	c.more.menu = true
	c.Add(c.add, c.more)
	c.field = &slotField{TextField: widget.NewTextField(), c: c}
	c.field.OnCommit = func(text string, u *gunim.UI) gunim.Intent {
		in := c.named(text)
		c.renaming = 0
		return in
	}
	c.field.Keys = func(k input.KeyPress, u *gunim.UI) bool {
		if k.Key != input.KeyEscape {
			return false
		}
		c.renaming = 0
		u.Focus(r)
		u.Invalidate()
		return true
	}
	return c
}

// rename starts naming slot id, its name in the field over its card.
func (c *chainRow) rename(id int, u *gunim.UI) {
	k := c.cards[id]
	if k == nil {
		return
	}
	c.renaming = id
	c.field.Disabled = false
	c.field.Placeholder = k.slot.Name
	title := k.slot.title()
	c.field.SetText(title, u)
	c.field.Select(0, len([]rune(title)))
	u.Focus(c.field)
	u.Invalidate()
}

// named is the naming of the slot renamed to text, or nil for none.
func (c *chainRow) named(text string) gunim.Intent {
	k := c.cards[c.renaming]
	if k == nil || text == k.slot.title() {
		return nil
	}
	return RenamePlugin{Track: c.track, Slot: c.renaming, Label: text}
}

// finish ends the naming, naming the slot text.
func (c *chainRow) finish(text string, u *gunim.UI) {
	if in := c.named(text); in != nil {
		u.Send(c, in)
	}
	c.renaming = 0
	u.Invalidate()
}

func (c *chainRow) card(s Slot) *chainCard {
	k := c.cards[s.ID]
	if k == nil {
		k = &chainCard{x: anim.NewFloat(0), w: anim.NewFloat(0), appear: anim.NewFloat(0), on: anim.NewFloat(1),
			hover: anim.NewFloat(0), open: anim.NewFloat(0), lra: anim.NewFloat(float32(s.LRA))}
		c.Add(k.x, k.w, k.appear, k.on, k.hover, k.open, k.lra)
		c.cards[s.ID] = k
	}
	return k
}

func onOff(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

func (c *chainRow) show(t Track, s Album) {
	switched := t.ID != c.track
	if switched {
		c.renaming = 0
	}
	c.track, c.slots, c.preset = t.ID, t.Chain, t.Preset
	keep := map[int]bool{}
	for _, sl := range t.Chain {
		keep[sl.ID] = true
	}
	for id, k := range c.cards {
		switch {
		case keep[id]:
		case switched:
			// Another track's chain: its cards go at once.
			c.Remove(k.x, k.w, k.appear, k.on, k.hover, k.open, k.lra)
			delete(c.cards, id)
		case !k.gone:
			k.gone = true
			k.appear.Animate(0, anim.Spring{Response: 0.25, Damping: 1})
		}
	}
	c.order = c.order[:0]
	for i, sl := range t.Chain {
		k := c.card(sl)
		k.slot, k.gone = sl, false
		k.appear.Animate(1, anim.Spring{Response: 0.35 + 0.04*float32(i)*onOff(switched), Damping: 0.8})
		k.on.Animate(onOff(!sl.Bypass), anim.Snappy)
		k.open.Animate(onOff(sl.Open), anim.Gentle)
		k.lra.Animate(sl.LRA*onOff(sl.Ranged), anim.Spring{Response: 0.5, Damping: 0.9})
		c.order = append(c.order, sl.ID)
	}
	c.inRanged, c.stale = t.Measured && t.Measure.InRanged, t.Stale
	c.inLRA.Animate(t.Measure.InLRA*onOff(c.inRanged), anim.Spring{Response: 0.5, Damping: 0.9})
	c.choices = s.Plugins
	c.add.setLit(false)
}

// cardWidth is how wide the card of slot s is.
func cardWidth(s Slot) float32 {
	w := max(shaped(s.title(), 12, true).Advance, shaped(cardDetail(s), 9, false).Advance)
	return min(max(w+52, 120), 230)
}

// cardDetail is what a card says under its name: how much louder the
// plugin makes the track, as measured, who makes it, and how late its
// sound comes.
func cardDetail(s Slot) string {
	if s.Failed != "" {
		return "would not load"
	}
	// Named apart, the plugin's own name in its maker's place.
	d := s.Vendor
	if s.Label != "" {
		d = s.Name
	}
	if s.Latency > 0 {
		d = fmt.Sprintf("%d smp · %s", s.Latency, d)
	}
	if s.Ranged {
		d = fmt.Sprintf("LRA %.1f · %s", s.LRA, d)
	}
	if s.Gained {
		d = fmt.Sprintf("%+.1f LU · %s", s.Gain, d)
	}
	return d
}

// Children implements [gunim.Composite]: the field is there all
// along, and takes anything only while a slot is named.
func (c *chainRow) Children() []gunim.Node {
	c.field.Disabled = c.renaming == 0
	return []gunim.Node{c.add, c.more, c.field}
}

// Layout implements [gunim.Node]: the cards in a row after the label,
// sliding to their places, the add button after them, and the copy
// button at the end.
func (c *chainRow) Layout(cs gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	c.size = cs.Max
	// The cards narrow to leave room for the buttons.
	room := c.size.W - chainLabelW - pillWidth("+ Plugin") - menuPillWidth(c.more.words) - 16
	var want float32
	for _, id := range c.order {
		want += cardWidth(c.cards[id].slot) + cardGap
	}
	fit := min(1, (room-float32(len(c.order))*cardGap)/max(want-float32(len(c.order))*cardGap, 1))
	x := float32(chainLabelW)
	for _, id := range c.order {
		k := c.cards[id]
		w := max(48, cardWidth(k.slot)*fit)
		move := anim.Spring{Response: 0.3, Damping: 0.85}
		if !k.placed {
			k.x.Jump(x)
			k.w.Jump(w)
			k.placed = true
		}
		k.x.Animate(x, move)
		k.w.Animate(w, move)
		x += w + cardGap
	}
	mid := c.size.H / 2
	aw := pillWidth("+ Plugin")
	kids.At(0).Layout(gunim.Tight(geom.Sz(aw, 32)))
	kids.At(0).Place(geom.Pt(x, mid-16))
	c.addX = x
	cw := menuPillWidth(c.more.words)
	kids.At(1).Layout(gunim.Tight(geom.Sz(cw, 32)))
	kids.At(1).Place(geom.Pt(c.size.W-cw, mid-16))
	// The field over the card named.
	fr := geom.Rc(0, mid-16, 160, 32)
	if k := c.cards[c.renaming]; k != nil {
		fr = geom.Rc(k.x.Target(), mid-16, max(k.w.Target(), 160), 32)
	}
	kids.At(2).Layout(gunim.Tight(fr.Size()))
	kids.At(2).Place(fr.Min)
	return c.size
}

// cardRect is where card k is.
func (c *chainRow) cardRect(k *chainCard) geom.Rect {
	return geom.Rc(k.x.Value(), (c.size.H-cardH)/2, k.w.Value(), cardH)
}

// powerAt is the centre of card k's light.
func (c *chainRow) powerAt(k *chainCard) geom.Point {
	r := c.cardRect(k)
	return geom.Pt(r.Min.X+18, r.Min.Y+cardH/2)
}

// at returns the card under p, and whether p is on its light.
func (c *chainRow) at(p geom.Point) (id int, light bool) {
	for _, sid := range c.order {
		k := c.cards[sid]
		if c.cardRect(k).Contains(p) {
			d := p.Sub(c.powerAt(k))
			return sid, d.X*d.X+d.Y*d.Y <= 12*12
		}
	}
	return -1, false
}

// Step implements [gunim.Stepper]: the cards' springs, and the sound
// flowing while it plays.
func (c *chainRow) Step(dt time.Duration) bool {
	moving := c.Group.Step(dt)
	for id, k := range c.cards {
		if k.gone && k.appear.Value() < 0.01 && !k.appear.Active() {
			c.Remove(k.x, k.w, k.appear, k.on, k.hover, k.open, k.lra)
			delete(c.cards, id)
		}
	}
	if c.r.state.Playing && len(c.order) > 0 {
		c.flow += float32(dt.Seconds()) * 40
		return true
	}
	return moving
}

// Focusable implements [gunim.Focusable].
func (c *chainRow) Focusable() bool { return false }

// Handle implements [gunim.Handler].
func (c *chainRow) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		id, _ := c.at(e.Pos)
		c.setHover(id)
	case input.PointerLeave:
		c.setHover(-1)
	case input.PointerDown:
		if e.Button == input.ButtonSecondary {
			return c.openMenu(e.Pos, u)
		}
		if e.Button != input.ButtonPrimary {
			return false
		}
		id, light := c.at(e.Pos)
		k := c.cards[id]
		if k == nil {
			return false
		}
		u.Cue(gunim.CueTick, c)
		if light {
			on := k.slot.Bypass
			k.on.Animate(onOff(on), anim.Snappy)
			u.Send(c, SetBypass{Track: c.track, Slot: id, On: !on})
			break
		}
		k.open.Animate(1, anim.Gentle)
		u.Send(c, ShowEditor{Track: c.track, Slot: id})
	default:
		return false
	}
	u.Invalidate()
	return true
}

func (c *chainRow) setHover(id int) {
	if id == c.hover {
		return
	}
	if k := c.cards[c.hover]; k != nil {
		k.hover.Animate(0, anim.Gentle)
	}
	if k := c.cards[id]; k != nil {
		k.hover.Animate(1, anim.Snappy)
	}
	c.hover = id
}

// openMenu opens the menu of the card at p.
func (c *chainRow) openMenu(p geom.Point, u *gunim.UI) bool {
	id, _ := c.at(p)
	k := c.cards[id]
	if k == nil || c.menu == nil {
		return false
	}
	i := slices.Index(c.order, id)
	run := "Bypass"
	if k.slot.Bypass {
		run = "Switch on"
	}
	c.menu.SetItems([]widget.MenuItem{
		{Label: "Open editor", Icon: icon.SlidersHorizontal, Disabled: k.slot.Failed != ""},
		{Label: run, Icon: icon.Power},
		{Label: "Rename…", Icon: icon.Pencil},
		{Label: "Move earlier", Icon: icon.ArrowLeft, Disabled: i == 0, Break: true},
		{Label: "Move later", Icon: icon.ArrowRight, Disabled: i == len(c.order)-1},
		{Label: "Remove from the chain", Icon: icon.Trash2, Break: true},
	})
	c.menu.OnPick = func(item int, u *gunim.UI) gunim.Intent {
		switch item {
		case 0:
			return ShowEditor{Track: c.track, Slot: id}
		case 1:
			return SetBypass{Track: c.track, Slot: id, On: !k.slot.Bypass}
		case 2:
			c.rename(id, u)
		case 3:
			return MovePlugin{Track: c.track, From: i, To: i - 1}
		case 4:
			return MovePlugin{Track: c.track, From: i, To: i + 1}
		case 5:
			return RemovePlugin{Track: c.track, Slot: id}
		}
		return nil
	}
	c.menu.Open(p, u)
	return true
}

// openPicker opens the palette of the plugins this computer has.
func (c *chainRow) openPicker(u *gunim.UI) {
	if c.track == 0 {
		return
	}
	// The plugins added last first, the latest at the top, as found on
	// this computer, then the rest.
	choices := make([]PluginChoice, 0, len(c.choices))
	recent := map[[2]string]bool{}
	for _, r := range c.r.state.Recent {
		for _, p := range c.choices {
			if p.Path == r.Path && p.Class == r.Class && !recent[[2]string{p.Path, p.Class}] {
				choices = append(choices, p)
				recent[[2]string{p.Path, p.Class}] = true
			}
		}
	}
	for _, p := range c.choices {
		if !recent[[2]string{p.Path, p.Class}] {
			choices = append(choices, p)
		}
	}
	items := make([]widget.PaletteItem, 0, len(choices))
	for _, p := range choices {
		it := widget.PaletteItem{Title: p.Name, Detail: p.Vendor, Also: []string{p.Vendor, p.Kind},
			Icon: icon.AudioLines, Key: widget.Key(p.Path + "|" + p.Class)}
		if recent[[2]string{p.Path, p.Class}] {
			it.Hint = "recent"
		}
		items = append(items, it)
	}
	c.picker.Items = items
	c.picker.Placeholder = "Find a plugin"
	c.picker.Status = ""
	switch {
	case c.r.state.Scanning:
		c.picker.Status = "Looking for plugins…"
	case len(items) == 0:
		c.picker.Status = "No VST3 plugins found in the system's plugin folders"
	}
	track := c.track
	c.picker.OnPick = func(i int, u *gunim.UI) gunim.Intent {
		if i >= 0 && i < len(choices) {
			return AddPlugin{Track: track, Choice: choices[i]}
		}
		return nil
	}
	c.add.setLit(true)
	c.picker.Open(c, geom.Rect{Max: c.size.Point()}, u)
}

// openCopy opens the menu of where to copy the chain to.
func (c *chainRow) openCopy(u *gunim.UI) {
	if c.menu == nil || c.track == 0 {
		return
	}
	var ids []int
	items := []widget.MenuItem{{Label: "Every other track"}}
	for i, t := range c.r.state.Tracks {
		if t.ID != c.track {
			ids = append(ids, t.ID)
			items = append(items, widget.MenuItem{Label: fmt.Sprintf("%02d %s", i+1, t.Title)})
		}
	}
	items[0].Disabled = len(ids) == 0
	if len(items) > 1 {
		items[1].Break = true
	}
	c.menu.SetItems(items)
	from := c.track
	c.menu.OnPick = func(item int, u *gunim.UI) gunim.Intent {
		if item == 0 {
			return CopyChain{From: from}
		}
		return CopyChain{From: from, To: []int{ids[item-1]}}
	}
	c.menu.Open(c.moreAt(), u)
}

// moreAt is where the chain's menus open: under its button.
func (c *chainRow) moreAt() geom.Point {
	return geom.Pt(c.size.W-menuPillWidth(c.more.words), c.size.H/2+16)
}

// openMore opens the chain's menu: copy it to other tracks, keep it as
// its preset or a new one, load a preset, or delete one.
func (c *chainRow) openMore(u *gunim.UI) {
	if c.menu == nil || c.track == 0 {
		return
	}
	presets := c.r.state.Presets
	save := "Save preset"
	has := c.preset != "" && slices.Contains(presets, c.preset)
	if has {
		save = "Save preset “" + c.preset + "”"
	}
	empty := len(c.slots) == 0
	c.menu.SetItems([]widget.MenuItem{
		{Label: "Copy to…", Icon: icon.Copy},
		{Label: save, Icon: icon.Save, Disabled: empty || !has, Break: true},
		{Label: "Save preset as…", Icon: icon.Save, Disabled: empty},
		{Label: "Load preset…", Icon: icon.FolderOpen, Disabled: len(presets) == 0, Break: true},
		{Label: "Delete preset…", Icon: icon.Trash2, Disabled: len(presets) == 0},
	})
	track := c.track
	c.menu.OnPick = func(item int, u *gunim.UI) gunim.Intent {
		switch item {
		case 0:
			c.openCopy(u)
		case 1:
			return SavePreset{Track: track}
		case 2:
			return NamePreset{Track: track}
		case 3:
			c.openPresets(u, false)
		case 4:
			c.openPresets(u, true)
		}
		return nil
	}
	c.menu.Open(c.moreAt(), u)
}

// openPresets opens the menu of the presets kept, to load one, or,
// del, to delete one.
func (c *chainRow) openPresets(u *gunim.UI, del bool) {
	presets := slices.Clone(c.r.state.Presets)
	items := make([]widget.MenuItem, len(presets))
	for i, p := range presets {
		items[i] = widget.MenuItem{Label: p, Icon: icon.AudioLines, Checked: !del && p == c.preset}
		if del {
			items[i].Icon = icon.Trash2
		}
	}
	c.menu.SetItems(items)
	track := c.track
	c.menu.OnPick = func(item int, u *gunim.UI) gunim.Intent {
		if item < 0 || item >= len(presets) {
			return nil
		}
		if del {
			return DeletePreset{Name: presets[item]}
		}
		return LoadPreset{Track: track, Name: presets[item]}
	}
	c.menu.Open(c.moreAt(), u)
}

// Paint implements [gunim.Node]: the label, the sound's line through the
// cards, and the cards.
func (c *chainRow) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	pal := colours(f.Theme)
	mid := box.H / 2
	shaped("CHAIN", 9, true).Paint(p, geom.Pt(4, mid-12), faded(pal.teal, 0.85))
	what := "no plugins"
	if n := len(c.order); n > 0 {
		what = fmt.Sprintf("%d plugin%s", n, map[bool]string{true: "", false: "s"}[n == 1])
	}
	shaped(what, 9, false).Paint(p, geom.Pt(4, mid+2), pal.quiet(0.45))
	// The line the sound runs along, from the label to the add button,
	// dotted with the sound flowing while it plays.
	addX := c.addX
	if addX > chainLabelW {
		c.paintRanges(p, pal, mid, addX-4)
		line := faded(pal.ink, 0.12)
		audioui.Segment(p, geom.Pt(chainLabelW-10, mid), geom.Pt(addX-4, mid), 1.5, line)
		if c.r.state.Playing {
			for x := chainLabelW - 10 + float32(math.Mod(float64(c.flow), 18)); x < addX-4; x += 18 {
				p.RRect(geom.Rc(x-1.5, mid-1.5, 3, 3), 1.5, paint.Solid(faded(pal.teal, 0.7)))
			}
		}
	}
	for _, id := range c.drawOrder() {
		c.paintCard(p, pal, c.cards[id])
	}
	kids.At(0).Paint(p)
	kids.At(1).Paint(p)
	if c.renaming != 0 {
		kids.At(2).Paint(p)
	}
}

// lraScale is how tall the band of the loudness range is, in pixels a
// unit.
const lraScale = 2.4

// paintRanges draws, behind the sound's line, a band as tall as the
// loudness range: of the sound fed in, from the label to the first
// card, then of the sound out of each plugin, to the next, and the last
// to end.
func (c *chainRow) paintRanges(p *paint.Painter, pal palette, mid, end float32) {
	alpha := float32(1)
	if c.stale {
		alpha = 0.45
	}
	band := func(x0, x1, lra float32) {
		h := min(max(lra*lraScale, 0), cardH+12)
		if x1 <= x0 || h < 1 {
			return
		}
		r := geom.Rc(x0, mid-h/2, x1-x0, h)
		p.RRect(r, min(h/2, 6), paint.Solid(faded(pal.sky, 0.13*alpha)))
		p.RRect(geom.Rc(x0, r.Min.Y, x1-x0, 1), 0, paint.Solid(faded(pal.sky, 0.35*alpha)))
		p.RRect(geom.Rc(x0, r.Max.Y-1, x1-x0, 1), 0, paint.Solid(faded(pal.sky, 0.35*alpha)))
	}
	// Each stretch runs a little under the cards either side, so the
	// band reads as one.
	x, lra := float32(chainLabelW-10), c.inLRA.Value()
	for _, id := range c.order {
		k := c.cards[id]
		r := c.cardRect(k)
		band(x, r.Min.X+8, lra)
		x, lra = r.Max.X-8, k.lra.Value()
	}
	band(x, end, lra)
}

// drawOrder is the cards, those leaving first, under the rest.
func (c *chainRow) drawOrder() []int {
	var out []int
	for id, k := range c.cards {
		if k.gone {
			out = append(out, id)
		}
	}
	return append(out, c.order...)
}

func (c *chainRow) paintCard(p *paint.Painter, pal palette, k *chainCard) {
	a := k.appear.Value()
	if a < 0.01 {
		return
	}
	r := c.cardRect(k)
	mid := r.Center()
	defer p.Push(paint.Scale(0.85+0.15*a, mid))()
	end := p.Layer(paint.LayerOpts{Bounds: r.Inset(geom.Insets{Top: -12, Bottom: -12, Left: -12, Right: -12}),
		Opacity: min(a, 1)})
	defer end()
	on, open, hover := k.on.Value(), k.open.Value(), k.hover.Value()
	failed := k.slot.Failed != ""
	edge := mix(pal.teal, pal.coral, onOff(failed))
	if open > 0.01 {
		p.ShadowRRect(r, 10, paint.Solid(pal.raised), paint.Shadow{Blur: 16 * open, Color: faded(edge, 0.35*open)})
	}
	p.RRect(r, 10, paint.Solid(mix(pal.raised, mix(pal.raised, pal.ink, 0.06), hover)))
	pal.outline(p, r, 10, 1)
	if open > 0.01 || failed {
		w := max(open, onOff(failed))
		p.RRect(geom.Rc(r.Min.X+10, r.Max.Y-2.5, r.Size().W-20, 2.5), 1.25, paint.Solid(faded(edge, w)))
	}
	// The light: lit while the plugin runs.
	at := c.powerAt(k)
	lit := mix(faded(pal.ink, 0.25), pal.teal, on)
	if on > 0.01 {
		p.ShadowRRect(geom.Rc(at.X-6, at.Y-6, 12, 12), 6, paint.Solid(faded(pal.teal, on)),
			paint.Shadow{Blur: 10 * on, Color: faded(pal.teal, 0.5*on)})
	}
	p.RRect(geom.Rc(at.X-7, at.Y-7, 14, 14), 7, paint.Solid(faded(lit, 0.25+0.2*(1-on))))
	p.RRect(geom.Rc(at.X-4, at.Y-4, 8, 8), 4, paint.Solid(mix(faded(pal.ink, 0.3), pal.night, on)))
	room := r.Size().W - 44
	words := mix(pal.quiet(0.45), pal.ink, on)
	paintFit(p, k.slot.title(), 12, true, geom.Pt(r.Min.X+34, r.Min.Y+8), room, words)
	detail := pal.quiet(0.45)
	if failed {
		detail = pal.coral
	}
	paintFit(p, cardDetail(k.slot), 9, false, geom.Pt(r.Min.X+34, r.Min.Y+26), room, detail)
}
