package main

import (
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// chainRow is the track's chain: its plugins as cards, in the order the
// sound runs through them, joined by the sound itself, flowing while it
// plays. A card's light switches the plugin on and off, a click opens
// its editor, and its menu moves and removes it. After the cards, a
// button adds a plugin, found by typing its name; at the end, another
// copies the chain to other tracks.
type chainRow struct {
	anim.Group
	r     *root
	menu  *widget.ContextMenu
	add   *pill
	copy  *pill
	cards map[int]*chainCard
	order []int
	// track is the track shown, and slots its chain.
	track int
	slots []Slot
	// flow is how far the sound has run along the joins, in pixels.
	flow    float32
	hover   int
	press   int
	picker  widget.Palette
	choices []PluginChoice
	// addX is where the add button is.
	addX float32
	size geom.Size
}

// chainCard is a plugin's card, its place and look animated.
type chainCard struct {
	slot        Slot
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
	c := &chainRow{r: r, cards: map[int]*chainCard{}, hover: -1, press: -1}
	c.add = newPill("+ Plugin", c.openPicker)
	c.copy = newPill("Copy to…", c.openCopy)
	c.Add(c.add, c.copy)
	return c
}

func (c *chainRow) card(s Slot) *chainCard {
	k := c.cards[s.ID]
	if k == nil {
		k = &chainCard{x: anim.NewFloat(0), w: anim.NewFloat(0), appear: anim.NewFloat(0), on: anim.NewFloat(1),
			hover: anim.NewFloat(0), open: anim.NewFloat(0)}
		c.Add(k.x, k.w, k.appear, k.on, k.hover, k.open)
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
	c.track, c.slots = t.ID, t.Chain
	keep := map[int]bool{}
	for _, sl := range t.Chain {
		keep[sl.ID] = true
	}
	for id, k := range c.cards {
		switch {
		case keep[id]:
		case switched:
			// Another track's chain: its cards go at once.
			c.Remove(k.x, k.w, k.appear, k.on, k.hover, k.open)
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
		c.order = append(c.order, sl.ID)
	}
	c.choices = s.Plugins
	c.add.setLit(false)
}

// cardWidth is how wide the card of slot s is.
func cardWidth(s Slot) float32 {
	w := max(shaped(s.Name, 12, true).Advance, shaped(cardDetail(s), 9, false).Advance)
	return min(max(w+52, 120), 230)
}

// cardDetail is what a card says under its name: who makes it, and how
// late its sound comes.
func cardDetail(s Slot) string {
	switch {
	case s.Failed != "":
		return "would not load"
	case s.Latency > 0:
		return fmt.Sprintf("%d smp · %s", s.Latency, s.Vendor)
	}
	return s.Vendor
}

// Children implements [gunim.Composite].
func (c *chainRow) Children() []gunim.Node { return []gunim.Node{c.add, c.copy} }

// Layout implements [gunim.Node]: the cards in a row after the label,
// sliding to their places, the add button after them, and the copy
// button at the end.
func (c *chainRow) Layout(cs gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	c.size = cs.Max
	// The cards narrow to leave room for the buttons.
	room := c.size.W - chainLabelW - pillWidth("+ Plugin") - pillWidth("Copy to…") - 16
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
	cw := pillWidth("Copy to…")
	kids.At(1).Layout(gunim.Tight(geom.Sz(cw, 32)))
	kids.At(1).Place(geom.Pt(c.size.W-cw, mid-16))
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
			c.Remove(k.x, k.w, k.appear, k.on, k.hover, k.open)
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
	c.menu.Items = []string{"Open editor", run, "Move earlier", "Move later", "Remove from the chain"}
	c.menu.Icons = []*icon.Icon{icon.SlidersHorizontal, icon.Power, icon.ArrowLeft, icon.ArrowRight, icon.Trash2}
	c.menu.Disabled = []bool{k.slot.Failed != "", false, i == 0, i == len(c.order)-1, false}
	c.menu.Breaks = []int{2, 4}
	c.menu.Captions = nil
	c.menu.Picked = func(item int, u *gunim.UI) {
		switch item {
		case 0:
			u.Send(c, ShowEditor{Track: c.track, Slot: id})
		case 1:
			u.Send(c, SetBypass{Track: c.track, Slot: id, On: !k.slot.Bypass})
		case 2:
			u.Send(c, MovePlugin{Track: c.track, From: i, To: i - 1})
		case 3:
			u.Send(c, MovePlugin{Track: c.track, From: i, To: i + 1})
		case 4:
			u.Send(c, RemovePlugin{Track: c.track, Slot: id})
		}
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
	c.picker.Pick = func(i int, u *gunim.UI) {
		if i >= 0 && i < len(choices) {
			u.Send(c, AddPlugin{Track: track, Choice: choices[i]})
		}
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
	items := []string{"Every other track"}
	for i, t := range c.r.state.Tracks {
		if t.ID != c.track {
			ids = append(ids, t.ID)
			items = append(items, fmt.Sprintf("%02d %s", i+1, t.Title))
		}
	}
	c.menu.Items, c.menu.Icons, c.menu.Captions = items, nil, nil
	c.menu.Disabled = make([]bool, len(items))
	c.menu.Disabled[0] = len(ids) == 0
	c.menu.Breaks = []int{1}
	from := c.track
	c.menu.Picked = func(item int, u *gunim.UI) {
		if item == 0 {
			u.Send(c, CopyChain{From: from})
			return
		}
		u.Send(c, CopyChain{From: from, To: []int{ids[item-1]}})
	}
	c.menu.Open(geom.Pt(c.size.W-pillWidth("Copy to…"), c.size.H/2+16), u)
}

// Paint implements [gunim.Node]: the label, the sound's line through the
// cards, and the cards.
func (c *chainRow) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	mid := box.H / 2
	shaped("CHAIN", 9, true).Paint(p, geom.Pt(4, mid-12), faded(teal, 0.85))
	what := "no plugins"
	if n := len(c.order); n > 0 {
		what = fmt.Sprintf("%d plugin%s", n, map[bool]string{true: "", false: "s"}[n == 1])
	}
	shaped(what, 9, false).Paint(p, geom.Pt(4, mid+2), faded(ink, 0.45))
	// The line the sound runs along, from the label to the add button,
	// dotted with the sound flowing while it plays.
	addX := c.addX
	if addX > chainLabelW {
		line := faded(ink, 0.12)
		segment(p, geom.Pt(chainLabelW-10, mid), geom.Pt(addX-4, mid), 1.5, line)
		if c.r.state.Playing {
			for x := chainLabelW - 10 + float32(math.Mod(float64(c.flow), 18)); x < addX-4; x += 18 {
				p.RRect(geom.Rc(x-1.5, mid-1.5, 3, 3), 1.5, paint.Solid(faded(teal, 0.7)))
			}
		}
	}
	for _, id := range c.drawOrder() {
		c.paintCard(p, c.cards[id])
	}
	for k := range kids.All {
		k.Paint(p)
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
	return append(out, c.order...)
}

func (c *chainRow) paintCard(p *paint.Painter, k *chainCard) {
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
	edge := mix(teal, coral, onOff(failed))
	if open > 0.01 {
		p.ShadowRRect(r, 10, paint.Solid(raised), paint.Shadow{Blur: 16 * open, Color: faded(edge, 0.35*open)})
	}
	p.RRect(r, 10, paint.Solid(mix(raised, mix(raised, ink, 0.06), hover)))
	if open > 0.01 || failed {
		w := max(open, onOff(failed))
		p.RRect(geom.Rc(r.Min.X+10, r.Max.Y-2.5, r.Size().W-20, 2.5), 1.25, paint.Solid(faded(edge, w)))
	}
	// The light: lit while the plugin runs.
	at := c.powerAt(k)
	lit := mix(faded(ink, 0.25), teal, on)
	if on > 0.01 {
		p.ShadowRRect(geom.Rc(at.X-6, at.Y-6, 12, 12), 6, paint.Solid(faded(teal, on)),
			paint.Shadow{Blur: 10 * on, Color: faded(teal, 0.5*on)})
	}
	p.RRect(geom.Rc(at.X-7, at.Y-7, 14, 14), 7, paint.Solid(faded(lit, 0.25+0.2*(1-on))))
	p.RRect(geom.Rc(at.X-4, at.Y-4, 8, 8), 4, paint.Solid(mix(faded(ink, 0.3), night, on)))
	room := r.Size().W - 44
	words := mix(faded(ink, 0.45), ink, on)
	paintFit(p, k.slot.Name, 12, true, geom.Pt(r.Min.X+34, r.Min.Y+8), room, words)
	detail := faded(ink, 0.45)
	if failed {
		detail = coral
	}
	paintFit(p, cardDetail(k.slot), 9, false, geom.Pt(r.Min.X+34, r.Min.Y+26), room, detail)
}
