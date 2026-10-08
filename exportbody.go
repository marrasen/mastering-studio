package main

import (
	"fmt"
	"image/color"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// exportBody is the export dialog's body, in three parts: where the
// files go, the folder written whole and a button to change it; the
// formats, a card each, WAV with its samples and dither, MP3 with its
// bitrate and the LAME that encodes it; and the tracks, a list of a
// set height that scrolls, ticked to export, each with its length and
// loudness. Under them, a line sums the export up.
type exportBody struct {
	dir    string
	change *widget.Button
	wav    *widget.Checkbox
	bits   *widget.Segmented
	dither *widget.Checkbox
	mp3    *widget.Checkbox
	rate   *widget.Dropdown
	locate *widget.Button
	report *widget.Checkbox
	// notes copies the tracks' notes, the text it copies.
	notes     *widget.Button
	notesText string
	lameAt    string
	list      *exportList
	scroll    *widget.Scroll
	box       *widget.Sized
	size      geom.Size
}

// The body's measures.
const (
	exHead   = 22
	exPathH  = 36
	exCardH  = 104
	exRowH   = 34
	exRows   = 7
	exListHd = 30
	exGap    = 18
)

func newExportBody(d ExportDraft) *exportBody {
	b := &exportBody{dir: d.Dir}
	b.change = widget.NewButton("Change…")
	b.change.OnClick = widget.Sends(ChooseExportDir{})
	b.wav = widget.NewCheckbox("WAV")
	b.wav.SetChecked(d.WAV, nil)
	names := make([]string, len(bitsChoices))
	for i, c := range bitsChoices {
		names[i] = c.name
	}
	b.bits = widget.NewSegmented(names...)
	b.bits.SetSelected(max(0, slices.IndexFunc(bitsChoices, func(c bitsChoice) bool { return c.bits == d.Bits })), nil)
	b.dither = widget.NewCheckbox("Dither")
	b.dither.SetChecked(d.Dither, nil)
	b.mp3 = widget.NewCheckbox("MP3")
	b.mp3.SetChecked(d.MP3, nil)
	rates := make([]widget.MenuItem, len(d.MP3Rates))
	for i, r := range d.MP3Rates {
		rates[i].Label = fmt.Sprintf("%d kbps", r)
	}
	b.rate = widget.NewDropdown(rates)
	b.rate.SetSelected(max(0, slices.Index(d.MP3Rates, d.MP3Rate)), nil)
	b.locate = widget.NewButton("Locate LAME…")
	b.locate.OnClick = widget.Sends(LocateLAME{})
	b.report = widget.NewCheckbox("Write report")
	b.report.SetChecked(d.Report, nil)
	b.notesText = notesText(d)
	b.notes = widget.NewButton("Copy notes to clipboard")
	b.notes.Disabled = !slices.ContainsFunc(d.Tracks, func(t ExportTrack) bool {
		return strings.TrimSpace(t.Note) != "" || len(t.Marks) > 0
	})
	b.notes.OnClick = func(u *gunim.UI) gunim.Intent {
		u.SetClipboard(b.notesText)
		// Told as done a moment, then as it was.
		b.notes.Label = "Copied"
		u.After(2*time.Second, func(u *gunim.UI) {
			b.notes.Label = "Copy notes to clipboard"
			u.Invalidate()
		})
		u.Invalidate()
		return nil
	}
	b.list = newExportList(d.Tracks)
	b.scroll = widget.NewScroll(b.list)
	b.box = widget.NewSized(b.scroll, 0, float32(min(max(len(d.Tracks), 3), exRows))*exRowH)
	b.lame(d.LAME)
	return b
}

// lame shows MP3 as LAME is found, at path, or not.
func (b *exportBody) lame(path string) {
	found := path != ""
	b.lameAt = path
	b.mp3.Disabled, b.rate.Disabled = !found, !found
	if !found {
		b.mp3.SetChecked(false, nil)
	}
}

// Focusables implements the dialog's way to Tab through the body.
func (b *exportBody) Focusables() []gunim.Node {
	return []gunim.Node{b.change, b.wav, b.bits, b.dither, b.mp3, b.rate, b.locate, b.notes, b.report}
}

// Children implements [gunim.Composite].
func (b *exportBody) Children() []gunim.Node {
	return []gunim.Node{b.change, b.wav, b.bits, b.dither, b.mp3, b.rate, b.locate, b.box, b.report, b.notes}
}

// exportPlaces are where the body's parts are.
type exportPlaces struct {
	path, wav, mp3, head, list geom.Rect
	// sum is how far down the sum is.
	sum float32
}

// places are the parts' places, from the body's width.
func (b *exportBody) places(w float32) exportPlaces {
	var at exportPlaces
	y := float32(exHead)
	at.path = geom.Rc(0, y, w-130, exPathH)
	y += exPathH + exGap + exHead
	cw := (w - 12) / 2
	at.wav = geom.Rc(0, y, cw, exCardH)
	at.mp3 = geom.Rc(cw+12, y, cw, exCardH)
	y += exCardH + exGap + exHead
	at.head = geom.Rc(0, y, w, exListHd)
	y += exListHd
	at.list = geom.Rc(0, y, w, b.box.Height)
	at.sum = y + b.box.Height + 14
	return at
}

// Layout implements [gunim.Node].
func (b *exportBody) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	w := c.Max.W
	at := b.places(w)
	path, wavCard, mp3Card, list, sum := at.path, at.wav, at.mp3, at.list, at.sum
	place := func(i int, at geom.Point, cs gunim.Constraints) geom.Size {
		s := kids.At(i).Layout(cs)
		kids.At(i).Place(at)
		return s
	}
	loose := gunim.Loose(geom.Sz(w, 200))
	cs := place(0, geom.Pt(0, 0), loose)
	kids.At(0).Place(geom.Pt(w-cs.W, path.Min.Y+(exPathH-cs.H)/2))
	pad := float32(14)
	// The WAV card: its tick, and dither, across the top; the samples
	// under them.
	place(1, wavCard.Min.Add(geom.Pt(pad, pad)), loose)
	ds := kids.At(3).Layout(loose)
	kids.At(3).Place(geom.Pt(wavCard.Max.X-pad-ds.W, wavCard.Min.Y+pad))
	place(2, geom.Pt(wavCard.Min.X+pad, wavCard.Max.Y-pad-36), gunim.Constraints{Max: geom.Sz(wavCard.Size().W-2*pad, 36)})
	// The MP3 card: its tick, and the bitrate, across the top; LAME
	// under them.
	place(4, mp3Card.Min.Add(geom.Pt(pad, pad)), loose)
	rs := kids.At(5).Layout(gunim.Loose(geom.Sz(130, 40)))
	kids.At(5).Place(geom.Pt(mp3Card.Max.X-pad-rs.W, mp3Card.Min.Y+pad-4))
	ls := kids.At(6).Layout(loose)
	kids.At(6).Place(geom.Pt(mp3Card.Max.X-pad-ls.W, mp3Card.Max.Y-pad-ls.H))
	place(7, list.Min, gunim.Tight(list.Size()))
	// The report's tick, on the sum's line, at the right.
	rs2 := kids.At(8).Layout(loose)
	kids.At(8).Place(geom.Pt(w-rs2.W, sum-4))
	// The notes' button, at the right of the tracks' heading.
	ns := kids.At(9).Layout(gunim.Loose(geom.Sz(w, 28)))
	kids.At(9).Place(geom.Pt(w-ns.W, at.head.Min.Y-exHead-ns.H+exHead-2))
	b.size = geom.Sz(w, sum+24)
	return b.size
}

// Handle implements [gunim.Handler]: the tick in the list's head ticks
// every track, or none.
func (b *exportBody) Handle(e input.Event, u *gunim.UI) bool {
	d, ok := e.(input.PointerDown)
	if !ok || d.Button != input.ButtonPrimary {
		return false
	}
	head := b.places(b.size.W).head
	if !geom.Rc(head.Min.X, head.Min.Y, 140, head.Size().H).Contains(d.Pos) {
		return false
	}
	all := len(b.list.ticked()) < len(b.list.tracks)
	for i := range b.list.on {
		b.list.on[i] = all
	}
	u.Cue(gunim.CueTick, b)
	u.Invalidate()
	return true
}

// Paint implements [gunim.Node].
func (b *exportBody) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	at := b.places(box.W)
	path, wavCard, mp3Card, head, list, sum := at.path, at.wav, at.mp3, at.head, at.list, at.sum
	heading := func(s string, y float32) {
		shaped(s, 10, true).Paint(p, geom.Pt(0, y-exHead+4), faded(teal, 0.9))
	}
	heading("DESTINATION", path.Min.Y)
	p.RRect(path, 8, paint.Solid(faded(ink, 0.06)))
	widget.PaintIcon(p, f.Theme, icon.FolderOpen, geom.Rc(path.Min.X+10, path.Min.Y+10, 16, 16), faded(ink, 0.6))
	paintTail(p, b.dir, 12, geom.Pt(path.Min.X+34, path.Min.Y+10), path.Size().W-44, ink)
	heading("FORMAT", wavCard.Min.Y)
	for _, card := range []struct {
		r  geom.Rect
		on bool
	}{{wavCard, b.wav.Checked()}, {mp3Card, b.mp3.Checked()}} {
		p.RRect(card.r, 12, paint.Solid(faded(ink, 0.05)))
		if card.on {
			p.RRectStroke(card.r, 12, paint.Solid(faded(ink, 0)), paint.Stroke{Width: 1, Color: faded(teal, 0.5)})
		}
	}
	lame := "Encoded by LAME"
	if b.lameAt == "" {
		lame = "LAME is not found"
	}
	paintFit(p, lame, 11, false, geom.Pt(mp3Card.Min.X+14, mp3Card.Max.Y-36), mp3Card.Size().W-150, faded(ink, 0.55))
	heading("TRACKS", head.Min.Y)
	// The list's head: a tick for every track, how many are ticked, and
	// the columns' names.
	n, of := len(b.list.ticked()), len(b.list.tracks)
	paintTick(p, f, geom.Pt(head.Min.X+10, head.Min.Y+7), n == of, n > 0 && n < of)
	shaped(fmt.Sprintf("%d of %d", n, of), 12, true).Paint(p, geom.Pt(head.Min.X+38, head.Min.Y+7), ink)
	shaped("LENGTH", 9, true).Paint(p, geom.Pt(head.Max.X-exColLen, head.Min.Y+10), faded(ink, 0.45))
	shaped("LUFS", 9, true).Paint(p, geom.Pt(head.Max.X-exColLUFS, head.Min.Y+10), faded(ink, 0.45))
	p.RRect(list, 10, paint.Solid(faded(night, 0.5)))
	for k := range kids.All {
		k.Paint(p)
	}
	// The sum: the tracks, how long, as what, where.
	var total time.Duration
	for i, t := range b.list.tracks {
		if b.list.on[i] {
			total += t.Length
		}
	}
	var formats []string
	if b.wav.Checked() {
		w := bitsChoices[b.bits.Selected()].name
		if b.dither.Checked() && bitsChoices[b.bits.Selected()].bits != 32 {
			w += ", dithered"
		}
		formats = append(formats, "WAV "+w)
	}
	if rates := b.rate.Items(); b.mp3.Checked() && b.rate.Selected() < len(rates) {
		formats = append(formats, "MP3 "+rates[b.rate.Selected()].Label)
	}
	words := fmt.Sprintf("%d tracks · %s · %s", n, short(total), strings.Join(formats, " + "))
	paintFit(p, words, 12, false, geom.Pt(0, sum), box.W, faded(ink, 0.6))
}

// The list's columns, from its right.
const exColLen, exColLUFS = 150, 70

// exportList is the tracks to export, a row each, ticked or not.
type exportList struct {
	anim.Group
	tracks []ExportTrack
	on     []bool
	hot    int
	size   geom.Size
}

func newExportList(ts []ExportTrack) *exportList {
	l := &exportList{tracks: ts, on: make([]bool, len(ts)), hot: -1}
	for i, t := range ts {
		l.on[i] = t.Ticked
	}
	return l
}

func (l *exportList) ticked() []int {
	var ids []int
	for i, t := range l.tracks {
		if l.on[i] {
			ids = append(ids, t.ID)
		}
	}
	return ids
}

// Layout implements [gunim.Node].
func (l *exportList) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	l.size = geom.Sz(c.Max.W, float32(len(l.tracks))*exRowH)
	return l.size
}

// Focusable implements [gunim.Focusable].
func (l *exportList) Focusable() bool { return false }

// Handle implements [gunim.Handler]: a click on a row ticks it.
func (l *exportList) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		l.hot = int(e.Pos.Y / exRowH)
	case input.PointerLeave:
		l.hot = -1
	case input.PointerDown:
		i := int(e.Pos.Y / exRowH)
		if e.Button != input.ButtonPrimary || i < 0 || i >= len(l.on) {
			return false
		}
		l.on[i] = !l.on[i]
		u.Cue(gunim.CueTick, l)
	default:
		return false
	}
	u.Invalidate()
	return true
}

// Paint implements [gunim.Node].
func (l *exportList) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	for i, t := range l.tracks {
		y := float32(i) * exRowH
		if i == l.hot {
			p.RRect(geom.Rc(4, y+2, box.W-8, exRowH-4), 8, paint.Solid(faded(ink, 0.05)))
		}
		if i > 0 {
			p.RRect(geom.Rc(12, y, box.W-24, 1), 0, paint.Solid(faded(ink, 0.05)))
		}
		alpha := float32(1)
		if !l.on[i] {
			alpha = 0.45
		}
		paintTick(p, f, geom.Pt(10, y+8), l.on[i], false)
		shapedFace(fmt.Sprintf("%02d", t.Number), 12, false).Paint(p, geom.Pt(38, y+9), faded(ink, 0.5*alpha))
		// The title, then whether the track has a note, and how many
		// notes at times.
		room := box.W - 66 - exColLen - 12
		badges := float32(0)
		if strings.TrimSpace(t.Note) != "" {
			badges += 22
		}
		count := ""
		if len(t.Marks) > 0 {
			count = strconv.Itoa(len(t.Marks))
			badges += 26 + shapedFace(count, 11, true).Advance
		}
		title := min(shaped(t.Title, 13, false).Advance, room-badges)
		paintFit(p, t.Title, 13, false, geom.Pt(66, y+8), title, faded(ink, alpha))
		x := 66 + title + 8
		if strings.TrimSpace(t.Note) != "" {
			widget.PaintIcon(p, f.Theme, icon.MessageSquareText, geom.Rc(x, y+10, 14, 14), faded(amber, 0.9*alpha))
			x += 22
		}
		if count != "" {
			run := shapedFace(count, 11, true)
			pill := geom.Rc(x, y+8, 22+run.Advance, 18)
			p.RRect(pill, 9, paint.Solid(faded(amber, 0.16*alpha)))
			widget.PaintIcon(p, f.Theme, icon.Clock, geom.Rc(x+5, y+10, 13, 13), faded(amber, 0.9*alpha))
			run.Paint(p, geom.Pt(x+19, y+10), faded(amber, alpha))
		}
		shapedFace(clock(t.Length), 12, false).Paint(p, geom.Pt(box.W-exColLen, y+9), faded(ink, 0.6*alpha))
		lufs, c := "—", faded(ink, 0.4*alpha)
		if t.Measured {
			lufs = fmt.Sprintf("%.1f", t.LUFS)
			c = faded(loudnessColor(t.LUFS-t.Target), alpha)
			if t.Stale {
				c = faded(c, 0.5)
				lufs += "*"
			}
		}
		shapedFace(lufs, 12, true).Paint(p, geom.Pt(box.W-exColLUFS, y+9), c)
	}
}

// paintTick draws a tick box at at, 18 square: ticked, or with a dash
// for some ticked, or empty.
func paintTick(p *paint.Painter, f gunim.Frame, at geom.Point, on, some bool) {
	r := geom.Rc(at.X, at.Y, 18, 18)
	switch {
	case on:
		p.RRect(r, 5, paint.Solid(teal))
		widget.PaintIcon(p, f.Theme, icon.Check, r.Inset(geom.Uniform(3)), night)
	case some:
		p.RRect(r, 5, paint.Solid(teal))
		p.RRect(geom.Rc(at.X+4, at.Y+8, 10, 2), 1, paint.Solid(night))
	default:
		p.RRectStroke(r, 5, paint.Solid(faded(ink, 0)), paint.Stroke{Width: 1.5, Color: faded(ink, 0.4)})
	}
}

// paintTail draws s at size, its top left at at, cut short from the
// front with an ellipsis where it is wider than room: a path's end
// says most.
func paintTail(p *paint.Painter, s string, size float32, at geom.Point, room float32, c color.NRGBA) {
	rs := []rune(s)
	for n := range rs {
		t := string(rs[n:])
		if n > 0 {
			t = "…" + t
		}
		if run := shaped(t, size, false); run.Advance <= room {
			run.Paint(p, at, c)
			return
		}
	}
}
