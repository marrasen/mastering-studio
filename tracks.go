package main

import (
	"fmt"
	"image/color"
	"math"
	"path/filepath"
	"strconv"
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

// rowH is the height of a track's row.
const rowH = 74

// rowLook is a row's animated state, by its track's ID: where it is,
// how lit, how picked by session A and by B, and its readings as they
// count to new ones.
type rowLook struct {
	y, lit, in       *anim.Float
	pickedA, pickedB *anim.Float
	lufs, peak       *anim.Float
	progress         *anim.Float
	gone             bool
	thumb            []float32
	thumbOf          *audioui.Wave
}

// trackList is the album's tracks, or the references, a row each, in
// order: its number, title and length, its waveform small, its loudness
// against the target and its true peak, and its export's progress. A
// row picked plays; a row dragged moves; files dropped on the list join
// it.
type trackList struct {
	anim.Group
	r *root
	// refs says the list is of the references.
	refs  bool
	rows  map[int]*rowLook
	order []int
	hot   int
	down  int
	// moving is the track dragged to a new place, from where it was
	// pressed: grab is how far down its row, and y where the pointer is.
	moving  int
	press   geom.Point
	grab, y float32
	spin    float64
	menu    *widget.ContextMenu
	size    geom.Size
}

func newTrackList(r *root, refs bool) *trackList {
	return &trackList{r: r, refs: refs, rows: map[int]*rowLook{}, down: -1}
}

// list returns the tracks of s the list shows.
func (l *trackList) list(s *Album) []Track {
	if l.refs {
		return s.References
	}
	return s.Tracks
}

func (l *trackList) look(id int) *rowLook {
	if lk := l.rows[id]; lk != nil {
		return lk
	}
	lk := &rowLook{y: anim.NewFloat(float32(len(l.order)) * rowH), lit: anim.NewFloat(0), pickedA: anim.NewFloat(0),
		pickedB: anim.NewFloat(0), in: anim.NewFloat(0), lufs: anim.NewFloat(0), peak: anim.NewFloat(0),
		progress: anim.NewFloat(0)}
	lk.in.Animate(1, anim.Spring{Response: 0.4, Damping: 0.8})
	l.Add(lk.y, lk.lit, lk.pickedA, lk.pickedB, lk.in, lk.lufs, lk.peak, lk.progress)
	l.rows[id] = lk
	return lk
}

func (l *trackList) show(s Album, u *gunim.UI) {
	// Each session's track, lit in its colour: fully while it is heard,
	// half while the other is.
	a, b := s.Current, s.Away.Current
	if s.Side == SideB {
		a, b = b, a
	}
	live := map[int]bool{}
	l.order = l.order[:0]
	for i, t := range l.list(&s) {
		live[t.ID] = true
		l.order = append(l.order, t.ID)
		lk := l.look(t.ID)
		lk.gone = false
		if t.ID != l.moving {
			lk.y.Animate(float32(i)*rowH, anim.Spring{Response: 0.35, Damping: 0.85})
		}
		lk.pickedA.Animate(onOff(t.ID == a)*(1-0.5*float32(s.Side)), anim.Snappy)
		lk.pickedB.Animate(onOff(t.ID == b)*(0.5+0.5*float32(s.Side)), anim.Snappy)
		if t.Measured {
			if lk.lufs.Value() == 0 {
				lk.lufs.Jump(t.Measure.LUFS)
				lk.peak.Jump(float32(dB(float64(t.Measure.TruePeak))))
			}
			lk.lufs.Animate(t.Measure.LUFS, anim.Spring{Response: 0.5, Damping: 1})
			lk.peak.Animate(float32(dB(float64(t.Measure.TruePeak))), anim.Spring{Response: 0.5, Damping: 1})
		}
		lk.progress.Animate(t.Progress, anim.Snappy)
		if t.Wave != lk.thumbOf && t.Wave != nil {
			lk.thumb, lk.thumbOf = t.Wave.Thumbnail(64), t.Wave
		}
	}
	for id, lk := range l.rows {
		if !live[id] && !lk.gone {
			lk.gone = true
			lk.in.Animate(0, anim.Spring{Response: 0.3, Damping: 1})
		}
	}
	u.Invalidate()
}

// Step implements [gunim.Animator]: rows gone drop away once faded, and
// a spinner turns while a track is measured.
func (l *trackList) Step(dt time.Duration) bool {
	moving := l.Group.Step(dt)
	for id, lk := range l.rows {
		if lk.gone && lk.in.Value() < 0.01 && !lk.in.Active() {
			l.Remove(lk.y, lk.lit, lk.pickedA, lk.pickedB, lk.in, lk.lufs, lk.peak, lk.progress)
			delete(l.rows, id)
		}
	}
	measuring := false
	for _, t := range l.list(&l.r.state) {
		measuring = measuring || t.Measuring || !t.Scanned || t.Progress > 0
	}
	if measuring {
		l.spin += dt.Seconds() * 5
	}
	return moving || measuring || l.r.state.Playing
}

// rowAt returns the place of the row at p, or -1.
func (l *trackList) rowAt(p geom.Point) int {
	if p.X < 0 || p.X > l.size.W || p.Y < 0 {
		return -1
	}
	i := int(p.Y / rowH)
	if i >= len(l.order) {
		return -1
	}
	return i
}

func (l *trackList) hover(i int) {
	id := 0
	if i >= 0 {
		id = l.order[i]
	}
	if id == l.hot {
		return
	}
	if lk := l.rows[l.hot]; lk != nil {
		lk.lit.Animate(0, anim.Gentle)
	}
	l.hot = id
	if lk := l.rows[id]; lk != nil {
		lk.lit.Animate(1, anim.Snappy)
	}
}

// Handle implements [gunim.Handler]: a click picks a track, a drag moves
// it, and the secondary button opens its menu.
func (l *trackList) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		if l.moving != 0 {
			l.y = e.Pos.Y
			l.rows[l.moving].y.Jump(l.y - l.grab)
			l.makeWay()
			break
		}
		l.hover(l.rowAt(e.Pos))
		if i := l.down; i >= 0 && i < len(l.order) && !e.Touch {
			if d := e.Pos.Sub(l.press); d.Y*d.Y > 64 {
				l.moving = l.order[i]
				l.grab = l.press.Y - float32(i)*rowH
				l.y = e.Pos.Y
			}
		}
	case input.PointerLeave:
		l.hover(-1)
	case input.PointerDown:
		switch e.Button {
		case input.ButtonPrimary:
			l.down, l.press = l.rowAt(e.Pos), e.Pos
		case input.ButtonSecondary:
			return l.openMenu(e.Pos, u)
		default:
			return false
		}
	case input.PointerUp:
		switch {
		case l.moving != 0:
			from := indexOf(l.list(&l.r.state), l.moving)
			to := max(0, min(int((l.y-l.grab+rowH/2)/rowH), len(l.order)-1))
			l.moving = 0
			if from >= 0 && from != to {
				u.Cue(gunim.CueTick, l)
				u.Send(l, MoveTrack{From: from, To: to, Refs: l.refs})
			} else {
				l.show(l.r.state, u)
			}
		case l.down >= 0 && l.rowAt(e.Pos) == l.down:
			u.Cue(gunim.CueSelect, l)
			u.Send(l, Pick{ID: l.order[l.down]})
		}
		l.down = -1
	default:
		return false
	}
	u.Invalidate()
	return true
}

func indexOf(ts []Track, id int) int {
	for i, t := range ts {
		if t.ID == id {
			return i
		}
	}
	return -1
}

// makeWay moves the rows between a track dragged and where it would
// land a place, to make way.
func (l *trackList) makeWay() {
	from := indexOf(l.list(&l.r.state), l.moving)
	to := max(0, min(int((l.y-l.grab+rowH/2)/rowH), len(l.order)-1))
	for i, id := range l.order {
		if id == l.moving {
			continue
		}
		at := i
		switch {
		case from < i && i <= to:
			at = i - 1
		case to <= i && i < from:
			at = i + 1
		}
		l.rows[id].y.Animate(float32(at)*rowH, anim.Spring{Response: 0.25, Damping: 0.85})
	}
}

// openMenu opens the menu of the track at p.
func (l *trackList) openMenu(p geom.Point, u *gunim.UI) bool {
	i := l.rowAt(p)
	if i < 0 || l.menu == nil {
		return false
	}
	id := l.order[i]
	// The track's file heads the menu, its folder after it.
	var file string
	if t := l.r.state.find(id); t != nil {
		file = t.File
	}
	l.menu.Items = []string{filepath.Base(file), "Rename", "Replace file…", "Show file in folder", "Export this track",
		"Remove from the album"}
	l.menu.Icons = []*icon.Icon{nil, icon.Pencil, icon.FileAudio, icon.FolderOpen, icon.Download, icon.Trash2}
	l.menu.Hints = []string{lastDirs(filepath.Dir(file)), "", "", "", "", ""}
	l.menu.Captions = []int{0}
	l.menu.Breaks = []int{1, 4, 5}
	l.menu.Disabled = nil
	if l.refs {
		// A reference is not exported.
		l.menu.Items[5] = "Remove reference"
		l.menu.Disabled = []bool{4: true, 5: false}
	}
	l.menu.Picked = func(k int, u *gunim.UI) {
		switch k - 1 {
		case 0:
			// The title is renamed where the editor shows it.
			if id != l.r.state.Current {
				u.Send(l, Pick{ID: id})
			}
			l.r.head.renameTrack(id, u)
		case 1:
			u.Send(l, ChooseReplacement{ID: id})
		case 2:
			u.Send(l, ShowFile{ID: id})
		case 3:
			u.Send(l, OpenExport{IDs: []int{id}})
		case 4:
			u.Send(l, RemoveTrack{ID: id})
		}
	}
	l.menu.Open(p, u)
	return true
}

// Layout implements [gunim.Node]: as tall as its rows, and the list's
// view at least.
func (l *trackList) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	l.size = c.Constrain(geom.Sz(c.Max.W, max(float32(len(l.order))*rowH+24, c.Min.H)))
	return l.size
}

// Paint implements [gunim.Node].
func (l *trackList) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	pal := colours(f.Theme)
	if len(l.order) == 0 {
		lines := []string{"Drop your mixes here", "WAV, FLAC, MP3 or Ogg, or a folder of them", "or press Add tracks"}
		y := float32(120)
		if l.refs {
			lines, y = []string{"Drop reference tracks here", "to compare the album with"}, 14
		}
		for i, s := range lines {
			size, alpha := float32(13), float32(0.5)
			if i == 0 {
				size, alpha = 17, 0.85
			}
			run := shaped(s, size, i == 0)
			run.Paint(p, geom.Pt((box.W-run.Advance)/2, y), faded(pal.ink, alpha))
			y += size + 12
		}
		return
	}
	for id, lk := range l.rows {
		if lk.gone {
			l.paintRow(p, f, id, lk, box)
		}
	}
	for i, id := range l.order {
		if id != l.moving {
			l.paintRow(p, f, id, l.rows[id], box)
		}
		_ = i
	}
	if l.moving != 0 {
		l.paintRow(p, f, l.moving, l.rows[l.moving], box)
	}
}

// paintRow draws track id's row.
func (l *trackList) paintRow(p *paint.Painter, f gunim.Frame, id int, lk *rowLook, box geom.Size) {
	pal := colours(f.Theme)
	s := l.r.state
	t, ok := Track{}, false
	number := 0
	for i, tr := range l.list(&s) {
		if tr.ID == id {
			t, ok, number = tr, true, i+1
		}
	}
	if !ok {
		return
	}
	in := min(max(lk.in.Value(), 0), 1)
	if in < 0.01 {
		return
	}
	y := lk.y.Value()
	row := geom.Rc(10, y+4, box.W-20, rowH-8)
	mid := row.Min.Add(geom.Pt(row.Size().W/2, row.Size().H/2))
	defer p.Push(paint.Scale(0.92+0.08*in, mid))()
	// Lit in the colour of the session that has it, the one heard over
	// the other.
	picked, tint := lk.pickedA.Value(), pal.teal
	if b := lk.pickedB.Value(); b > picked {
		picked, tint = b, pal.sky
	}
	if id == l.moving {
		p.ShadowRRect(row, 14, paint.Solid(pal.raised), paint.Shadow{Blur: 18, Offset: geom.Pt(0, 6), Color: faded(pal.night, 0.7)})
	}
	p.RRect(row, 14, paint.Solid(faded(mix(pal.raised, tint, 0.14*picked), (0.55+0.45*picked)*in)))
	pal.outline(p, row, 14, in)
	if lit := lk.lit.Value(); lit > 0.01 {
		p.RRect(row, 14, paint.Solid(faded(pal.ink, 0.05*lit*in)))
	}
	if picked > 0.01 {
		p.RRectStroke(row, 14, paint.Solid(color.NRGBA{}), paint.Stroke{Width: 1.5, Color: faded(tint, 0.8*picked*in)})
	}
	// The number, in a ring that spins while the track is read.
	ring := geom.Rc(row.Min.X+12, mid.Y-15, 30, 30)
	numColor := mix(pal.quiet(0.7), tint, picked)
	if !t.Scanned || t.Measuring {
		l.paintSpin(p, ring, faded(pal.teal, 0.8*in))
	} else {
		p.RRectStroke(ring, 15, paint.Solid(color.NRGBA{}), paint.Stroke{Width: 1.5, Color: faded(numColor, 0.5*in)})
	}
	if t.ID == s.Current && s.Playing {
		// The track playing shows bars moving with it in its ring.
		audioui.PaintBars(p, l.r.meters.bands[:], geom.Pt(ring.Min.X+17, ring.Min.Y+15), faded(tint, in))
	} else {
		label := strconv.Itoa(number)
		if l.refs {
			label = "R" + label
		}
		run := shaped(label, 13, true)
		run.Paint(p, geom.Pt(ring.Min.X+(30-run.Advance)/2, ring.Min.Y+7), faded(numColor, in))
	}
	textX := ring.Max.X + 12
	// After the title, whether a note waits on the track, and how many
	// notes at times it has.
	room := row.Max.X - textX - 92
	badges := float32(0)
	if t.Note != "" {
		badges += 20
	}
	count := ""
	if len(t.Marks) > 0 {
		count = strconv.Itoa(len(t.Marks))
		badges += 26 + shapedFace(count, 10, true).Advance
	}
	title := min(shaped(t.Title, 14, true).Advance, room-badges)
	paintFit(p, t.Title, 14, true, geom.Pt(textX, row.Min.Y+10), title, faded(pal.ink, 0.92*in))
	at := textX + title + 6
	if t.Note != "" {
		widget.PaintIcon(p, f.Theme, icon.MessageSquareText, geom.Rc(at, row.Min.Y+11, 14, 14), faded(pal.amber, 0.9*in))
		at += 20
	}
	if count != "" {
		run := shapedFace(count, 10, true)
		pill := geom.Rc(at, row.Min.Y+10, 20+run.Advance, 16)
		p.RRect(pill, 8, paint.Solid(faded(pal.amber, 0.16*in)))
		widget.PaintIcon(p, f.Theme, icon.Clock, geom.Rc(at+4, row.Min.Y+12, 12, 12), faded(pal.amber, 0.9*in))
		run.Paint(p, geom.Pt(at+17, row.Min.Y+12), faded(pal.amber, in))
	}
	// How long it exports, the silence before it and all.
	if t.Scanned {
		shapedFace(clock(lengthOf(t, s.gapOf(&t))), 10, false).Paint(p, geom.Pt(textX, row.Min.Y+30),
			faded(pal.quiet(0.55), in))
	}
	// The waveform, small, under the title.
	if lk.thumb != nil {
		w := row.Max.X - textX - 96
		cw := w / float32(len(lk.thumb))
		base := row.Min.Y + 48
		for i, v := range lk.thumb {
			h := max(1, 16*min(float32(math.Sqrt(float64(v)))*1.4, 1))
			p.RRect(geom.Rc(textX+float32(i)*cw, base-h/2, max(cw-1, 1), h), 0.5,
				paint.Solid(faded(mix(pal.ink, tint, picked), 0.35*in)))
		}
	}
	// The readings, right: loudness against the target, and true peak.
	right := row.Max.X - 12
	switch {
	case t.Progress > 0:
		l.paintProgress(p, pal, geom.Rc(right-76, mid.Y-4, 76, 8), lk.progress.Value(), in)
	case t.Measured && t.Measure.Loud:
		off := t.Measure.LUFS - s.Target
		c := loudnessColor(f.Theme, off)
		lufs := shapedFace(fmt.Sprintf("%.1f", lk.lufs.Value()), 15, true)
		alpha := in
		if t.Stale {
			// Changed since: the reading is old, faint, with a dot by
			// it, until Measure loudness measures it again.
			alpha *= 0.4
			p.RRect(geom.Rc(right-lufs.Advance-11, row.Min.Y+15, 6, 6), 3, paint.Solid(faded(pal.amber, in)))
		}
		lufs.Paint(p, geom.Pt(right-lufs.Advance, row.Min.Y+9), faded(c, alpha))
		tp := lk.peak.Value()
		sub := fmt.Sprintf("%+.1f · TP %.1f", off, tp)
		tpColor := faded(pal.quiet(0.5), in)
		if tp > -1 {
			tpColor = faded(pal.coral, in)
		}
		subRun := shapedFace(sub, 10, false)
		subRun.Paint(p, geom.Pt(right-subRun.Advance, row.Min.Y+32), tpColor)
		lraX := right
		if t.Exported != "" {
			widget.PaintIcon(p, f.Theme, icon.Check, geom.Rc(right-14, row.Min.Y+46, 14, 14), faded(pal.teal, in))
			lraX -= 20
		}
		if t.Measure.Ranged {
			lra := shapedFace(fmt.Sprintf("LRA %.1f", t.Measure.LRA), 10, false)
			lra.Paint(p, geom.Pt(lraX-lra.Advance, row.Min.Y+47), faded(pal.sky, 0.75*in))
		}
	case t.Measured:
		run := shaped("silent", 12, false)
		run.Paint(p, geom.Pt(right-run.Advance, row.Min.Y+12), faded(pal.quiet(0.4), in))
	}
}

// paintSpin draws an arc turning round ring, for a track being read.
func (l *trackList) paintSpin(p *paint.Painter, ring geom.Rect, c color.NRGBA) {
	mid := ring.Min.Add(geom.Pt(ring.Size().W/2, ring.Size().H/2))
	r := ring.Size().W / 2
	for i := range 10 {
		a := l.spin + float64(i)*0.22
		pt := geom.Pt(mid.X+r*float32(math.Cos(a)), mid.Y+r*float32(math.Sin(a)))
		d := 1.2 + 0.18*float32(i)
		p.RRect(geom.Rc(pt.X-d, pt.Y-d, 2*d, 2*d), d, paint.Solid(faded(c, float32(i+1)/10)))
	}
}

// paintProgress draws an export's bar, filled to v.
func (l *trackList) paintProgress(p *paint.Painter, pal palette, bar geom.Rect, v, alpha float32) {
	p.RRect(bar, 4, paint.Solid(faded(pal.ink, 0.1*alpha)))
	fill := bar
	fill.Max.X = bar.Min.X + bar.Size().W*min(max(v, 0), 1)
	p.ShadowRRect(fill, 4, paint.Solid(faded(pal.teal, alpha)), paint.Shadow{Blur: 8, Color: faded(pal.teal, 0.5*alpha)})
}
