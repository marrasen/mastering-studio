package main

import (
	"context"
	"math"
	"os"
	"time"

	"github.com/marrasen/gunim/audio"
)

// waveBuckets is how many stretches a track's waveform is kept in: some
// 20 ms each for a five-minute track, fine enough to cut by.
const waveBuckets = 16384

// Wave is a track's file drawn as its loudness along it: for each
// stretch, each channel's peak and its RMS, from 0 to 1. It is made
// once and never changed, so the window may read it as the application
// shares it.
type Wave struct {
	Peak, RMS [2][]float32
	// Frames is the file's length, and Rate its rate.
	Frames int64
	Rate   int
	// Levels are the waveform finer, for the editor zoomed in: each
	// stretch's lowest and highest sample and its RMS, the finest first,
	// of finest frames a stretch, and each after four times coarser.
	Levels []Level
}

// Level is a waveform at one fineness: Per frames a stretch.
type Level struct {
	Per           int
	Min, Max, RMS [2][]float32
}

// finest is the frames of a stretch of a waveform's finest level:
// past it, the editor reads the samples themselves.
const finest = 128

// levels builds the coarser levels from the finest, four stretches to
// one, until a level is a few thousand stretches long.
func levels(fine Level) []Level {
	out := []Level{fine}
	for prev := fine; len(prev.Min[0]) > 4096; {
		n := (len(prev.Min[0]) + 3) / 4
		l := Level{Per: prev.Per * 4}
		for ch := range 2 {
			l.Min[ch], l.Max[ch], l.RMS[ch] = make([]float32, n), make([]float32, n), make([]float32, n)
			for b := range n {
				lo, hi, ms, k := float32(0), float32(0), float32(0), 0
				for j := 4 * b; j < min(4*b+4, len(prev.Min[ch])); j++ {
					lo, hi = min(lo, prev.Min[ch][j]), max(hi, prev.Max[ch][j])
					ms += prev.RMS[ch][j] * prev.RMS[ch][j]
					k++
				}
				l.Min[ch][b], l.Max[ch][b], l.RMS[ch][b] = lo, hi, float32(math.Sqrt(float64(ms/float32(k))))
			}
		}
		out = append(out, l)
		prev = l
	}
	return out
}

// scan is what reading a track's file through tells, once: its format
// and length, its waveform, and where its sound starts and ends, past
// the silence or noise around it.
type scan struct {
	Format     audio.Format
	Frames     int64
	Wave       *Wave
	SoundStart time.Duration
	SoundEnd   time.Duration
}

// silence is the level under which a file's start and end count as
// silent: -60 dBFS.
const silence = 0.001

// openTrack opens the file at path to read at its own rate.
func openTrack(path string) (audio.Seeker, audio.Format, func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, audio.Format{}, nil, err
	}
	src, format, err := audio.DecodeNative(f)
	if err != nil {
		_ = f.Close()
		return nil, audio.Format{}, nil, err
	}
	return src, format, func() { _ = f.Close() }, nil
}

// scanTrack reads the file at path through.
func scanTrack(ctx context.Context, path string) (scan, error) {
	src, format, closer, err := openTrack(path)
	if err != nil {
		return scan{}, err
	}
	defer closer()
	total := src.Len()
	w := &Wave{Frames: total, Rate: format.SampleRate}
	for ch := range 2 {
		w.Peak[ch] = make([]float32, waveBuckets)
		w.RMS[ch] = make([]float32, waveBuckets)
	}
	counts := make([]int, waveBuckets)
	per := max(float64(total)/waveBuckets, 1)
	nFine := int(max(1, (total+finest-1)/finest))
	fine := Level{Per: finest}
	for ch := range 2 {
		fine.Min[ch], fine.Max[ch], fine.RMS[ch] = make([]float32, nFine), make([]float32, nFine), make([]float32, nFine)
	}
	first, last := int64(-1), int64(0)
	buf := make([]float32, 2*8192)
	var at int64
	for {
		if ctx.Err() != nil {
			return scan{}, ctx.Err()
		}
		n, err := src.Read(buf)
		for i := range n {
			b := min(int(float64(at+int64(i))/per), waveBuckets-1)
			fb := min(int((at+int64(i))/finest), nFine-1)
			loud := false
			for ch := range 2 {
				v := buf[2*i+ch]
				fine.Min[ch][fb] = min(fine.Min[ch][fb], v)
				fine.Max[ch][fb] = max(fine.Max[ch][fb], v)
				fine.RMS[ch][fb] += v * v
				a := float32(math.Abs(float64(v)))
				w.Peak[ch][b] = max(w.Peak[ch][b], a)
				w.RMS[ch][b] += v * v
				loud = loud || a > silence
			}
			counts[b]++
			if loud {
				if first < 0 {
					first = at + int64(i)
				}
				last = at + int64(i)
			}
		}
		at += int64(n)
		if err != nil || n == 0 {
			break
		}
	}
	for ch := range 2 {
		for b, c := range counts {
			if c > 0 {
				w.RMS[ch][b] = float32(math.Sqrt(float64(w.RMS[ch][b] / float32(c))))
			}
		}
	}
	for ch := range 2 {
		for b := range fine.RMS[ch] {
			k := min(int64(finest), total-int64(b)*finest)
			fine.RMS[ch][b] = float32(math.Sqrt(float64(fine.RMS[ch][b] / float32(max(k, 1)))))
		}
	}
	w.Levels = levels(fine)
	sc := scan{Format: format, Frames: total, Wave: w, SoundEnd: duration(total, format.SampleRate)}
	if first >= 0 {
		sc.SoundStart, sc.SoundEnd = duration(first, format.SampleRate), duration(last+1, format.SampleRate)
	}
	return sc, nil
}

// Measure is a track measured as it is exported: its integrated
// loudness, LUFS, and its true and sample peaks, from 0 to 1. Loud says
// it has a loudness: silence has none.
type Measure struct {
	LUFS     float32
	Loud     bool
	TruePeak float32
	Peak     float32
	// Length is how long it plays, silence before it and all.
	Length time.Duration
}

// measure reads a track through as rendered, at its file's rate, and
// through its chain.
func measure(ctx context.Context, path string, gap time.Duration, e Edit, chain []Slot, states map[int][]byte) (Measure, error) {
	src, format, closer, err := openTrack(path)
	if err != nil {
		return Measure{}, err
	}
	defer closer()
	r, done, err := rendered(src, format.SampleRate, gap, e, chain, states)
	if err != nil {
		return Measure{}, err
	}
	defer done()
	lm := audio.NewLoudnessMeter(format.SampleRate)
	var tp audio.TruePeakMeter
	buf := make([]float32, 2*8192)
	for {
		if ctx.Err() != nil {
			return Measure{}, ctx.Err()
		}
		n, err := r.Read(buf)
		lm.Write(buf[:2*n])
		tp.Write(buf[:2*n])
		if err != nil || n == 0 {
			break
		}
	}
	l, ok := lm.Integrated()
	m := Measure{Loud: ok, TruePeak: float32(tp.Peak()), Peak: lm.Peak(), Length: duration(r.Len(), format.SampleRate)}
	if ok {
		m.LUFS = float32(l)
	}
	return m, nil
}

// dB is a level, 1 at full scale, in decibels.
func dB(v float64) float64 { return 20 * math.Log10(max(v, 1e-9)) }

// rendered returns a track as exported: rendered from src, at rate, and
// run through a copy of its chain, offline, which done lets go of.
func rendered(src audio.Seeker, rate int, gap time.Duration, e Edit, chain []Slot, states map[int][]byte) (
	out audio.Seeker, done func(), err error) {
	r := newRender(src, rate, gap, e)
	if len(chain) == 0 {
		return r, func() {}, nil
	}
	rk, err := offlineRack(chain, states, rate)
	if err != nil {
		return nil, nil, err
	}
	return newStage(r, rk), rk.close, nil
}
