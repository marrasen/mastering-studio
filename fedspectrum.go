package main

import (
	"context"
	"errors"
	"io"
	"math"
	"slices"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/mastering-studio/plug"
)

// The spectrum of the sound a slot's plugin is fed, over the whole
// track, for a plugin that asks for it through [plug.Host.Spectrum], as
// an EQ matching the track to pink noise does: the track is rendered
// through the plugins before the slot, offline, in the background.

type (
	// MeasureSpectrum asks for the spectrum of the sound fed into a
	// slot's plugin, at Freqs; Ticket tells the answer apart.
	MeasureSpectrum struct {
		Track, Slot int
		Freqs       []float64
		Ticket      int
	}
	// SlotSpectrum is the answer: the levels at the frequencies asked
	// for, or what kept the studio from measuring them.
	SlotSpectrum struct {
		Slot, Ticket int
		Levels       []float64
		Err          string
	}
)

// spectrumBlock is how many frames each transform takes, and
// spectrumQuiet the level under which a block counts as silence, left
// out, so a track's gaps and fades weigh nothing.
const (
	spectrumBlock = 8192
	spectrumQuiet = -50
)

// measureSpectrum starts measuring the spectrum asked for in the
// background; the answer comes to the loop through spectra.
func (a *app) measureSpectrum(in MeasureSpectrum) {
	t, i := a.slot(in.Track, in.Slot)
	if i < 0 {
		return
	}
	chain, states := a.chainOf(t)
	chain = chain[:i]
	path, gap, e := t.File, a.gapOf(t), t.Edit
	// The gain after the chain is after this slot too.
	e.Out = 0
	freqs := slices.Clone(in.Freqs)
	ctx := a.ctx
	a.work.Add(1)
	go func() {
		defer a.work.Done()
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		levels, err := fedSpectrum(ctx, path, gap, e, chain, states, freqs)
		<-slots
		out := SlotSpectrum{Slot: in.Slot, Ticket: in.Ticket, Levels: levels}
		if err != nil {
			out.Err = err.Error()
		}
		select {
		case a.spectra <- out:
		case <-ctx.Done():
		}
	}()
}

// tookSpectrum hands the window a spectrum measured, in place of any
// before for its slot.
func (a *app) tookSpectrum(s SlotSpectrum) {
	a.Spectra = slices.DeleteFunc(a.Spectra, func(o SlotSpectrum) bool { return o.Slot == s.Slot })
	a.Spectra = append(a.Spectra, s)
}

// fedSpectrum renders the track at path, with gap and e, through chain,
// and returns its power about each of freqs, in decibels: averaged over
// its blocks of sound, each overlapping the last by half, as Welch's
// method does, and each band summed from halfway to the frequency below
// to halfway to the one above, so pink noise reads level.
func fedSpectrum(ctx context.Context, path string, gap time.Duration, e Edit, chain []Slot, states map[int][]byte,
	freqs []float64) ([]float64, error) {
	if len(freqs) == 0 {
		return nil, nil
	}
	src, format, closer, err := openTrack(path)
	if err != nil {
		return nil, err
	}
	defer closer()
	r, done, err := rendered(src, format.SampleRate, gap, e, chain, states)
	if err != nil {
		return nil, err
	}
	defer done()
	if st, ok := r.(*chainStage); ok && len(chain) > 0 {
		st.r.measureSteps(format.SampleRate)
	}
	rate := float64(format.SampleRate)
	fft := audio.NewFFT(spectrumBlock)
	window := make([]float64, spectrumBlock)
	for i := range window {
		window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/spectrumBlock)
	}
	x := make([]complex128, spectrumBlock)
	frames := make([]float32, 2*spectrumBlock)
	power := make([]float64, spectrumBlock/2+1)
	blocks := 0
	// The first block whole, then each the second half of the last and
	// a new half.
	half := spectrumBlock / 2
	n, err := readFull(r, frames[:2*half])
	for ctx.Err() == nil && n == half {
		var k int
		k, err = readFull(r, frames[2*half:])
		if k < half {
			// The last, short block is left out: a window cut short
			// would smear it.
			break
		}
		var sum float64
		for i := range spectrumBlock {
			m := 0.5 * float64(frames[2*i]+frames[2*i+1])
			sum += m * m
			x[i] = complex(m*window[i], 0)
		}
		copy(frames, frames[2*half:])
		if 10*math.Log10(max(sum/spectrumBlock, 1e-20)) < spectrumQuiet {
			continue
		}
		fft.Transform(x)
		for k := range power {
			re, im := real(x[k]), imag(x[k])
			power[k] += re*re + im*im
		}
		blocks++
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if blocks == 0 {
		return nil, errors.New("the track is silent")
	}
	return bandLevels(power, float64(blocks), rate, freqs), nil
}

// bandLevels sums power, a transform's bins summed over blocks, about
// each of freqs, from halfway to the frequency below to halfway to the
// one above, in log frequency, each bin as far as it falls inside, and
// returns the mean of each a block, in decibels.
func bandLevels(power []float64, blocks, rate float64, freqs []float64) []float64 {
	binHz := rate / float64(2*(len(power)-1))
	out := make([]float64, len(freqs))
	for i, f := range freqs {
		lo, hi := f, f
		if i > 0 {
			lo = math.Sqrt(freqs[i-1] * f)
		}
		if i+1 < len(freqs) {
			hi = math.Sqrt(f * freqs[i+1])
		}
		// The ends reach as far out as in.
		if i == 0 {
			lo = f * f / hi
		}
		if i+1 == len(freqs) {
			hi = f * f / lo
		}
		var sum float64
		for k := max(0, int(lo/binHz)); k < len(power) && float64(k)-0.5 <= hi/binHz; k++ {
			from, to := max((float64(k)-0.5)*binHz, lo), min((float64(k)+0.5)*binHz, hi)
			if to > from {
				sum += power[k] * (to - from) / binHz
			}
		}
		out[i] = 10 * math.Log10(max(sum/blocks, 1e-20))
	}
	return out
}

// readFull reads src until frames are full or it ends, as a stage gives
// a chunk at a time.
func readFull(src audio.Source, frames []float32) (int, error) {
	n := 0
	for 2*n < len(frames) {
		k, err := src.Read(frames[2*n:])
		n += k
		if err != nil {
			return n, err
		}
		if k == 0 {
			// Offline, nothing more comes later.
			return n, io.EOF
		}
	}
	return n, nil
}

// spectrumDone is a plugin view's wait for a spectrum, by ticket.
type spectrumDone func(levels []float64, err error, u *gunim.UI)

// Spectrum implements [plug.Host]: the app measures it, and the answer
// comes back with the album.
func (h goHost) Spectrum(freqs []float64, done func(levels []float64, err error, u *gunim.UI), u *gunim.UI) {
	r := h.v.r
	r.tickets++
	if h.v.spectra == nil {
		h.v.spectra = map[int]spectrumDone{}
	}
	h.v.spectra[r.tickets] = done
	u.Send(nil, MeasureSpectrum{Track: h.v.track, Slot: h.v.slot, Freqs: slices.Clone(freqs), Ticket: r.tickets})
}

// takeSpectra hands each view the spectrum it waits for, once.
func (r *root) takeSpectra(s Album, u *gunim.UI) {
	for _, sp := range s.Spectra {
		v := r.goViews[sp.Slot]
		if v == nil {
			continue
		}
		done, ok := v.spectra[sp.Ticket]
		if !ok {
			continue
		}
		delete(v.spectra, sp.Ticket)
		var err error
		if sp.Err != "" {
			err = errors.New(sp.Err)
		}
		done(sp.Levels, err, u)
	}
}

var _ plug.Host = goHost{}
