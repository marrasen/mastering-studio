package main

import (
	"fmt"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// transport plays the track picked from its start, plays and pauses
// it, steps to the tracks
// either side, and says where it is; Match levels plays every track at
// the target loudness, so the ear compares their sound alone; Album
// plays on through the album, track into track, and the volume sets how
// loud to listen.
type transport struct {
	r                *root
	back, play, next *iconButton
	restart          *iconButton
	match            *pill
	album            *pill
	// bypass plays the track without its chain and gains, to compare;
	// carry turns through where a track picked while one plays starts,
	// its tip saying which.
	bypass  *pill
	carry   *iconButton
	carryTo *widget.Tooltip
	// compact says the buttons are in fewer words, for a narrow window;
	// pillsX is where they start, and matchMid where Match's middle is.
	compact  bool
	pillsX   float32
	matchMid float32
	volume   *valueChip
}

func newTransport(r *root) *transport {
	t := &transport{r: r}
	t.restart = newIconButton(icon.RotateCcw, func(u *gunim.UI) { u.Send(r, PlayFromStart{}) })
	t.back = newIconButton(icon.SkipBack, func(u *gunim.UI) { r.step(-1, u) })
	t.play = newIconButton(icon.Play, func(u *gunim.UI) { u.Send(r, TogglePlay{}) })
	t.play.primary = true
	t.next = newIconButton(icon.SkipForward, func(u *gunim.UI) { r.step(1, u) })
	t.carry = newIconButton(icon.Percent, func(u *gunim.UI) {
		u.Send(r, SetCarry{Carry: (r.state.Carry + 1) % carries})
	})
	t.carryTo = widget.NewTooltip(t.carry, carryLooks[CarryPercent].tip)
	t.match = newPill("Match levels", func(u *gunim.UI) {
		// Matching a track not yet measured waits on its loudness: the
		// button measures it.
		if t.match.warn {
			u.Send(r, MeasureLoudness{})
			return
		}
		u.Send(r, SetMatch{On: !r.state.Match})
	})
	t.bypass = newPill("Bypass", func(u *gunim.UI) { u.Send(r, SetBypassAll{On: !r.state.Bypass}) })
	t.album = newPill("Autoplay next", func(u *gunim.UI) { u.Send(r, SetAlbumPlay{On: !r.state.AlbumPlay}) })
	t.volume = newValueChip("LISTEN", func(v float64) string { return fmt.Sprintf("%.0f%%", v*100) }, 0.004, 0.05, 0, 1, 0.8,
		func(v float64, u *gunim.UI) { u.Send(r, SetVolume{Volume: float32(v)}) })
	return t
}

func (t *transport) show(s Album) {
	if s.Playing {
		t.play.morph(icon.Pause)
	} else {
		t.play.morph(icon.Play)
	}
	t.match.setLit(s.Match)
	t.match.words, t.match.warn = "Match levels", false
	if tr, ok := t.r.track(); ok && s.Match {
		if _, matched := s.matchDB(&tr); !matched {
			t.match.words, t.match.warn = "Measure loudness first", true
			if tr.Measuring {
				t.match.words = "Measuring…"
			}
		}
	}
	t.album.setLit(s.AlbumPlay)
	look := carryLooks[s.Carry%carries]
	t.carry.morph(look.icon)
	t.carryTo.Text = look.tip
	t.bypass.warn = s.Bypass
	t.volume.value = float64(s.Volume)
}

// Children implements [gunim.Composite].
func (t *transport) Children() []gunim.Node {
	return []gunim.Node{t.back, t.play, t.next, t.match, t.volume, t.album, t.restart, t.bypass, t.carryTo}
}

// Layout implements [gunim.Node]: the buttons left, the level right.
func (t *transport) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	mid := size.H / 2
	kids.At(6).Layout(gunim.Tight(geom.Sz(40, 40)))
	kids.At(6).Place(geom.Pt(8, mid-20))
	kids.At(0).Layout(gunim.Tight(geom.Sz(40, 40)))
	kids.At(0).Place(geom.Pt(52, mid-20))
	kids.At(1).Layout(gunim.Tight(geom.Sz(56, 56)))
	kids.At(1).Place(geom.Pt(100, mid-28))
	kids.At(2).Layout(gunim.Tight(geom.Sz(40, 40)))
	kids.At(2).Place(geom.Pt(164, mid-20))
	kids.At(8).Layout(gunim.Tight(geom.Sz(34, 34)))
	kids.At(8).Place(geom.Pt(210, mid-17))
	// The buttons at the right in their words, or, where they would
	// crowd the time, in fewer.
	short := map[string]string{"Autoplay next": "Autoplay", "Match levels": "Match", "Measure loudness first": "Measure first"}
	for _, b := range []*pill{t.album, t.match} {
		if s, ok := short[b.words]; ok && t.compact {
			b.words = s
		}
	}
	widths := func() float32 {
		return 84 + 12 + pillWidth(t.match.words) + 8 + pillWidth(t.album.words) + 8 + pillWidth(t.bypass.words)
	}
	if !t.compact && size.W-widths() < timeX+timeRoom {
		t.compact = true
		for _, b := range []*pill{t.album, t.match} {
			if s, ok := short[b.words]; ok {
				b.words = s
			}
		}
	} else if t.compact && size.W-widths() > timeX+timeRoom+80 {
		t.compact = false
	}
	x := size.W - 84
	kids.At(4).Layout(gunim.Tight(geom.Sz(84, 44)))
	kids.At(4).Place(geom.Pt(x, mid-22))
	x -= 12
	for _, k := range []struct {
		i int
		b *pill
	}{{3, t.match}, {5, t.album}, {7, t.bypass}} {
		w := pillWidth(k.b.words)
		x -= w
		kids.At(k.i).Layout(gunim.Tight(geom.Sz(w, 34)))
		kids.At(k.i).Place(geom.Pt(x, mid-17))
		if k.i == 3 {
			t.matchMid = x + w/2
		}
		x -= 8
	}
	t.pillsX = x
	return size
}

// timeRoom is the room the time takes, after timeX.
const timeRoom = 180

// timeX is where the time is written, after the buttons.
const timeX = 258

// Paint implements [gunim.Node]: the buttons, and the time and the
// track playing between them and the level.
func (t *transport) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 16, paint.Solid(panel))
	at, length, id := t.r.d.position()
	tr, ok := t.r.track()
	if ok && id == tr.ID {
		run := shapedFace(clock(at), 22, true)
		run.Paint(p, geom.Pt(timeX, box.H/2-20), ink)
		shapedFace("/ "+clock(length), 12, false).Paint(p, geom.Pt(timeX+run.Advance+8, box.H/2-10), faded(ink, 0.45))
	} else if ok {
		shapedFace(clock(0), 22, true).Paint(p, geom.Pt(timeX, box.H/2-20), faded(ink, 0.6))
	}
	if ok {
		// The title, where there is room for it.
		if room := t.pillsX - timeX - 8; room > 60 {
			paintFit(p, tr.Title, 12, false, geom.Pt(timeX, box.H/2+10), room, faded(ink, 0.55))
		}
	}
	if d, matched := t.r.state.matchDB(&tr); ok && matched {
		words := fmt.Sprintf("%+.1f dB to match", d)
		run := shaped(words, 10, false)
		run.Paint(p, geom.Pt(t.matchMid-run.Advance/2, box.H/2+19), faded(teal, 0.8))
	}
	for k := range kids.All {
		k.Paint(p)
	}
}

// Step implements [gunim.Animator]: the time moves while a track
// plays.
func (t *transport) Step(time.Duration) bool { return t.r.state.Playing }
