package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"math"
	"os"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audioui"
)

// scan is what reading a track's file through tells, once: its format
// and length, its waveform, and where its sound starts and ends, past
// the silence or noise around it.
type scan struct {
	Format     audio.Format
	Frames     int64
	Wave       *audioui.Wave
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
	ws := audioui.NewWaveScan(total, format.SampleRate)
	first, last := int64(-1), int64(0)
	buf := make([]float32, 2*8192)
	var at int64
	for {
		if ctx.Err() != nil {
			return scan{}, ctx.Err()
		}
		n, err := src.Read(buf)
		ws.Write(buf[:2*n])
		for i := range n {
			if max(math.Abs(float64(buf[2*i])), math.Abs(float64(buf[2*i+1]))) > silence {
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
	sc := scan{Format: format, Frames: total, Wave: ws.Done(), SoundEnd: duration(total, format.SampleRate)}
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
	// LRA is how far its loudness ranges, in LU, from Low to High, in
	// LUFS, as EBU Tech 3342 measures it; Ranged says it has a range:
	// under three seconds of sound has none.
	LRA, Low, High float32
	Ranged         bool
	// DryLUFS is the loudness of the mix, as it came, which DryLoud says
	// it has: the chain and the gains bypassed.
	DryLUFS float32
	DryLoud bool
	// blocks and shorts are the powers the loudness and its range are
	// measured from, for the album's, measured over every track, and
	// for the editor's curves; running is the integrated loudness from
	// the start to each second, for its curve of it.
	blocks, shorts []float64
	running        []float32
	// sum is of the file measured, as it was, for a file that comes
	// back the same after its time changes, as a copy or a cloud folder
	// gives it, to keep its measure.
	sum string
}

// reading is what the meters measured of a sound length long.
func reading(lm *audio.LoudnessMeter, tp *audio.TruePeakMeter, length time.Duration) Measure {
	m := Measure{TruePeak: float32(tp.Peak()), Peak: lm.Peak(), Length: length, blocks: lm.Blocks(), shorts: lm.ShortTerms()}
	m.running = audioui.RunningLoudness(m.blocks)
	if l, ok := lm.Integrated(); ok {
		m.LUFS, m.Loud = float32(l), true
	}
	if low, high, ok := audio.LoudnessRange(m.shorts); ok {
		m.Low, m.High, m.LRA, m.Ranged = float32(low), float32(high), float32(high-low), true
	}
	return m
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
	return measureOf(ctx, r, format.SampleRate)
}

// dB is a level, 1 at full scale, in decibels.
func dB(v float64) float64 { return 20 * math.Log10(max(v, 1e-9)) }

// rendered returns a track as exported: rendered from src, at rate, and
// run through a copy of its chain, offline, which done lets go of.
func rendered(src audio.Seeker, rate int, gap time.Duration, e Edit, chain []Slot, states map[int][]byte) (
	out audio.Seeker, done func(), err error) {
	r := newRender(src, rate, gap, e)
	rk, done := &rack{active: true}, func() {}
	if len(chain) > 0 {
		if rk, err = offlineRack(chain, states, rate); err != nil {
			return nil, nil, err
		}
		done = rk.close
	}
	st := newStage(r, rk, e.Out)
	// The mix's loudness too, as fed, for levels matched while bypassed.
	st.setIn(e.Gain)
	st.dryMeter = audio.NewLoudnessMeter(rate)
	return st, done, nil
}

// curves are the loudness along the track, as measured, for the
// editor to draw.
func (m *Measure) curves() audioui.Curves {
	return audioui.Curves{Blocks: m.blocks, Shorts: m.shorts, Running: m.running, LUFS: m.LUFS, Loud: m.Loud,
		Low: m.Low, High: m.High, LRA: m.LRA, Ranged: m.Ranged}
}

// keptMeasure is a track's measure as the project keeps it, with what
// the album's is measured from, and the file it measured as it was:
// a file changed since is measured anew.
type keptMeasure struct {
	Measure
	Blocks, Shorts []byte
	Size           int64
	Modified       time.Time
	Sum            string `json:",omitempty"`
}

// keep returns m, of the file at path, to keep.
func keep(path string, m Measure) *keptMeasure {
	k := &keptMeasure{Measure: m, Blocks: floats(m.blocks), Shorts: floats(m.shorts), Sum: m.sum}
	if fi, err := os.Stat(path); err == nil {
		k.Size, k.Modified = fi.Size(), fi.ModTime()
	}
	return k
}

// measure returns the measure kept, where the file at path is as it was
// measured: as it was then, or, its time changed, the same throughout.
func (k *keptMeasure) measure(path string) (Measure, bool) {
	if k == nil {
		return Measure{}, false
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Size() != k.Size {
		return Measure{}, false
	}
	if !fi.ModTime().Equal(k.Modified) && (k.Sum == "" || fileSum(path) != k.Sum) {
		return Measure{}, false
	}
	m := k.Measure
	m.blocks, m.shorts, m.sum = unfloats(k.Blocks), unfloats(k.Shorts), k.Sum
	m.running = audioui.RunningLoudness(m.blocks)
	return m, true
}

// fileSum is the SHA-256 of the file at path, in hex, or "" where it
// cannot be read.
func fileSum(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// floats packs powers as 32-bit floats, as the project keeps them.
func floats(vs []float64) []byte {
	b := make([]byte, 0, 4*len(vs))
	for _, v := range vs {
		b = binary.LittleEndian.AppendUint32(b, math.Float32bits(float32(v)))
	}
	return b
}

func unfloats(b []byte) []float64 {
	vs := make([]float64, len(b)/4)
	for i := range vs {
		vs[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:])))
	}
	return vs
}
