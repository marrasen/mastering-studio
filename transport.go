package main

import (
	"fmt"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
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
	volume           *valueChip
}

func newTransport(r *root) *transport {
	t := &transport{r: r}
	t.restart = newIconButton(icon.RotateCcw, func(u *gunim.UI) { u.Send(r, PlayFromStart{}) })
	t.back = newIconButton(icon.SkipBack, func(u *gunim.UI) { r.step(-1, u) })
	t.play = newIconButton(icon.Play, func(u *gunim.UI) { u.Send(r, TogglePlay{}) })
	t.play.primary = true
	t.next = newIconButton(icon.SkipForward, func(u *gunim.UI) { r.step(1, u) })
	t.match = newPill("Match levels", func(u *gunim.UI) { u.Send(r, SetMatch{On: !r.state.Match}) })
	t.album = newPill("Album", func(u *gunim.UI) { u.Send(r, SetAlbumPlay{On: !r.state.AlbumPlay}) })
	t.volume = newValueChip("LISTEN", func(v float64) string { return fmt.Sprintf("%.0f%%", v*100) }, 0.004, 0.05, 0, 1, 0.8,
		func(v float64, u *gunim.UI) { u.Send(r, SetVolume{Volume: float32(v)}) })
	return t
}

func (t *transport) show(s Album) {
	if s.Playing {
		t.play.ic = icon.Pause
	} else {
		t.play.ic = icon.Play
	}
	t.match.setLit(s.Match)
	t.album.setLit(s.AlbumPlay)
	t.volume.value = float64(s.Volume)
}

// Children implements [gunim.Composite].
func (t *transport) Children() []gunim.Node {
	return []gunim.Node{t.back, t.play, t.next, t.match, t.volume, t.album, t.restart}
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
	w := pillWidth("Match levels")
	kids.At(4).Layout(gunim.Tight(geom.Sz(84, 44)))
	kids.At(4).Place(geom.Pt(size.W-84, mid-22))
	kids.At(3).Layout(gunim.Tight(geom.Sz(w, 34)))
	kids.At(3).Place(geom.Pt(size.W-84-12-w, mid-17))
	aw := pillWidth("Album")
	kids.At(5).Layout(gunim.Tight(geom.Sz(aw, 34)))
	kids.At(5).Place(geom.Pt(size.W-84-12-w-8-aw, mid-17))
	return size
}

// timeX is where the time is written, after the buttons.
const timeX = 222

// Paint implements [gunim.Node]: the buttons, and the time and the
// track playing between them and the level.
func (t *transport) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 16, paint.Solid(panel))
	at, length, id := t.r.d.position()
	tr, ok := t.r.track()
	if ok && id == tr.ID {
		run := shapedFace(clock(at), 22, true, true)
		run.Paint(p, geom.Pt(timeX, box.H/2-20), ink)
		shapedFace("/ "+clock(length), 12, false, true).Paint(p, geom.Pt(timeX+run.Advance+8, box.H/2-10), faded(ink, 0.45))
	} else if ok {
		shapedFace(clock(0), 22, true, true).Paint(p, geom.Pt(timeX, box.H/2-20), faded(ink, 0.6))
	}
	if ok {
		room := box.W - timeX - pillWidth("Match levels") - pillWidth("Album") - 128
		paintFit(p, tr.Title, 12, false, geom.Pt(timeX, box.H/2+10), room, faded(ink, 0.55))
	}
	if t.r.state.Match && ok && tr.Measured && tr.Measure.Loud {
		d := t.r.state.Target - tr.Measure.LUFS
		words := fmt.Sprintf("%+.1f dB to match", d)
		run := shaped(words, 10, false)
		run.Paint(p, geom.Pt(box.W-84-12-pillWidth("Match levels")/2-run.Advance/2, box.H/2+19), faded(teal, 0.8))
	}
	for k := range kids.All {
		k.Paint(p)
	}
}

// Step implements [gunim.Animator]: the time moves while a track
// plays.
func (t *transport) Step(time.Duration) bool { return t.r.state.Playing }
