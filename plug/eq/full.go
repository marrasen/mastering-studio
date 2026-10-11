package eq

import (
	"fmt"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
	"github.com/marrasen/mastering-studio/plug"
	"github.com/marrasen/mastering-studio/plug/plugui"
)

// full is the EQ as the plugin's pane shows it. Along the top: a switch
// between the graph and the list of bands; the button that matches the
// track to pink noise, with a bar of how far it has read the track and
// a button to stop it, gliding in while it reads; and the amount's knob.
// Under them, the graph with the band picked's row under it, or the
// list of every band's row, crossfading as the switch turns.
type full struct {
	anim.Group
	p      *plugin
	host   plug.Host
	panel  *panel
	g      *graph
	views  *widget.Segmented
	match  *widget.Button
	stop   *widget.IconButton
	amount *plugui.Knob
	row    *bandRow
	list   *bandList
	scroll *widget.Scroll
	// tune opens the tuner of how the graph shows behind the list: its
	// knobs, and reset puts them back; tuneIn is how far it is open.
	tune   *widget.IconButton
	card   *tunerCard
	tuners []*plugui.Knob
	reset  *widget.Button
	tuneIn *anim.Float
	// listed is how far the list shows, from 0 for the graph to 1, and
	// stopIn how far the stop button is in.
	listed, stopIn *anim.Float
	// listening says the track is being read for Auto, progress how far,
	// and shimmer runs along the bar as it reads; stopPoll stops looking
	// how far it has got, and calm puts the button's words back after
	// it tells how it went.
	listening bool
	progress  float32
	shimmer   float32
	stopPoll  func()
	calm      func()
	size      geom.Size
	// matchAt and rowAt are where the match button and the band's row
	// are, and tunerAt the tuner's card.
	matchAt, rowAt, tunerAt geom.Rect
}

// The match button's words, at rest and once done.
const (
	matchWords   = "Match to pink noise"
	matchedWords = "Matched to pink noise"
	matchTip     = "Read the whole track as it reaches the EQ, and shape the bands so it sounds as even as pink noise. " +
		"The bands it makes take the place of these; Undo brings them back."
)

func newFull(p *plugin, h plug.Host) *full {
	pn := newPanel(p, h)
	v := &full{p: p, host: h, panel: pn, g: pn.graph, listed: anim.NewFloat(0), stopIn: anim.NewFloat(0),
		tuneIn: anim.NewFloat(0)}
	v.Add(v.listed, v.stopIn, v.tuneIn)
	v.makeTuner()
	v.views = widget.NewSegmented("Graph", "Bands")
	v.views.Tooltip = "Show the graph, with the band picked under it, or every band with all its knobs"
	v.views.OnChange = func(i int, u *gunim.UI) gunim.Intent {
		v.listed.Animate(float32(i), widget.Settle.Get(u.Theme()))
		v.list.show(u)
		u.Invalidate()
		return nil
	}
	v.match = widget.NewButton(matchWords)
	v.match.Tooltip = matchTip
	v.match.OnClick = func(u *gunim.UI) gunim.Intent {
		v.listen(u)
		return nil
	}
	v.stop = widget.NewIconButton(icon.Square, "Stop reading the track")
	v.stop.OnClick = func(u *gunim.UI) gunim.Intent {
		v.halt(u)
		return nil
	}
	v.amount = plugui.NewKnob(h, Descriptor.Params[1])
	v.amount.Compact = true
	v.amount.Tooltip = "How far every bell and shelf lifts or lowers, their dynamics too, from none to twice"
	v.row = newBandRow(v.g, false)
	v.list = newBandList(v.g)
	v.scroll = widget.NewScroll(v.list)
	// Its edges never fade, which would draw the rows apart from the
	// graph, and their glass would blur nothing.
	v.scroll.NoFade = true
	v.g.onChange = v.changed
	return v
}

// The tuner's knobs: what each turns of the look, and how.
var tunerParams = []plug.Param{
	{Name: "Darkness", Max: 100, Default: defaultLook.Dark * 100, Format: percent},
	{Name: "Blur", Max: 30, Default: defaultLook.Blur, Format: func(v float64) string { return fmt.Sprintf("%.0f px", v) }},
	{Name: "Card", Max: 100, Default: defaultLook.Card * 100, Format: percent},
	{Name: "Curve", Max: 100, Default: defaultLook.Curve * 100, Format: percent},
}

func percent(v float64) string { return fmt.Sprintf("%.0f%%", v) }

// lookOf returns the place of what tuner k turns.
func lookOf(k int) *float64 {
	return []*float64{&look.Dark, &look.Blur, &look.Card, &look.Curve}[k]
}

// makeTuner makes the tuner: a button that opens it, a knob for each of
// the look's values, as they show at once, and a button to put them
// back.
func (v *full) makeTuner() {
	v.tune = widget.NewIconButton(icon.SlidersHorizontal, "Tune how the graph shows behind the bands")
	v.tune.OnClick = func(u *gunim.UI) gunim.Intent {
		open := v.tuneIn.Target() == 0
		v.tune.Active = open
		v.tuneIn.Animate(onOff(open), widget.Settle.Get(u.Theme()))
		u.Invalidate()
		return nil
	}
	tips := []string{"How dark the graph goes behind the bands", "How far each band's card blurs the graph behind it",
		"How much of each card's own colour shows", "How bright the curve and the points stay over the dark"}
	for k, prm := range tunerParams {
		scale := 100.0
		if k == 1 {
			scale = 1
		}
		at := lookOf(k)
		kn := plugui.NewKnobFunc(prm, *at*scale, func(*gunim.UI) {}, func(x float64, u *gunim.UI) {
			*at = x / scale
			u.Invalidate()
		}, func(*gunim.UI) {})
		kn.Compact, kn.Tooltip = true, tips[k]
		v.tuners = append(v.tuners, kn)
	}
	v.card = &tunerCard{}
	v.reset = widget.NewButton("Reset")
	v.reset.Tooltip = "Put the look back as it starts"
	v.reset.OnClick = func(u *gunim.UI) gunim.Intent {
		look = defaultLook
		for k, kn := range v.tuners {
			scale := 100.0
			if k == 1 {
				scale = 1
			}
			kn.Follow(*lookOf(k)*scale, u)
		}
		u.Invalidate()
		return nil
	}
}

// changed shows the band picked in the row, and every band in the list.
func (v *full) changed(u *gunim.UI) {
	b, i := v.g.band(v.g.sel)
	v.row.show(b, i, u)
	v.list.show(u)
	v.matchable()
}

// matchable greys the match button where there is no track to read.
func (v *full) matchable() {
	if v.listening {
		return
	}
	v.match.Disabled = !v.p.canRead()
	v.match.Tooltip = matchTip
	if v.match.Disabled {
		v.match.Tooltip = "The host gives the EQ no track to read"
	}
}

// listen starts reading the track, for Auto, and follows it.
func (v *full) listen(u *gunim.UI) {
	if v.listening || !v.p.canRead() {
		return
	}
	if v.calm != nil {
		v.calm()
		v.calm = nil
	}
	v.listening, v.progress = true, 0
	v.match.Active = true
	v.stopIn.Animate(1, widget.Bounce.Get(u.Theme()))
	v.p.startReading()
	v.watchReading(u)
}

// halt stops reading the track.
func (v *full) halt(u *gunim.UI) {
	if !v.listening {
		return
	}
	v.p.stopReading()
	if v.stopPoll != nil {
		v.stopPoll()
	}
	v.done(matchWords, u)
}

// readPoll is how often Auto looks how far the reading has got, and
// tellFor how long the button tells how it went.
const (
	readPoll = 50 * time.Millisecond
	tellFor  = 2500 * time.Millisecond
)

// watchReading looks how far the reading has got: while it reads, it
// shows how far and looks again a moment later; once the track is read,
// it shapes the bands to bring it to pink, as one step to undo.
func (v *full) watchReading(u *gunim.UI) {
	if !v.listening {
		return
	}
	levels, reading, progress, err := v.p.readState()
	switch {
	case reading:
		v.progress = float32(progress)
		v.match.Label = fmt.Sprintf("Reading the track… %.0f%%", 100*progress)
		v.stopPoll = u.After(readPoll, v.watchReading)
		u.Invalidate()
		return
	case err != nil:
		v.match.Tooltip = "Could not read the track: " + err.Error()
		v.done("Could not read the track", u)
	case len(levels) == len(autoFreqs):
		bands := autoBands(levels)
		v.g.edit("Auto EQ", true, u, func() bool {
			v.g.bands, v.g.sel = bands, 0
			return true
		})
		u.Cue(gunim.CueSelect, v)
		v.done(matchedWords, u)
	default:
		v.done(matchWords, u)
	}
}

// done ends the reading, the button saying words for a moment.
func (v *full) done(words string, u *gunim.UI) {
	v.listening, v.match.Active = false, false
	v.stopIn.Animate(0, widget.Settle.Get(u.Theme()))
	v.match.Label = words
	if words != matchWords {
		v.calm = u.After(tellFor, func(u *gunim.UI) {
			v.match.Label, v.calm = matchWords, nil
			v.matchable()
			u.Invalidate()
		})
	}
	u.Invalidate()
}

// Step implements [gunim.Animator]: the shimmer runs along the bar
// while the track is read.
func (v *full) Step(dt time.Duration) bool {
	moving := v.Group.Step(dt)
	if v.listening {
		v.shimmer += float32(dt.Seconds()) * 0.8
		v.shimmer -= float32(int(v.shimmer))
		return true
	}
	return moving
}

// FollowState implements [plug.StateFollower]: the graph, the row and
// the list show the bands.
func (v *full) FollowState(u *gunim.UI) {
	v.panel.FollowState(u)
	v.changed(u)
}

// FollowDocument implements [plug.DocumentFollower]: the match button
// can read the track, or not.
func (v *full) FollowDocument(u *gunim.UI) {
	v.matchable()
	u.Invalidate()
}

// Follow implements [plug.Follower].
func (v *full) Follow(id uint32, val float64, u *gunim.UI) {
	v.panel.Follow(id, val, u)
	if id == AmountID {
		v.amount.Follow(val, u)
	}
}

// Children implements [gunim.Composite].
func (v *full) Children() []gunim.Node {
	kids := make([]gunim.Node, 0, 10+len(v.tuners))
	kids = append(kids, v.panel, v.row, v.scroll, v.views, v.match, v.stop, v.amount, v.tune, v.card, v.reset)
	for _, k := range v.tuners {
		kids = append(kids, k)
	}
	return kids
}

// The tuner's card: how wide and tall it is.
const tunerW, tunerH = 4*plugui.CompactW + 3*8 + 2*12, plugui.CompactH + 44

// barH is how tall the bar along the top is, and gap the room between
// its parts.
const (
	barH = plugui.CompactH
	gap  = 10
)

// Layout implements [gunim.Node].
func (v *full) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	v.size = c.Max
	size := v.size
	// Along the top: the switch at the left, the knob at the right, and
	// the match button, and the stop button as far in as it is, before
	// the knob.
	sw := kids.At(3).Layout(gunim.Constraints{Max: geom.Sz(220, 32)})
	kids.At(3).Place(geom.Pt(0, (barH-sw.H)/2))
	kids.At(6).Layout(gunim.Tight(geom.Sz(plugui.CompactW, plugui.CompactH)))
	kids.At(6).Place(geom.Pt(size.W-plugui.CompactW, 0))
	x := size.W - plugui.CompactW - gap
	stopIn := min(max(v.stopIn.Value(), 0), 1)
	stopW := (32 + 6) * stopIn
	if stopIn < 0.01 && v.stopIn.Target() == 0 {
		kids.At(5).Layout(gunim.Tight(geom.Size{}))
	} else {
		kids.At(5).Layout(gunim.Tight(geom.Sz(32, 32)))
	}
	kids.At(5).Place(geom.Pt(x-32, (barH-32)/2))
	mw := kids.At(4).Layout(gunim.Constraints{Max: geom.Sz(260, 32)})
	v.matchAt = geom.Rc(x-stopW-mw.W, (barH-mw.H)/2, mw.W, mw.H)
	kids.At(4).Place(v.matchAt.Min)
	// The graph and its row, or the list, the one out of sight taking no
	// room, so the pointer reaches only the one shown.
	top := float32(barH + gap)
	body := max(size.H-top, 0)
	listed := min(max(v.listed.Value(), 0), 1)
	rowH := float32(plugui.KnobH)
	// The graph grows to fill the room as the list comes over it.
	v.g.behind = listed
	kids.At(0).Layout(gunim.Tight(geom.Sz(size.W, max(body-(rowH+gap)*(1-listed), 0))))
	if listed > 0.99 && v.listed.Target() == 1 {
		kids.At(1).Layout(gunim.Tight(geom.Size{}))
	} else {
		kids.At(1).Layout(gunim.Tight(geom.Sz(min(size.W, v.row.width()), rowH)))
	}
	kids.At(0).Place(geom.Pt(0, top))
	v.rowAt = geom.Rc(0, top+max(body-rowH, 0), size.W, rowH)
	kids.At(1).Place(v.rowAt.Min)
	if listed < 0.01 && v.listed.Target() == 0 {
		kids.At(2).Layout(gunim.Tight(geom.Size{}))
	} else {
		kids.At(2).Layout(gunim.Tight(geom.Sz(size.W, body)))
	}
	kids.At(2).Place(geom.Pt(0, top))
	// The tuner's button, after the switch, as the list shows; its card
	// under it, as it opens.
	tuneShown := !(listed < 0.01 && v.listed.Target() == 0)
	if tuneShown {
		kids.At(7).Layout(gunim.Tight(geom.Sz(32, 32)))
	} else {
		kids.At(7).Layout(gunim.Tight(geom.Size{}))
	}
	kids.At(7).Place(geom.Pt(sw.W+gap, (barH-32)/2))
	open := tuneShown && !(v.tuneIn.Value() < 0.01 && v.tuneIn.Target() == 0)
	v.tunerAt = geom.Rc(sw.W+gap, barH-4, tunerW, tunerH)
	in := v.tunerAt.Min.Add(geom.Pt(12, 10))
	if open {
		kids.At(8).Layout(gunim.Tight(v.tunerAt.Size()))
	} else {
		kids.At(8).Layout(gunim.Tight(geom.Size{}))
	}
	kids.At(8).Place(v.tunerAt.Min)
	for k := range v.tuners {
		if open {
			kids.At(10 + k).Layout(gunim.Tight(geom.Sz(plugui.CompactW, plugui.CompactH)))
		} else {
			kids.At(10 + k).Layout(gunim.Tight(geom.Size{}))
		}
		kids.At(10 + k).Place(in.Add(geom.Pt(float32(k)*(plugui.CompactW+8), 0)))
	}
	if open {
		kids.At(9).Layout(gunim.Constraints{Max: geom.Sz(100, 28)})
	} else {
		kids.At(9).Layout(gunim.Tight(geom.Size{}))
	}
	kids.At(9).Place(in.Add(geom.Pt(0, plugui.CompactH+4)))
	return size
}

// Paint implements [gunim.Node]: the bar along the top, the graph and
// its row faded out as the list fades in, and the bar of how far the
// track is read under the match button.
func (v *full) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	listed := min(max(v.listed.Value(), 0), 1)
	fade := func(i int, alpha float32, dy float32) {
		if alpha < 0.01 {
			return
		}
		defer p.Push(paint.Translate(geom.Pt(0, dy)))()
		if alpha > 0.999 {
			// Whole, it draws in place: the glass of the list's rows
			// blurs the graph drawn before them.
			kids.At(i).Paint(p)
			return
		}
		end := p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}.Inset(geom.Uniform(-16)), Opacity: alpha})
		kids.At(i).Paint(p)
		end()
	}
	// The graph stays, darkened as the list comes over it.
	kids.At(0).Paint(p)
	fade(1, 1-listed, -listed*12)
	fade(2, listed, (1-listed)*12)
	if b, _ := v.g.band(v.g.sel); b == nil && listed < 0.99 {
		// No band picked: how to pick one, where its row would be.
		r := v.rowAt
		msg := "Pick a band to change it, or double-click the graph to add one. Drag a point to move it, " +
			"scroll on it to change its width, and right-click it for more."
		paintFit(p, msg, ReadoutSize.Get(th), false, geom.Pt(4, r.Min.Y+(r.Size().H-ReadoutSize.Get(th))/2),
			box.W-8, audioui.Faded(widget.Ink.Get(th), 0.5*(1-listed)*(1-v.row.in.Value())))
	}
	for i := 3; i < 7; i++ {
		if i == 5 {
			fade(5, min(max(v.stopIn.Value(), 0), 1), 0)
			continue
		}
		kids.At(i).Paint(p)
	}
	fade(7, listed, 0)
	if in := min(max(v.tuneIn.Value(), 0), 1) * listed; in > 0.01 {
		// The tuner's card, over the list, dropping in.
		defer p.Push(paint.Translate(geom.Pt(0, -(1-in)*8)))()
		end := p.Layer(paint.LayerOpts{Bounds: v.tunerAt.Inset(geom.Uniform(-20)), Opacity: in})
		for i := 8; i < kids.Len(); i++ {
			kids.At(i).Paint(p)
		}
		end()
	}
	if v.listening || v.stopIn.Value() > 0.01 {
		// How far the track is read, a light running along it.
		mb := v.matchAt
		bar := geom.Rc(mb.Min.X+8, mb.Max.Y+3, mb.Size().W-16, 3)
		accent := widget.Accent.Get(th)
		alpha := min(max(v.stopIn.Value(), 0), 1)
		p.RRect(bar, 1.5, paint.Solid(audioui.Faded(widget.Ink.Get(th), 0.1*alpha)))
		done := geom.Rc(bar.Min.X, bar.Min.Y, bar.Size().W*v.progress, 3)
		p.RRect(done, 1.5, paint.Solid(audioui.Faded(accent, alpha)))
		x := bar.Min.X + bar.Size().W*v.shimmer
		p.ShadowRRect(geom.Rc(x-6, bar.Min.Y, 12, 3), 1.5, paint.Solid(audioui.Faded(accent, 0.6*alpha)),
			paint.Shadow{Blur: 8, Color: audioui.Faded(accent, 0.5*alpha)})
	}
}

// tunerCard is the tuner's card, under its knobs. Over the list, it is
// what a click on it reaches, and nothing under it. Its one byte keeps
// it apart from another in the tree, as a type of no size would not be.
type tunerCard struct{ _ byte }

// Layout implements [gunim.Node].
func (*tunerCard) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return c.Max
}

// Paint implements [gunim.Node]: a raised card, its shadow under it.
func (*tunerCard) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	r := geom.Rect{Max: box.Point()}
	p.ShadowRRect(r, 12, paint.Solid(audioui.Mix(audioui.Ground.Get(th), widget.Ink.Get(th), 0.08)),
		paint.Shadow{Blur: 18, Offset: geom.Pt(0, 6), Color: audioui.Faded(audioui.Ground.Get(th), 0.8)})
	p.RRectStroke(r, 12, paint.Fill{}, paint.Stroke{Width: 1, Color: audioui.Faded(widget.Ink.Get(th), 0.12)})
}
