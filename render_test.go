package main

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio"
)

const rate = 44100

// writeTrack writes a 24-bit WAV at 44.1 kHz: lead of silence, then a
// 1 kHz sine at amp for body, then tail of silence.
func writeTrack(t *testing.T, lead, body, tail time.Duration, amp float64) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "track.wav")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := audio.NewWAVWriter(f, rate, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	n0, n1, n2 := frames(lead, rate), frames(body, rate), frames(tail, rate)
	buf := make([]float32, 2*(n0+n1+n2))
	for i := range n1 {
		v := float32(amp * math.Sin(2*math.Pi*1000*float64(i)/rate))
		buf[2*(n0+i)], buf[2*(n0+i)+1] = v, v
	}
	if err := w.Write(buf); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// readAll reads src to its end.
func readAll(src audio.Source) []float32 {
	var out []float32
	buf := make([]float32, 2*4096)
	for {
		n, err := src.Read(buf)
		out = append(out, buf[:2*n]...)
		if err != nil || n == 0 {
			return out
		}
	}
}

func TestAScanFindsWhereTheSoundStartsAndEnds(t *testing.T) {
	path := writeTrack(t, 700*time.Millisecond, 2*time.Second, 300*time.Millisecond, 0.5)
	sc, err := scanTrack(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if sc.Format.SampleRate != rate || sc.Format.Bits != 24 || sc.Frames != frames(3*time.Second, rate) {
		t.Fatalf("scanned as %+v, %d frames", sc.Format, sc.Frames)
	}
	if d := sc.SoundStart - 700*time.Millisecond; d < -time.Millisecond || d > time.Millisecond {
		t.Fatalf("the sound starts at %v, want 700ms", sc.SoundStart)
	}
	if d := sc.SoundEnd - 2700*time.Millisecond; d < -time.Millisecond || d > time.Millisecond {
		t.Fatalf("the sound ends at %v, want 2.7s", sc.SoundEnd)
	}
	// Each stretch holds a few samples of the sine's cycle of 44: the
	// highest of a cycle's worth of them reaches its crest.
	var peak float32
	for b := waveBuckets / 2; b < waveBuckets/2+8; b++ {
		peak = max(peak, sc.Wave.Peak[0][b])
	}
	if math.Abs(float64(peak)-0.5) > 0.01 {
		t.Fatalf("the waveform's middle peaks at %v, want 0.5", peak)
	}
}

func TestARenderIsTheGapThenTheTrackCutAndFaded(t *testing.T) {
	path := writeTrack(t, 500*time.Millisecond, 2*time.Second, 0, 0.5)
	src, format, closer, err := openTrack(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closer()
	e := Edit{Start: 500 * time.Millisecond, FadeIn: Fade{Length: 100 * time.Millisecond, Curve: Linear},
		FadeOut: Fade{Length: time.Second, Curve: Smooth}, Gain: -6}
	r := newRender(src, format.SampleRate, time.Second, e)
	out := readAll(r)
	gap, body := frames(time.Second, rate), frames(2*time.Second, rate)
	if int64(len(out)/2) != gap+body {
		t.Fatalf("rendered %d frames, want %d: a second of silence and the two seconds cut", len(out)/2, gap+body)
	}
	for i := range gap {
		if out[2*i] != 0 {
			t.Fatalf("frame %d of the silence before is %v", i, out[2*i])
		}
	}
	// Peaks of the sine through the fades: the fade in's middle is at
	// half, -6 dB more; past the fades, at -6 dB.
	peakAround := func(at time.Duration) float64 {
		f := gap + frames(at, rate)
		p := 0.0
		for i := f - 22; i < f+22; i++ {
			p = max(p, math.Abs(float64(out[2*i])))
		}
		return p
	}
	half := 0.5 * math.Pow(10, -6.0/20)
	if p := peakAround(50 * time.Millisecond); math.Abs(p-half*0.5) > 0.01 {
		t.Fatalf("halfway through the linear fade in, the peak is %.3f, want %.3f", p, half*0.5)
	}
	if p := peakAround(700 * time.Millisecond); math.Abs(p-half) > 0.005 {
		t.Fatalf("past the fades, the peak is %.3f, want %.3f", p, half)
	}
	if p := peakAround(1500 * time.Millisecond); math.Abs(p-half*0.5) > 0.02 {
		t.Fatalf("halfway through the smooth fade out, the peak is %.3f, want %.3f", p, half*0.5)
	}
	if last := out[len(out)-2]; math.Abs(float64(last)) > 1e-3 {
		t.Fatalf("the last frame is %v, want silence", last)
	}
}

func TestTheCurvesRiseFromNothingToFull(t *testing.T) {
	for c := Linear; c <= Slow; c++ {
		if c.at(0) > 1e-9 || math.Abs(c.at(1)-1) > 1e-9 {
			t.Errorf("%s runs from %v to %v, want 0 to 1", curveNames[c], c.at(0), c.at(1))
		}
		last := 0.0
		for i := 1; i <= 100; i++ {
			v := c.at(float64(i) / 100)
			if v < last {
				t.Errorf("%s falls at %d%%", curveNames[c], i)
			}
			last = v
		}
	}
}

func TestAnExportIsMeasuredAsItWillSound(t *testing.T) {
	path := writeTrack(t, 0, 4*time.Second, 0, 0.2)
	e := Edit{FadeOut: Fade{Length: time.Second, Curve: Natural}}
	want, err := measure(context.Background(), path, 2*time.Second, e, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(t.TempDir(), exportName(3, "A/B: test"))
	got, out, err := exportTrack(context.Background(), exportJob{path: path, edit: e, gap: 2 * time.Second, base: base,
		wav: true, bits: 16, dither: true}, func(float32) {})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(out) != "03 A_B_ test.wav" {
		t.Fatalf("exported as %q", filepath.Base(out))
	}
	if math.Abs(float64(got.LUFS-want.LUFS)) > 0.05 || got.Length != want.Length {
		t.Fatalf("exported at %.2f LUFS, %v; measured before at %.2f, %v", got.LUFS, got.Length, want.LUFS, want.Length)
	}
	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	src, format, err := audio.DecodeNative(f)
	if err != nil {
		t.Fatal(err)
	}
	if format.Bits != 16 || format.SampleRate != rate || src.Len() != frames(6*time.Second, rate) {
		t.Fatalf("the file is %+v, %d frames; want 16 bits at 44.1 kHz, six seconds", format, src.Len())
	}
}

func TestAWaveformsCoarserLevelsHoldTheFinersExtremes(t *testing.T) {
	path := writeTrack(t, 0, 20*time.Second, 0, 0.3)
	sc, err := scanTrack(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	ls := sc.Wave.Levels
	if len(ls) < 2 || ls[0].Per != finest {
		t.Fatalf("the waveform has %d levels, the finest of %d frames", len(ls), ls[0].Per)
	}
	for i := 1; i < len(ls); i++ {
		fine, coarse := ls[i-1], ls[i]
		if coarse.Per != 4*fine.Per {
			t.Fatalf("level %d is of %d frames, after %d", i, coarse.Per, fine.Per)
		}
		for b := range coarse.Max[0] {
			var hi float32
			for j := 4 * b; j < min(4*b+4, len(fine.Max[0])); j++ {
				hi = max(hi, fine.Max[0][j])
			}
			if coarse.Max[0][b] != hi {
				t.Fatalf("level %d, stretch %d: highest %v, the finer's %v", i, b, coarse.Max[0][b], hi)
			}
		}
	}
}

func TestAScanDrawsTheFilesPitchesOverItsLength(t *testing.T) {
	// A second of silence, two of a 1 kHz tone at -10.5 dBFS, a second of
	// silence.
	path := writeTrack(t, time.Second, 2*time.Second, time.Second, 0.3)
	sc, err := scanTrack(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	g := sc.Wave.Gram
	if want := int(4*rate/gramHop) + 1; g.Cols < want-1 || g.Cols > want+1 {
		t.Fatalf("four seconds made %d columns, want about %d", g.Cols, want)
	}
	col := func(at float64) int { return int(at * rate / gramHop) }
	row := func(hz float64) int { return int(math.Log(hz/20) / math.Log(1000) * gramRows) }
	if db := g.at(col(2), row(1000)); math.Abs(db-(-10.5)) > 1.5 {
		t.Fatalf("in the tone, its pitch reads %.1f dB, want -10.5", db)
	}
	if db := g.at(col(2), row(100)); db > -60 {
		t.Fatalf("in the tone, 100 Hz reads %.1f dB", db)
	}
	if db := g.at(col(0.5), row(1000)); db > -90 {
		t.Fatalf("in the silence before, 1 kHz reads %.1f dB", db)
	}
}
