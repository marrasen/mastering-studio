package main

import (
	"fmt"
	"math"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// specPoints is how many frequencies the spectrum is drawn at.
const specPoints = 120

type (
	// SpectrumView is how the meters show the sound's spectrum: as a
	// spectrogram, or as a spectrum, its output or input line hidden.
	// Kept across runs, its zero is the spectrum with both lines.
	SpectrumView struct {
		Gram    bool `json:",omitempty"`
		HideOut bool `json:",omitempty"`
		HideIn  bool `json:",omitempty"`
	}
	// SetSpectrum sets how the spectrum is shown.
	SetSpectrum struct{ View SpectrumView }
)

// firstSpectrum is how the spectrum is shown the first time the program
// runs: the output alone.
var firstSpectrum = SpectrumView{HideIn: true}

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
	// rate is the rate the sound is heard at, the mixer's.
	rate int
	loud *audioui.Loudness
	// scope is the stereo image.
	scope *audioui.Scope
	// spec and specIn are the spectrum out of the chain and into it, in
	// decibels, as last taken, and spectrum the two as drawn.
	spec, specIn []float32
	spectrum     *audioui.Spectrum
	bands        [3]float32
	// gram is the spectrogram, shown in the spectrum's place while
	// showGram says; gramAt counts toward its next column, and
	// column holds the levels of one.
	gram     audioui.Spectrogram
	showGram bool
	gramAt   time.Duration
	column   []float32
	// specHead is where the spectrum's heading is, which switches it,
	// specArea where the spectrum is, its legend switching its lines,
	// and listenRects where the ways to listen are.
	specHead    geom.Rect
	specArea    geom.Rect
	listenRects []geom.Rect
	// in and out are the input's and output's levels, their faders the
	// gains in and out, and inFrom the frame of the input read next.
	in, out           audioui.Levels
	inFader, outFader *audioui.Fader
	inFrom            int64
	inBuf             []float32
	// inMeter takes the spectrum of the input to the chain, from the
	// frames of inSpan.
	inMeter *audioui.Spectrometer
	inSpan  []float32
	size    geom.Size
}

func newMeters(r *root) *meters {
	rate := r.d.mix.Rate()
	m := &meters{r: r, rate: rate, loud: audioui.NewLoudness(rate), scope: audioui.NewScope(),
		in: audioui.NewLevels(), out: audioui.NewLevels(), spectrum: audioui.NewSpectrum(specPoints),
		inMeter: audioui.NewSpectrometer(4096)}
	m.spectrum.ShowIn, m.spectrum.Switches = true, true
	m.spec, m.specIn = make([]float32, specPoints), make([]float32, specPoints)
	m.inFader, m.outFader = m.fader(false), m.fader(true)
	return m
}

// fader returns the fader of the gain in, or out, of the track picked.
func (m *meters) fader(out bool) *audioui.Fader {
	return audioui.NewFader(func() float32 {
		if out {
			return m.r.editor.edit.Out
		}
		return m.r.editor.edit.Gain
	}, func(v float32, u *gunim.UI) {
		if m.r.editor.track.ID == 0 {
			return
		}
		e := m.r.editor.edit
		if out {
			e.Out = v
		} else {
			e.Gain = v
		}
		m.r.editor.send(e, u)
	})
}

// show starts the integrated reading over as a track starts or another
// is picked.
func (m *meters) show(was, s Album) {
	if s.Starts != was.Starts || s.Current != was.Current {
		m.loud.Reset(m.rate)
		m.in, m.out = audioui.NewLevels(), audioui.NewLevels()
	}
	// The spectrum as kept, where the application says it changed: a
	// click shows its own at once.
	if s.Spectrum != was.Spectrum {
		m.setView(s.Spectrum)
	}
}

// view is how the spectrum is shown.
func (m *meters) view() SpectrumView {
	return SpectrumView{Gram: m.showGram, HideOut: m.spectrum.HideOut, HideIn: !m.spectrum.ShowIn}
}

func (m *meters) setView(v SpectrumView) {
	m.showGram, m.spectrum.HideOut, m.spectrum.ShowIn = v.Gram, v.HideOut, !v.HideIn
}

// listening returns the gain, in decibels, the track is heard at over
// its own level: the volume, and the level match.
func (m *meters) listening() float64 {
	s := m.r.state
	g := dB(float64(s.Volume))
	if t, ok := m.r.track(); ok {
		d, _ := s.matchDB(&t)
		g += float64(d)
	}
	return g
}

// Step implements [gunim.Animator]: each frame the meters read what has
// been heard since the last.
func (m *meters) Step(dt time.Duration) bool {
	var now int64
	if rate := m.r.d.mix.Rate(); rate != m.rate {
		// The speakers opened again at another rate: the sound heard
		// comes at it from now.
		m.rate = rate
		m.loud.Reset(rate)
	}
	m.buf, now = m.r.d.heard(m.buf[:0], m.from)
	m.from = now
	if len(m.buf) > 0 {
		// The listening level taken out, so the meters read the track.
		back := float32(math.Pow(10, -m.listening()/20))
		for i := range m.buf {
			m.buf[i] *= back
		}
		m.loud.Write(m.buf)
		m.out.Take(m.buf, m.rate, dt)
		m.scope.Write(m.buf)
	} else {
		m.out.Quiet(dt)
	}
	// The input, as fed into the chain, for the moment heard.
	m.takeInput(dt)
	playing := m.r.state.Playing
	loud := m.loud.Step(dt, playing)
	m.scope.Step(dt)
	back := float32(-m.listening())
	if playing {
		m.r.d.spectrum(m.spectrum.Freqs, m.spec)
		for i := range m.spec {
			m.spec[i] += back
		}
		m.inputSpectrum()
	} else {
		for i := range m.spec {
			m.spec[i], m.specIn[i] = audioui.SpectrumBottom-30, audioui.SpectrumBottom-30
		}
	}
	settled := m.spectrum.Step(dt, m.spec, m.specIn)
	// The spectrogram takes a column 40 times a second, while the sound
	// plays.
	if m.gramAt += dt; playing && m.gramAt >= 25*time.Millisecond {
		m.gramAt = 0
		m.column = m.column[:0]
		for i, v := range m.spec {
			m.column = append(m.column, audioui.GramLevel(m.spectrum.Tilted(i, v)))
		}
		m.gram.Push(m.column)
	}
	// Three bands of the spectrum, for the little bars of the track
	// playing.
	m.spectrum.Bands(m.bands[:])
	// Stopped, the levels fall on to silence.
	return playing || !settled || loud || falling(&m.in) || falling(&m.out)
}

// falling says levels still show above the meter's foot, to fall on.
func falling(l *audioui.Levels) bool {
	for ch := range 2 {
		if max(l.Peak[ch], l.RMS[ch], l.Hold[ch]) > -60 {
			return true
		}
	}
	return false
}

// Layout implements [gunim.Node].
func (m *meters) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	m.size = c.Max
	area := m.ioArea(c.Max)
	for i, out := range []bool{false, true} {
		kids.At(i).Layout(gunim.Tight(geom.Sz(24, area.Size().H+16)))
		kids.At(i).Place(geom.Pt(m.faderX(c.Max, out), area.Min.Y-8))
	}
	return c.Max
}

// Children implements [gunim.Composite]: the faders of the gains in and
// out.
func (m *meters) Children() []gunim.Node { return []gunim.Node{m.inFader, m.outFader} }

// Paint implements [gunim.Node]: loudness at the top, the stereo image
// in the middle, the spectrum at the foot.
func (m *meters) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	pal := colours(f.Theme)
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(pal.panel))
	p.RRect(geom.Rc(0, 0, 1, box.H), 0, paint.Solid(faded(pal.ink, 0.06)))
	y := float32(16)
	y = m.paintIO(p, f, box, y)
	for k := range kids.All {
		k.Paint(p)
	}
	y = m.paintLoudness(p, f, box, y+14)
	y = m.paintScope(p, f, box, y+18)
	m.paintSpectrum(p, f, geom.Rc(16, y+18, box.W-32, box.H-y-34))
}

// takeInput reads what was fed into the chain for the moment heard
// since the last frame, as the input's levels.
func (m *meters) takeInput(dt time.Duration) {
	// The frames given, as the chain is fed them: through a loop's jumps.
	at, ok := m.r.d.heardFrames()
	if !ok || !m.r.state.Playing {
		m.in.Quiet(dt)
		return
	}
	var rate int
	m.inBuf, rate = m.r.d.input(m.inBuf[:0], 0, 0)
	heard := int64(at.Seconds() * float64(rate))
	// Off by more than a second, as after a seek: from here.
	if heard < m.inFrom || heard > m.inFrom+int64(rate) {
		m.inFrom = heard
	}
	m.inBuf, _ = m.r.d.input(m.inBuf[:0], m.inFrom, heard)
	m.inFrom = heard
	if len(m.inBuf) == 0 {
		m.in.Quiet(dt)
		return
	}
	m.in.Take(m.inBuf, rate, dt)
}

func (m *meters) paintLoudness(p *paint.Painter, f gunim.Frame, box geom.Size, y float32) float32 {
	pal := colours(f.Theme)
	target := m.r.state.Target
	shaped("LOUDNESS", 10, true).Paint(p, geom.Pt(16, y), faded(pal.teal, 0.85))
	shaped(fmt.Sprintf("target %.1f LUFS", target), 10, false).Paint(p, geom.Pt(100, y), pal.quiet(0.4))
	return m.loud.Paint(p, f.Theme, geom.Rc(16, y+20, box.W-32, 0), target)
}

// paintScope draws the vectorscope, mid up and side across, the last
// frames heard as points, brighter the newer; and the correlation bar
// under it.
func (m *meters) paintScope(p *paint.Painter, f gunim.Frame, box geom.Size, y float32) float32 {
	pal := colours(f.Theme)
	shaped("STEREO", 10, true).Paint(p, geom.Pt(16, y), faded(pal.teal, 0.85))
	// How the sound is listened to, at the right: amber while it is
	// other than stereo, so it is not forgotten.
	m.listenRects = m.listenRects[:0]
	x := box.W - 16
	for i := len(listenNames) - 1; i >= 0; i-- {
		run := shaped(listenNames[i], 10, true)
		r := geom.Rc(x-run.Advance-16, y-5, run.Advance+16, 20)
		x = r.Min.X - 4
		m.listenRects = append([]geom.Rect{r}, m.listenRects...)
		on := Listen(i) == m.r.state.Listen
		c := pal.quiet(0.45)
		if on {
			lit := pal.teal
			if Listen(i) != ListenStereo {
				lit = pal.amber
			}
			p.RRect(r, 10, paint.Solid(faded(lit, 0.18)))
			pal.outline(p, r, 10, 1)
			c = lit
		}
		run.Paint(p, geom.Pt(r.Min.X+8, r.Min.Y+4), c)
	}
	y += 20
	side := min(box.W-32, 170)
	m.scope.Paint(p, f.Theme, geom.Rc((box.W-side)/2, y, side, side))
	y += side + 12
	m.scope.PaintCorrelation(p, f.Theme, geom.Rc(40, y, box.W-80, 8))
	return y + 14
}

// paintSpectrum draws the spectrum heard into area.
func (m *meters) paintSpectrum(p *paint.Painter, f gunim.Frame, area geom.Rect) {
	pal := colours(f.Theme)
	if area.Size().H < 40 {
		return
	}
	// The heading names both views, the one shown lit; a click on it
	// switches.
	head := area.Min.Sub(geom.Pt(0, 2))
	spec, gram := shaped("SPECTRUM", 10, true), shaped("SPECTROGRAM", 10, true)
	on, off := faded(pal.teal, 0.85), pal.quiet(0.35)
	if m.showGram {
		on, off = off, on
	}
	spec.Paint(p, head, on)
	gram.Paint(p, head.Add(geom.Pt(spec.Advance+12, 0)), off)
	m.specHead = geom.Rc(head.X-4, head.Y-4, spec.Advance+gram.Advance+20, 20)
	area.Min.Y += 18
	if m.showGram {
		p.RRect(area, 10, paint.Solid(pal.night))
		m.gram.Paint(p, area, 1)
		audioui.PaintPitches(p, f.Theme, area, true)
		return
	}
	// The output, filled, and over it the input to the chain, a line:
	// where they part, the chain changed the sound. Their legend
	// switches each on and off.
	m.specArea = area
	m.spectrum.Paint(p, f.Theme, area)
}

// Handle implements [gunim.Handler]: a click on the spectrum's heading
// switches it to the spectrogram and back, and one on its legend
// switches the output or the input on and off.
func (m *meters) Handle(e input.Event, u *gunim.UI) bool {
	d, ok := e.(input.PointerDown)
	if !ok || d.Button != input.ButtonPrimary {
		return false
	}
	for i, r := range m.listenRects {
		if r.Contains(d.Pos) {
			u.Cue(gunim.CueTick, m)
			u.Send(m, SetListen{Listen: Listen(i)})
			u.Invalidate()
			return true
		}
	}
	if !m.showGram {
		out, in := m.spectrum.LegendRects(m.specArea)
		switch {
		case out.Contains(d.Pos):
			m.spectrum.HideOut = !m.spectrum.HideOut
		case in.Contains(d.Pos):
			m.spectrum.ShowIn = !m.spectrum.ShowIn
		}
		if out.Contains(d.Pos) || in.Contains(d.Pos) {
			u.Cue(gunim.CueTick, m)
			u.Send(m, SetSpectrum{View: m.view()})
			u.Invalidate()
			return true
		}
	}
	if !m.specHead.Contains(d.Pos) {
		return false
	}
	m.showGram = !m.showGram
	u.Cue(gunim.CueTick, m)
	u.Send(m, SetSpectrum{View: m.view()})
	u.Invalidate()
	return true
}

// listenNames name the ways to listen, in their order.
var listenNames = []string{"Stereo", "Mono", "Side"}

// inputSpectrum takes the spectrum of the newest frames fed into the
// chain, for the moment heard, into specIn, as the analyzer takes the
// output's: in decibels, 0 for a full-scale sine.
func (m *meters) inputSpectrum() {
	n := int64(m.inMeter.Frames())
	at, ok := m.r.d.heardFrames()
	var rate int
	if ok {
		m.inSpan, rate = m.r.d.input(m.inSpan[:0], 0, 0)
		heard := int64(at.Seconds() * float64(rate))
		m.inSpan, _ = m.r.d.input(m.inSpan[:0], heard-n, heard)
	}
	if !ok || !m.inMeter.Measure(m.inSpan, rate, m.spectrum.Freqs, m.specIn) {
		for i := range m.specIn {
			m.specIn[i] = audioui.SpectrumBottom - 30
		}
	}
}
