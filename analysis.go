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
			loud := false
			for ch := range 2 {
				v := buf[2*i+ch]
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

// measure reads a track through as rendered, at its file's rate.
func measure(ctx context.Context, path string, gap time.Duration, e Edit) (Measure, error) {
	src, format, closer, err := openTrack(path)
	if err != nil {
		return Measure{}, err
	}
	defer closer()
	r := newRender(src, format.SampleRate, gap, e)
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
