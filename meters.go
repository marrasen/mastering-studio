package main

import (
	"fmt"
	"image/color"
	"math"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// The spectrum's view: frequencies from 20 Hz to 20 kHz, its levels
// tilted 4.5 dB an octave about 1 kHz, as studio analyzers show music,
// between specTop and specBottom.
const (
	specPoints          = 120
	specTilt            = 4.5
	specTop, specBottom = 0, -84
	// scopeFrames is how many of the last frames heard the vectorscope
	// draws.
	scopeFrames = 1200
)

// meters read the sound as it is heard, frame by frame: its loudness,
// as momentary, short-term and integrated since the track started, and
// its true peak; its stereo image, as a vectorscope and the
// correlation of its channels; and its spectrum. The listening level,
// and the gain that matches levels, are taken back out, so the
// readings are the track's own.
type meters struct {
	r    *root
	from int64
	buf  []float32
	// lm measures since the track started; tp its true peak; scope holds
	// the last frames heard, for the vectorscope.
	lm          *audio.LoudnessMeter
	tp          audio.TruePeakMeter
	scope       []float32
	corr        float32
	moment      float32
	shortTerm   float32
	freqs, spec []float32
	smooth      []float32
	bands       [3]float32
	// low and high are where the loudness has ranged since the track
	// started, read every half second, as ranged says it has.
	low, high float32
	ranged    bool
	rangedAt  time.Duration
	// gram is the spectrogram, shown in the spectrum's place while
	// showGram says; gramAt counts toward its next column, and
	// column holds the levels of one.
	gram     spectrogram
	showGram bool
	gramAt   time.Duration
	column   []float32
	// specHead is where the spectrum's heading is, which switches it.
	specHead geom.Rect
	size     geom.Size
}

func newMeters(r *root) *meters {
	m := &meters{r: r, lm: audio.NewLoudnessMeter(audio.SampleRate), moment: -70, shortTerm: -70}
	m.freqs = make([]float32, specPoints)
	m.spec, m.smooth = make([]float32, specPoints), make([]float32, specPoints)
	for i := range m.freqs {
		m.freqs[i] = float32(20 * math.Pow(1000, float64(i)/(specPoints-1)))
		m.smooth[i] = specBottom
	}
	return m
}

// show starts the integrated reading over as a track starts or another
// is picked.
func (m *meters) show(was, s Album) {
	if s.Starts != was.Starts || s.Current != was.Current {
		m.lm = audio.NewLoudnessMeter(audio.SampleRate)
		m.tp = audio.TruePeakMeter{}
		m.ranged = false
	}
}

// listening returns the gain, in decibels, the track is heard at over
// its own level: the volume, and the level match.
func (m *meters) listening() float64 {
	s := m.r.state
	g := dB(float64(s.Volume))
	if t, ok := m.r.track(); ok && s.Match && t.Measured && t.Measure.Loud {
		g += float64(max(-24, min(s.Target-t.Measure.LUFS, 24)))
	}
	return g
}

// Step implements [gunim.Animator]: each frame the meters read what has
// been heard since the last.
func (m *meters) Step(dt time.Duration) bool {
	var now int64
	m.buf, now = m.r.d.heard(m.buf[:0], m.from)
	m.from = now
	if len(m.buf) > 0 {
		// The listening level taken out, so the meters read the track.
		back := float32(math.Pow(10, -m.listening()/20))
		for i := range m.buf {
			m.buf[i] *= back
		}
		m.lm.Write(m.buf)
		m.tp.Write(m.buf)
		m.scope = append(m.scope, m.buf...)
		if over := len(m.scope) - 2*scopeFrames; over > 0 {
			m.scope = m.scope[over:]
		}
	}
	sec := float32(dt.Seconds())
	ease := func(v *float32, to, rise, fall float32) {
		rate := rise
		if to < *v {
			rate = fall
		}
		*v += (to - *v) * min(1, rate*sec)
	}
	playing := m.r.state.Playing
	momentary, short := float32(-70), float32(-70)
	if playing {
		momentary = float32(max(m.lm.Momentary(), -70))
		short = float32(max(m.lm.ShortTerm(), -70))
	}
	ease(&m.moment, momentary, 20, 4)
	ease(&m.shortTerm, short, 10, 3)
	// The range, read now and then: it changes slowly, and is read over
	// all the track heard.
	if m.rangedAt += dt; m.rangedAt > 500*time.Millisecond {
		m.rangedAt = 0
		if low, high, ok := audio.LoudnessRange(m.lm.ShortTerms()); ok {
			m.ranged = true
			m.low, m.high = float32(low), float32(high)
		}
	}
	// The correlation of the channels, over the frames drawn.
	var lr, ll, rr float64
	for i := 0; i+1 < len(m.scope); i += 2 {
		l, r := float64(m.scope[i]), float64(m.scope[i+1])
		lr += l * r
		ll += l * l
		rr += r * r
	}
	c := float32(1)
	if ll > 1e-9 && rr > 1e-9 {
		c = float32(lr / math.Sqrt(ll*rr))
	}
	ease(&m.corr, c, 8, 8)
	if playing {
		m.r.d.spectrum(m.freqs, m.spec)
	} else {
		for i := range m.spec {
			m.spec[i] = specBottom - 30
		}
	}
	back := float32(-m.listening())
	settled := true
	// The spectrogram takes a column 40 times a second, while the sound
	// plays.
	m.gramAt += dt
	takeColumn := playing && m.gramAt >= 25*time.Millisecond
	if takeColumn {
		m.gramAt = 0
		m.column = m.column[:0]
	}
	for i, f := range m.freqs {
		tilt := float32(specTilt * math.Log2(float64(f)/1000))
		to := m.spec[i] + tilt + back
		if takeColumn {
			m.column = append(m.column, gramLevel(to))
		}
		ease(&m.smooth[i], to, 25, 5)
		settled = settled && math.Abs(float64(m.smooth[i]-to)) < 0.5
	}
	if takeColumn {
		m.gram.push(m.column)
	}
	// Three bands of the spectrum, for the little bars of the track
	// playing.
	for k, r := range [3][2]int{{2, 30}, {30, 80}, {80, 119}} {
		var v float32
		for i := r[0]; i < r[1]; i++ {
			v = max(v, (m.smooth[i]-specBottom)/(specTop-specBottom))
		}
		m.bands[k] = min(max(v, 0), 1)
	}
	return playing || !settled || m.moment > -69
}

// Layout implements [gunim.Node].
func (m *meters) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	m.size = c.Max
	return c.Max
}

// Paint implements [gunim.Node]: loudness at the top, the stereo image
// in the middle, the spectrum at the foot.
func (m *meters) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, _ gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(panel))
	p.RRect(geom.Rc(0, 0, 1, box.H), 0, paint.Solid(faded(ink, 0.06)))
	y := float32(16)
	y = m.paintLoudness(p, box, y)
	y = m.paintScope(p, box, y+18)
	m.paintSpectrum(p, geom.Rc(16, y+18, box.W-32, box.H-y-34))
}

// lufsText writes a loudness, a dash for none.
func lufsText(v float32) string {
	if v <= -69 {
		return "—"
	}
	return fmt.Sprintf("%.1f", v)
}

func (m *meters) paintLoudness(p *paint.Painter, box geom.Size, y float32) float32 {
	target := m.r.state.Target
	shaped("LOUDNESS", 10, true).Paint(p, geom.Pt(16, y), faded(teal, 0.85))
	shaped(fmt.Sprintf("target %.1f LUFS", target), 10, false).Paint(p, geom.Pt(100, y), faded(ink, 0.4))
	y += 20
	integ := float32(-70)
	if v, ok := m.lm.Integrated(); ok {
		integ = float32(v)
	}
	big := shapedFace(lufsText(m.shortTerm), 34, true, true)
	c := ink
	if m.shortTerm > -69 {
		c = loudnessColor(m.shortTerm - target)
	}
	big.Paint(p, geom.Pt(16, y), c)
	shaped("LUFS short-term", 10, false).Paint(p, geom.Pt(20+big.Advance, y+22), faded(ink, 0.45))
	y += 50
	for _, row := range []struct {
		name string
		v    float32
	}{{"M", m.moment}, {"S", m.shortTerm}, {"I", integ}} {
		m.paintBar(p, geom.Rc(40, y, box.W-110, 10), row.v, target)
		shaped(row.name, 11, true).Paint(p, geom.Pt(16, y-2), faded(ink, 0.6))
		shapedFace(lufsText(row.v), 11, false, true).Paint(p, geom.Pt(box.W-62, y-2), faded(ink, 0.8))
		y += 22
	}
	// The range, as a band on the same scale, from its low to its high.
	shaped("LRA", 9, true).Paint(p, geom.Pt(14, y), faded(ink, 0.6))
	m.paintRange(p, geom.Rc(40, y, box.W-110, 10))
	words := "—"
	if m.ranged {
		words = fmt.Sprintf("%.1f LU", m.high-m.low)
	}
	shapedFace(words, 11, false, true).Paint(p, geom.Pt(box.W-62, y-2), faded(ink, 0.8))
	y += 22
	tp := float32(dB(m.tp.Peak()))
	tpColor := faded(ink, 0.8)
	if tp > -1 {
		tpColor = coral
	}
	shaped("TP", 11, true).Paint(p, geom.Pt(16, y), faded(ink, 0.6))
	tpWords := "—"
	if m.tp.Peak() > 0 {
		tpWords = fmt.Sprintf("%.1f dBTP", tp)
	}
	shapedFace(tpWords, 11, false, true).Paint(p, geom.Pt(40, y), tpColor)
	return y + 18
}

// paintRange draws where the loudness has ranged as a band on the
// bars' scale, from -36 to 0 LUFS.
func (m *meters) paintRange(p *paint.Painter, bar geom.Rect) {
	const lo, hi = -36, 0
	at := func(l float32) float32 { return bar.Min.X + bar.Size().W*min(max((l-lo)/(hi-lo), 0), 1) }
	p.RRect(bar, 5, paint.Solid(faded(ink, 0.08)))
	if !m.ranged {
		return
	}
	x0, x1 := at(m.low), at(m.high)
	band := geom.Rc(x0, bar.Min.Y, max(x1-x0, 3), bar.Size().H)
	p.ShadowRRect(band, 5, paint.Solid(faded(sky, 0.85)), paint.Shadow{Blur: 8, Color: faded(sky, 0.4)})
}

// paintBar draws a loudness from -36 to 0 LUFS as a bar, the target
// marked.
func (m *meters) paintBar(p *paint.Painter, bar geom.Rect, v, target float32) {
	const lo, hi = -36, 0
	at := func(l float32) float32 { return bar.Min.X + bar.Size().W*min(max((l-lo)/(hi-lo), 0), 1) }
	p.RRect(bar, 5, paint.Solid(faded(ink, 0.08)))
	if v > lo {
		fill := bar
		fill.Max.X = at(v)
		p.RRect(fill, 5, paint.Solid(loudnessColor(v-target)))
	}
	tx := at(target)
	p.RRect(geom.Rc(tx-1, bar.Min.Y-3, 2, bar.Size().H+6), 1, paint.Solid(ink))
}

// paintScope draws the vectorscope, mid up and side across, the last
// frames heard as points, brighter the newer; and the correlation bar
// under it.
func (m *meters) paintScope(p *paint.Painter, box geom.Size, y float32) float32 {
	shaped("STEREO", 10, true).Paint(p, geom.Pt(16, y), faded(teal, 0.85))
	y += 20
	side := min(box.W-32, 220)
	area := geom.Rc((box.W-side)/2, y, side, side)
	mid := area.Min.Add(geom.Pt(side/2, side/2))
	rad := side / 2
	p.RRect(area, rad, paint.Solid(faded(night, 0.6)))
	// The guides: mid up, side across, and each channel alone on its
	// diagonal.
	p.RRect(geom.Rc(mid.X-0.5, area.Min.Y+8, 1, side-16), 0, paint.Solid(faded(ink, 0.1)))
	p.RRect(geom.Rc(area.Min.X+8, mid.Y-0.5, side-16, 1), 0, paint.Solid(faded(ink, 0.06)))
	d := rad * 0.68
	segment(p, geom.Pt(mid.X-d, mid.Y-d), geom.Pt(mid.X+d, mid.Y+d), 1, faded(ink, 0.06))
	segment(p, geom.Pt(mid.X+d, mid.Y-d), geom.Pt(mid.X-d, mid.Y+d), 1, faded(ink, 0.06))
	shaped("L", 9, true).Paint(p, geom.Pt(mid.X-d-10, mid.Y-d-10), faded(ink, 0.3))
	shaped("R", 9, true).Paint(p, geom.Pt(mid.X+d+4, mid.Y-d-10), faded(ink, 0.3))
	n := len(m.scope) / 2
	k := rad * 0.9 / math.Sqrt2
	for i := 0; i < n; i += 2 {
		l, r := m.scope[2*i], m.scope[2*i+1]
		x := mid.X + (r-l)*k
		yy := mid.Y - (l+r)*k
		if math.Hypot(float64(x-mid.X), float64(yy-mid.Y)) > float64(rad) {
			continue
		}
		a := 0.15 + 0.6*float32(i)/float32(n)
		p.RRect(geom.Rc(x-0.9, yy-0.9, 1.8, 1.8), 0.9, paint.Solid(faded(teal, a)))
	}
	y += side + 12
	// The correlation, from -1, out of phase, to +1, mono.
	bar := geom.Rc(40, y, box.W-80, 8)
	p.RRect(bar, 4, paint.Solid(faded(ink, 0.08)))
	p.RRect(geom.Rc(bar.Min.X+bar.Size().W/2-0.5, y-3, 1, 14), 0, paint.Solid(faded(ink, 0.3)))
	cx := bar.Min.X + bar.Size().W*(m.corr+1)/2
	c := corrColor(m.corr)
	p.ShadowRRect(geom.Rc(cx-5, y-3, 10, 14), 4, paint.Solid(c), paint.Shadow{Blur: 8, Color: faded(c, 0.5)})
	shapedFace("-1", 9, false, true).Paint(p, geom.Pt(18, y-2), faded(ink, 0.4))
	shapedFace("+1", 9, false, true).Paint(p, geom.Pt(box.W-34, y-2), faded(ink, 0.4))
	return y + 14
}

// corrColor colours a correlation: teal as the channels agree, amber as
// they part, coral as they work against each other, which a mono
// listener loses.
func corrColor(c float32) color.NRGBA {
	switch {
	case c >= 0.3:
		return teal
	case c >= 0:
		return mix(amber, teal, c/0.3)
	}
	return mix(amber, coral, min(-c/0.5, 1))
}

// paintSpectrum draws the spectrum heard into area.
func (m *meters) paintSpectrum(p *paint.Painter, area geom.Rect) {
	if area.Size().H < 40 {
		return
	}
	// The heading names both views, the one shown lit; a click on it
	// switches.
	head := area.Min.Sub(geom.Pt(0, 2))
	spec, gram := shaped("SPECTRUM", 10, true), shaped("SPECTROGRAM", 10, true)
	on, off := faded(teal, 0.85), faded(ink, 0.35)
	if m.showGram {
		on, off = off, on
	}
	spec.Paint(p, head, on)
	gram.Paint(p, head.Add(geom.Pt(spec.Advance+12, 0)), off)
	m.specHead = geom.Rc(head.X-4, head.Y-4, spec.Advance+gram.Advance+20, 20)
	area.Min.Y += 18
	if m.showGram {
		p.RRect(area, 10, paint.Solid(night))
		m.gram.paint(p, area, 1)
		m.paintPitches(p, area, true)
		return
	}
	p.RRect(area, 10, paint.Solid(faded(night, 0.6)))
	w := area.Size().W / float32(len(m.smooth))
	var prev geom.Point
	for i, v := range m.smooth {
		t := min(max((v-specBottom)/(specTop-specBottom), 0), 1)
		h := area.Size().H * t
		x := area.Min.X + float32(i)*w
		p.RRect(geom.Rc(x, area.Max.Y-h, w+0.5, h), 0, paint.Solid(faded(teal, 0.22)))
		pt := geom.Pt(x+w/2, area.Max.Y-h)
		if i > 0 {
			segment(p, prev, pt, 1.4, faded(teal, 0.8))
		}
		prev = pt
	}
	m.paintPitches(p, area, false)
}

// paintBars draws three little bars moving with the music, low, middle
// and high, centred on mid, in c.
func paintBars(p *paint.Painter, m *meters, mid geom.Point, c color.NRGBA) {
	for i, v := range m.bands {
		h := 4 + 14*v
		x := mid.X - 9 + float32(i)*7
		p.RRect(geom.Rc(x, mid.Y+9-h, 4, h), 2, paint.Solid(c))
	}
}

// paintPitches marks 100 Hz, 1 kHz and 10 kHz along the spectrum, or,
// up, along the spectrogram.
func (m *meters) paintPitches(p *paint.Painter, area geom.Rect, up bool) {
	for _, hz := range []float64{100, 1000, 10000} {
		at := float32(math.Log(hz/20) / math.Log(1000))
		label := fmt.Sprintf("%.0f", hz)
		if hz >= 1000 {
			label = fmt.Sprintf("%.0fk", hz/1000)
		}
		if up {
			y := area.Max.Y - area.Size().H*at
			p.RRect(geom.Rc(area.Min.X, y, area.Size().W, 1), 0, paint.Solid(faded(ink, 0.08)))
			shaped(label, 9, false).Paint(p, geom.Pt(area.Min.X+4, y-12), faded(ink, 0.45))
			continue
		}
		x := area.Min.X + area.Size().W*at
		p.RRect(geom.Rc(x, area.Min.Y, 1, area.Size().H), 0, paint.Solid(faded(ink, 0.06)))
		shaped(label, 9, false).Paint(p, geom.Pt(x+3, area.Max.Y-14), faded(ink, 0.35))
	}
}

// Handle implements [gunim.Handler]: a click on the spectrum's heading
// switches it to the spectrogram and back.
func (m *meters) Handle(e input.Event, u *gunim.UI) bool {
	d, ok := e.(input.PointerDown)
	if !ok || d.Button != input.ButtonPrimary || !m.specHead.Contains(d.Pos) {
		return false
	}
	m.showGram = !m.showGram
	u.Cue(gunim.CueTick, m)
	u.Invalidate()
	return true
}
