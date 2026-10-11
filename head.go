package main

import (
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// Follow is how the editor follows the playhead.
type Follow int

const (
	// FollowJump turns the view a page on as the playhead leaves it.
	FollowJump Follow = iota
	// FollowScroll keeps the playhead still, a quarter of the way in,
	// the sound sliding under it.
	FollowScroll
	// FollowOff leaves the view where it is.
	FollowOff
	followModes
)

// followNames names the ways to follow, as the toggle says them.
var followNames = map[Follow]string{FollowJump: "Follow: Jump", FollowScroll: "Follow: Scroll", FollowOff: "Follow: Off"}

// next is the way after f, as the toggle steps: Scroll, Jump, Off.
func (f Follow) next() Follow {
	return map[Follow]Follow{FollowScroll: FollowJump, FollowJump: FollowOff, FollowOff: FollowScroll}[f]
}

// trackHead is the track picked's name, over its editor: its title,
// renamed with a double-click on it, and the file it is read from, with
// a button to replace the file, and the toggle of how the editor follows
// the playhead.
type trackHead struct {
	anim.Group
	r        *root
	track    Track
	field    *renameField
	renaming bool
	// note is the track's note, kept as it is typed; view says, and
	// switches, what the area under the head shows; follow says how the
	// editor follows the playhead, and fades away, followIn, while a
	// plugin is shown there.
	note     *widget.TextField
	view     *viewSwitch
	follow   *pill
	followIn *anim.Float
	// titleW is how wide the title is drawn, for the double-click.
	titleW float32
	// later is a track to rename once it is the one shown.
	later int
	hover *anim.Float
	size  geom.Size
}

// renameField is the field a title is renamed in: it renames as Enter
// is pressed or it loses the keyboard, and Escape lets it go unchanged.
type renameField struct {
	*widget.TextField
	h *trackHead
}

// Handle implements [gunim.Handler].
func (f *renameField) Handle(e input.Event, u *gunim.UI) bool {
	if _, ok := e.(input.FocusLost); ok && f.h.renaming {
		f.h.finish(f.Text(), u)
	}
	return f.TextField.Handle(e, u)
}

func newTrackHead(r *root) *trackHead {
	h := &trackHead{r: r, hover: anim.NewFloat(0), followIn: anim.NewFloat(1)}
	h.Add(h.hover, h.followIn)
	h.field = &renameField{TextField: widget.NewTextField(), h: h}
	h.field.OnCommit = func(text string, _ *gunim.UI) gunim.Intent {
		h.renaming = false
		return h.renamed(text)
	}
	h.field.Keys = func(k input.KeyPress, u *gunim.UI) bool {
		if k.Key != input.KeyEscape {
			return false
		}
		h.renaming = false
		u.Focus(r)
		u.Invalidate()
		return true
	}
	h.note = widget.NewTextField()
	h.note.Placeholder = "Add a note, as what the track still needs"
	h.note.OnChange = func(text string, _ *gunim.UI) gunim.Intent {
		if h.track.ID == 0 {
			return nil
		}
		return SetNote{ID: h.track.ID, Note: text}
	}
	h.view = newViewSwitch(r)
	h.follow = newPill(followNames[FollowJump], func(u *gunim.UI) {
		u.Send(r, SetFollow{Follow: r.state.Follow.next()})
	})
	return h
}

// renamed is the rename of the track to text, or nil for none.
func (h *trackHead) renamed(text string) gunim.Intent {
	text = strings.TrimSpace(text)
	if text == "" || text == h.track.Title || h.track.ID == 0 {
		return nil
	}
	return RenameTrack{ID: h.track.ID, Title: text}
}

// finish ends the renaming, renaming the track to text.
func (h *trackHead) finish(text string, u *gunim.UI) {
	h.renaming = false
	if in := h.renamed(text); in != nil {
		u.Send(h, in)
	}
	u.Invalidate()
}

func (h *trackHead) show(t Track, s Album, u *gunim.UI) {
	switched := t.ID != h.track.ID
	if switched {
		h.renaming = false
	}
	h.track = t
	h.note.Disabled = t.ID == 0
	if h.later != 0 && h.later == t.ID {
		h.later = 0
		h.rename(u)
	}
	h.follow.words = followNames[s.Follow]
	h.follow.setLit(s.Follow != FollowOff)

	// The note as kept, but while it is typed in: the field is ahead.
	if u.Focused() != h.note || switched {
		h.note.SetText(t.Note, u)
	}
}

// renameTrack renames track id: at once where it is the one shown, or
// once it is.
func (h *trackHead) renameTrack(id int, u *gunim.UI) {
	if id == h.track.ID {
		h.rename(u)
		return
	}
	h.later = id
}

// rename starts renaming the track picked, its title in a field.
func (h *trackHead) rename(u *gunim.UI) {
	if h.track.ID == 0 {
		return
	}
	h.renaming = true
	h.field.Disabled = false
	h.field.SetText(h.track.Title, u)
	h.field.Select(0, len([]rune(h.track.Title)))
	u.Focus(h.field)
	u.Invalidate()
}

// Children implements [gunim.Composite]: the field is there all
// along, so it takes the keyboard the moment a renaming starts, and is
// shown, and takes anything, only while it does.
func (h *trackHead) Children() []gunim.Node {
	h.field.Disabled = !h.renaming
	return []gunim.Node{h.view, h.follow, h.field, h.note}
}

// Focusable implements [gunim.Focusable].
func (h *trackHead) Focusable() bool { return false }

// Step implements [gunim.Animator]: Follow fades away while a plugin is
// shown under the head, as it follows nothing there.
func (h *trackHead) Step(dt time.Duration) bool {
	if to := onOff(!h.r.pane.isOpen()); h.followIn.Target() != to {
		h.followIn.Animate(to, anim.Spring{Response: 0.25, Damping: 1})
	}
	return h.Group.Step(dt)
}

// Layout implements [gunim.Node]: the switch and Follow at the right,
// Follow taking as much room as it is in; the field over the title.
func (h *trackHead) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	h.size = c.Max
	mid := h.size.H / 2
	in := min(max(h.followIn.Value(), 0), 1)
	fw := pillWidth("Follow: Scroll")
	if in < 0.01 && h.followIn.Target() == 0 {
		kids.At(1).Layout(gunim.Tight(geom.Size{}))
	} else {
		kids.At(1).Layout(gunim.Tight(geom.Sz(fw, 30)))
	}
	kids.At(1).Place(geom.Pt(h.size.W-fw, mid-15))
	fw = (fw + 8) * in
	rw := h.view.width()
	kids.At(0).Layout(gunim.Tight(geom.Sz(rw, switchH)))
	kids.At(0).Place(geom.Pt(h.size.W-fw-rw, mid-switchH/2))
	kids.At(2).Layout(gunim.Tight(geom.Sz(max(min(360, h.size.W-fw-rw-40), 0), 34)))
	kids.At(2).Place(geom.Pt(0, mid-17))
	// The note, from after the title to the buttons.
	h.titleW = min(shaped(h.track.Title, 16, true).Advance, h.size.W*0.3)
	x := h.titleW + 18
	kids.At(3).Layout(gunim.Tight(geom.Sz(max(120, h.size.W-fw-rw-24-x), 32)))
	kids.At(3).Place(geom.Pt(x, mid-16))
	return h.size
}

// Handle implements [gunim.Handler]: a double-click on the title
// renames the track.
func (h *trackHead) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		on := e.Pos.X < h.titleW+8
		h.hover.Animate(map[bool]float32{false: 0, true: 1}[on], anim.Snappy)
	case input.PointerLeave:
		h.hover.Animate(0, anim.Gentle)
	case input.PointerDown:
		if e.Button != input.ButtonPrimary || e.Clicks != 2 || e.Pos.X > h.titleW+8 {
			return false
		}
		h.rename(u)
	default:
		return false
	}
	u.Invalidate()
	return true
}

// Paint implements [gunim.Node]: the title, and after it the note.
func (h *trackHead) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	pal := colours(f.Theme)
	t := h.track
	if t.ID != 0 && !h.renaming {
		if hv := h.hover.Value(); hv > 0.01 {
			p.RRect(geom.Rc(-6, box.H/2-15, h.titleW+12, 30), 8, paint.Solid(faded(pal.ink, 0.06*hv)))
		}
		paintFit(p, t.Title, 16, true, geom.Pt(0, box.H/2-10), h.titleW, pal.ink)
	}
	kids.At(0).Paint(p)
	if in := min(max(h.followIn.Value(), 0), 1); in > 0.01 {
		end := p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: in})
		kids.At(1).Paint(p)
		end()
	}
	if h.renaming {
		kids.At(2).Paint(p)
	} else if t.ID != 0 {
		kids.At(3).Paint(p)
	}
}
