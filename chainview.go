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
// its editor, a drag moves it along the chain, the others making way,
// and its menu renames, moves and removes it. Behind the
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
	// down is the card pressed, -1 for none, at pressAt; moving is the
	// card dragged, 0 for none, held grab from its left edge, and moveTo
	// the place in the chain it would land.
	down    int
	pressAt geom.Point
	moving  int
	grab    float32
	moveTo  int
	picker  widget.Palette
	choices []PluginChoice
	// addX is where the add button is, and addY the middle of its row.
	addX, addY float32
	// rows is how many rows the cards take, one or two, as the width
	// has room for; tall is the row's height, gliding to theirs.
	rows int
	tall *anim.Float
	// chipIn are the Go plugins' chips among the row's children, and
	// their slots.
	chipIn map[gunim.Node]int
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
	x, w, y     *anim.Float
	appear, on  *anim.Float
	hover, open *anim.Float
	gone        bool
	placed      bool
}

const (
	chainLabelW = 70
	cardH       = 44
	cardGap     = 26
	// rowStep is how far the second row of cards stands under the
	// first, and chainH how tall the chain is with one.
	rowStep = cardH + 16
	chainH  = 60
)

func newChainRow(r *root) *chainRow {
	c := &chainRow{r: r, cards: map[int]*chainCard{}, hover: -1, press: -1, down: -1, inLRA: anim.NewFloat(0),
		chipIn: map[gunim.Node]int{}, rows: 1, tall: anim.NewFloat(chainH)}
	c.Add(c.inLRA, c.tall)
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
		k = &chainCard{x: anim.NewFloat(0), w: anim.NewFloat(0), y: anim.NewFloat(chainH / 2), appear: anim.NewFloat(0),
			on: anim.NewFloat(1), hover: anim.NewFloat(0), open: anim.NewFloat(0), lra: anim.NewFloat(float32(s.LRA))}
		c.Add(k.x, k.w, k.y, k.appear, k.on, k.hover, k.open, k.lra)
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
			c.Remove(k.x, k.w, k.y, k.appear, k.on, k.hover, k.open, k.lra)
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
		k.open.Animate(onOff(sl.Open || c.r.pane.v != nil && c.r.pane.v.slot == sl.ID), anim.Gentle)
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
	kids := make([]gunim.Node, 0, 3+len(c.chipIn))
	kids = append(kids, c.add, c.more, c.field)
	for n := range c.chipIn {
		kids = append(kids, n)
	}
	return kids
}

// chips are the slots of the cards with a Go plugin's chip in, in
// order.
func (c *chainRow) chips() []int {
	var out []int
	for _, id := range c.order {
		if v := c.r.goViews[id]; v != nil && v.chip != nil {
			out = append(out, id)
		}
	}
	return out
}

// showOpen shows slot id's card as open, or not, as the pane opens
// and closes its plugin's views.
func (c *chainRow) showOpen(id int, on bool) {
	if k := c.cards[id]; k != nil {
		k.open.Animate(onOff(on), anim.Gentle)
	}
}

// syncChips puts the chips of the cards shown among the row's
// children, and takes those of cards gone out.
func (c *chainRow) syncChips(u *gunim.UI) {
	want := map[gunim.Node]int{}
	for _, id := range c.chips() {
		want[c.r.goViews[id].chip] = id
	}
	for n := range c.chipIn {
		if _, ok := want[n]; !ok {
			u.Remove(n)
			delete(c.chipIn, n)
		}
	}
	for n, id := range want {
		if _, ok := c.chipIn[n]; !ok {
			u.Insert(c, n)
		}
		c.chipIn[n] = id
	}
}

// chipRect is where card k's chip is, under its name.
func (c *chainRow) chipRect(k *chainCard) geom.Rect {
	r := c.cardRect(k)
	return geom.Rc(r.Min.X+34, r.Min.Y+23, max(r.Size().W-44, 0), cardH-27)
}

// rowRoom is how much room each row has for its cards: the first
// beside the chain's menu, and the last before the add button, which
// in one row are the same row.
func (c *chainRow) rowRoom(w float32, rows int) (first, last float32) {
	add, more := pillWidth("+ Plugin"), menuPillWidth(c.more.words)
	if rows == 1 {
		r := w - chainLabelW - add - more - 16
		return r, r
	}
	return w - chainLabelW - more - 16, w - chainLabelW - add - 16
}

// wantRows sets how many rows the cards take in a row w wide: two where
// they would narrow to under fitLeast of their width in one, and the
// height they take glides there.
func (c *chainRow) wantRows(w float32) {
	var want float32
	for _, id := range c.order {
		if k := c.cards[id]; k != nil {
			want += cardWidth(k.slot)
		}
	}
	room, _ := c.rowRoom(w, 1)
	n := float32(len(c.order))
	rows := 1
	if len(c.order) > 1 && (room-n*cardGap)/max(want, 1) < fitLeast {
		rows = 2
	}
	c.rows = rows
	if to := chainH + float32(rows-1)*rowStep; c.tall.Target() != to {
		c.tall.Animate(to, anim.Spring{Response: 0.3, Damping: 0.9})
	}
}

// fitLeast is how far the cards narrow in one row before they take two.
const fitLeast = 0.8

// height is how tall the chain stands now.
func (c *chainRow) height() float32 { return c.tall.Value() }

// rowMid is the middle of row i of cards.
func rowMid(i int) float32 { return chainH/2 + float32(i)*rowStep }

// splitRows returns the cards placed, in the rows they take: the first
// row the first half, a card more where they are odd.
func (c *chainRow) splitRows(order []int) [][]int {
	if c.rows < 2 || len(order) < 2 {
		return [][]int{order}
	}
	half := (len(order) + 1) / 2
	return [][]int{order[:half], order[half:]}
}

// Layout implements [gunim.Node]: the cards in a row after the label,
// or two where the row has no room for them, sliding to their places;
// the add button after them, and the chain's menu at the end of the
// first row.
func (c *chainRow) Layout(cs gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	c.size = cs.Max
	move := anim.Spring{Response: 0.3, Damping: 0.85}
	x, mid := float32(chainLabelW), rowMid(0)
	rows := c.splitRows(c.placing())
	for ri, row := range rows {
		first, last := c.rowRoom(c.size.W, len(rows))
		room := first
		if ri == len(rows)-1 {
			room = last
		}
		// The cards narrow to leave room for the buttons.
		var want float32
		for _, id := range row {
			want += cardWidth(c.cards[id].slot)
		}
		fit := min(1, (room-float32(len(row))*cardGap)/max(want, 1))
		x, mid = float32(chainLabelW), rowMid(ri)
		for _, id := range row {
			k := c.cards[id]
			w := max(48, cardWidth(k.slot)*fit)
			if !k.placed {
				k.x.Jump(x)
				k.w.Jump(w)
				k.y.Jump(mid)
				k.placed = true
			}
			// The card dragged goes where the pointer has it.
			if id != c.moving {
				k.x.Animate(x, move)
				k.y.Animate(mid, move)
			}
			k.w.Animate(w, move)
			x += w + cardGap
		}
	}
	aw := pillWidth("+ Plugin")
	kids.At(0).Layout(gunim.Tight(geom.Sz(aw, 32)))
	kids.At(0).Place(geom.Pt(x, mid-16))
	c.addX, c.addY = x, mid
	cw := menuPillWidth(c.more.words)
	kids.At(1).Layout(gunim.Tight(geom.Sz(cw, 32)))
	kids.At(1).Place(geom.Pt(c.size.W-cw, rowMid(0)-16))
	// The field over the card named.
	fr := geom.Rc(0, rowMid(0)-16, 160, 32)
	if k := c.cards[c.renaming]; k != nil {
		fr = geom.Rc(k.x.Target(), k.y.Target()-16, max(k.w.Target(), 160), 32)
	}
	kids.At(2).Layout(gunim.Tight(fr.Size()))
	kids.At(2).Place(fr.Min)
	// The chips, each in its card; one leaving, nowhere.
	for i := 3; i < kids.Len(); i++ {
		r := geom.Rect{}
		if k := c.cards[c.chipIn[kids.At(i).Node()]]; k != nil {
			r = c.chipRect(k)
		}
		kids.At(i).Layout(gunim.Tight(r.Size()))
		kids.At(i).Place(r.Min)
	}
	return c.size
}

// placing is the cards in the order they are placed: as the chain has
// them, but the one dragged where it would land, the rest making way.
func (c *chainRow) placing() []int {
	if c.moving == 0 {
		return c.order
	}
	out := make([]int, 0, len(c.order))
	for _, id := range c.order {
		if id != c.moving {
			out = append(out, id)
		}
	}
	return slices.Insert(out, max(0, min(c.moveTo, len(out))), c.moving)
}

// landing is where in the chain the card dragged would land: after
// every card of a row above the row it is on, and every other card of
// its row whose middle it has passed.
func (c *chainRow) landing() int {
	k := c.cards[c.moving]
	mid := k.x.Value() + k.w.Value()/2
	row := c.rowAt(k.y.Value())
	to := 0
	for _, id := range c.order {
		o := c.cards[id]
		if id == c.moving {
			continue
		}
		if r := c.rowAt(o.y.Target()); r < row || r == row && o.x.Target()+o.w.Target()/2 < mid {
			to++
		}
	}
	return to
}

// rowAt is the row of cards nearest y.
func (c *chainRow) rowAt(y float32) int {
	if c.rows < 2 || y < (rowMid(0)+rowMid(1))/2 {
		return 0
	}
	return 1
}

// cardRect is where card k is.
func (c *chainRow) cardRect(k *chainCard) geom.Rect {
	return geom.Rc(k.x.Value(), k.y.Value()-cardH/2, k.w.Value(), cardH)
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
			c.Remove(k.x, k.w, k.y, k.appear, k.on, k.hover, k.open, k.lra)
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
		if k := c.cards[c.moving]; k != nil {
			k.x.Jump(e.Pos.X - c.grab)
			if c.rows > 1 {
				k.y.Jump(min(max(e.Pos.Y, rowMid(0)), rowMid(1)))
			}
			c.moveTo = c.landing()
			break
		}
		id, _ := c.at(e.Pos)
		c.setHover(id)
		// A press that moves far enough along is a drag.
		if k := c.cards[c.down]; k != nil && !e.Touch && len(c.order) > 1 {
			if d := e.Pos.Sub(c.pressAt); d.X*d.X+d.Y*d.Y > 64 {
				c.moving, c.grab = c.down, c.pressAt.X-k.x.Value()
				c.moveTo = slices.Index(c.order, c.down)
				k.x.Jump(e.Pos.X - c.grab)
			}
		}
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
		if light {
			u.Cue(gunim.CueTick, c)
			on := k.slot.Bypass
			k.on.Animate(onOff(on), anim.Snappy)
			u.Send(c, SetBypass{Track: c.track, Slot: id, On: !on})
			break
		}
		// A double-click renames it; a click opens the editor as the
		// button comes up; a drag moves the card.
		if e.Clicks == 2 {
			c.down = -1
			c.rename(id, u)
			break
		}
		c.down, c.pressAt = id, e.Pos
	case input.PointerUp:
		id, down := c.moving, c.down
		c.down = -1
		if id != 0 {
			c.moving = 0
			from, to := slices.Index(c.order, id), c.moveTo
			if from >= 0 && from != to {
				u.Cue(gunim.CueTick, c)
				// Where it lands, as the chain will have it.
				c.order = slices.Insert(slices.Delete(c.order, from, from+1), to, id)
				u.Send(c, MovePlugin{Track: c.track, From: from, To: to})
			}
			break
		}
		k := c.cards[down]
		if at, light := c.at(e.Pos); k == nil || at != down || light {
			return false
		}
		u.Cue(gunim.CueTick, c)
		k.open.Animate(1, anim.Gentle)
		u.Send(c, ShowEditor{Track: c.track, Slot: down})
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
	mid := rowMid(0)
	shaped("CHAIN", 9, true).Paint(p, geom.Pt(4, mid-12), faded(pal.teal, 0.85))
	what := "no plugins"
	if n := len(c.order); n > 0 {
		what = fmt.Sprintf("%d plugin%s", n, map[bool]string{true: "", false: "s"}[n == 1])
	}
	shaped(what, 9, false).Paint(p, geom.Pt(4, mid+2), pal.quiet(0.45))
	// The line the sound runs along, from the label to the add button,
	// down from the end of the first row to the start of the second
	// where there are two, dotted with the sound flowing while it plays.
	if path := c.soundPath(); len(path) > 1 {
		c.paintRanges(p, pal)
		line := faded(pal.ink, 0.12)
		for i := 1; i < len(path); i++ {
			audioui.Segment(p, path[i-1], path[i], 1.5, line)
		}
		if c.r.state.Playing {
			for _, at := range alongPath(path, float32(math.Mod(float64(c.flow), 18)), 18) {
				p.RRect(geom.Rc(at.X-1.5, at.Y-1.5, 3, 3), 1.5, paint.Solid(faded(pal.teal, 0.7)))
			}
		}
	}
	for _, id := range c.drawOrder() {
		c.paintCard(p, pal, c.cards[id])
	}
	// The chips, in the cards in their places.
	for i := 3; i < kids.Len(); i++ {
		if k := c.cards[c.chipIn[kids.At(i).Node()]]; k != nil && k.appear.Value() > 0.99 && !k.gone {
			kids.At(i).Paint(p)
		}
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

// soundPath is the line the sound runs along: from the label, along
// the first row, and where there are two, round from its end down to
// the start of the second, and along it to the add button.
func (c *chainRow) soundPath() []geom.Point {
	start := float32(chainLabelW - 10)
	if c.addX <= chainLabelW {
		return nil
	}
	rows := c.splitRows(c.order)
	if len(rows) < 2 {
		return []geom.Point{{X: start, Y: rowMid(0)}, {X: c.addX - 4, Y: rowMid(0)}}
	}
	end := start
	for _, id := range rows[0] {
		end = max(end, c.cardRect(c.cards[id]).Max.X+cardGap/2)
	}
	turn := (rowMid(0) + rowMid(1)) / 2
	return []geom.Point{{X: start, Y: rowMid(0)}, {X: end, Y: rowMid(0)}, {X: end, Y: turn}, {X: start, Y: turn},
		{X: start, Y: rowMid(1)}, {X: c.addX - 4, Y: rowMid(1)}}
}

// alongPath returns the points along path every step, starting from
// along it.
func alongPath(path []geom.Point, starting, step float32) []geom.Point {
	var out []geom.Point
	at := starting
	var gone float32
	for i := 1; i < len(path); i++ {
		a, b := path[i-1], path[i]
		d := b.Sub(a)
		l := float32(math.Hypot(float64(d.X), float64(d.Y)))
		for ; at <= gone+l; at += step {
			t := (at - gone) / max(l, 1e-6)
			out = append(out, geom.Pt(a.X+d.X*t, a.Y+d.Y*t))
		}
		gone += l
	}
	return out
}

// paintRanges draws, behind the sound's line, a band as tall as the
// loudness range: of the sound fed in, from the label to the first
// card, then of the sound out of each plugin, to the next, and the last
// to the end of its row; where there are two rows, the second takes up
// from its start.
func (c *chainRow) paintRanges(p *paint.Painter, pal palette) {
	alpha := float32(1)
	if c.stale {
		alpha = 0.45
	}
	band := func(x0, x1, mid, lra float32) {
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
	start := float32(chainLabelW - 10)
	x, lra := start, c.inLRA.Value()
	rows := c.splitRows(c.order)
	for ri, row := range rows {
		mid := rowMid(ri)
		if ri > 0 {
			x = start
		}
		for _, id := range row {
			k := c.cards[id]
			r := c.cardRect(k)
			band(x, r.Min.X+8, mid, lra)
			x, lra = r.Max.X-8, k.lra.Value()
		}
		end := c.addX - 4
		if ri < len(rows)-1 {
			end = x + 8 + cardGap/2
		}
		band(x, end, mid, lra)
	}
}

// drawOrder is the cards, those leaving first, under the rest.
func (c *chainRow) drawOrder() []int {
	var out []int
	for id, k := range c.cards {
		if k.gone {
			out = append(out, id)
		}
	}
	out = append(out, c.order...)
	// The card dragged, over the rest.
	if i := slices.Index(out, c.moving); i >= 0 {
		out = append(slices.Delete(out, i, i+1), c.moving)
	}
	return out
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
	if k.slot.ID == c.moving {
		// Lifted, as it is dragged.
		p.ShadowRRect(r, 10, paint.Solid(pal.raised), paint.Shadow{Blur: 18, Offset: geom.Pt(0, 6), Color: faded(pal.night, 0.6)})
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
	if v := c.r.goViews[k.slot.ID]; v != nil && v.chip != nil {
		// The plugin's chip says it.
		return
	}
	paintFit(p, cardDetail(k.slot), 9, false, geom.Pt(r.Min.X+34, r.Min.Y+26), room, detail)
}
