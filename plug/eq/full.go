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
	// are.
	matchAt, rowAt geom.Rect
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
	v := &full{p: p, host: h, panel: pn, g: pn.graph, listed: anim.NewFloat(0), stopIn: anim.NewFloat(0)}
	v.Add(v.listed, v.stopIn)
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
	v.g.onChange = v.changed
	return v
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
	return []gunim.Node{v.panel, v.row, v.scroll, v.views, v.match, v.stop, v.amount}
}

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
	if listed > 0.99 && v.listed.Target() == 1 {
		kids.At(0).Layout(gunim.Tight(geom.Size{}))
		kids.At(1).Layout(gunim.Tight(geom.Size{}))
	} else {
		kids.At(0).Layout(gunim.Tight(geom.Sz(size.W, max(body-rowH-gap, 0))))
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
		end := p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}.Inset(geom.Uniform(-16)), Opacity: alpha})
		kids.At(i).Paint(p)
		end()
	}
	fade(0, 1-listed, -listed*12)
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
	for i := 3; i < kids.Len(); i++ {
		if i == 5 {
			fade(5, min(max(v.stopIn.Value(), 0), 1), 0)
			continue
		}
		kids.At(i).Paint(p)
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
