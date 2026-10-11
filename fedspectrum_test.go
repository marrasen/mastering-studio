package main

import (
	"context"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/mastering-studio/plug/trim"
)

// writeNoise writes seconds of white noise at amp, at 44.1 kHz.
func writeNoise(t *testing.T, seconds int, amp float64) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "noise.wav")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := audio.NewWAVWriter(f, rate, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewPCG(1, 2))
	buf := make([]float32, 2*seconds*rate)
	for i := range buf {
		buf[i] = float32(amp * (2*r.Float64() - 1))
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

// sixths returns frequencies a sixth of an octave apart from 100 Hz to
// 10 kHz.
func sixths() []float64 {
	var out []float64
	for f := 100.0; f <= 10001; f *= math.Pow(2, 1.0/6) {
		out = append(out, f)
	}
	return out
}

func TestTheSpectrumOfWhiteNoiseRisesThreeDecibelsAnOctave(t *testing.T) {
	path := writeNoise(t, 8, 0.2)
	freqs := sixths()
	levels, err := fedSpectrum(context.Background(), path, 0, Edit{}, nil, nil, freqs)
	if err != nil {
		t.Fatal(err)
	}
	// Each band a sixth of an octave wider than the last holds that
	// much more of the noise's even power: the levels rise along a line
	// of 3 dB an octave, less what chance scatters about it, which the
	// narrow low bands, of a few of the transform's bins, show most.
	var sx, sy, sxx, sxy float64
	for i, f := range freqs {
		x := math.Log2(f / 100)
		sx, sy, sxx, sxy = sx+x, sy+levels[i], sxx+x*x, sxy+x*levels[i]
	}
	n := float64(len(freqs))
	slope := (n*sxy - sx*sy) / (n*sxx - sx*sx)
	if math.Abs(slope-3) > 0.15 {
		t.Errorf("the levels rise %.2f dB an octave, want 3", slope)
	}
	at := (sy - slope*sx) / n
	for i, f := range freqs {
		if d := levels[i] - (at + slope*math.Log2(f/100)); math.Abs(d) > 1.5 {
			t.Errorf("at %.0f Hz the level is %+.2f dB off the line", f, d)
		}
	}
}

func TestTheSpectrumAskedForIsOfTheSoundThePluginIsFed(t *testing.T) {
	a, tr, s := goApp(t)
	tr.File = writeNoise(t, 3, 0.2)
	a.handle(SetGoParam{Track: tr.ID, Slot: s.ID, ID: trim.GainID, Value: -6, Gesture: 1})
	a.handle(AddPlugin{Track: tr.ID, Choice: PluginChoice{Path: goPath, Class: trim.Descriptor.ID, Name: "Trim"}})
	after := tr.Chain[1].ID
	freqs := sixths()
	got := map[int]SlotSpectrum{}
	for ticket, slot := range []int{s.ID, after} {
		a.handle(MeasureSpectrum{Track: tr.ID, Slot: slot, Freqs: freqs, Ticket: ticket + 1})
		select {
		case sp := <-a.spectra:
			a.tookSpectrum(sp)
			got[slot] = sp
		case <-time.After(20 * time.Second):
			t.Fatal("the spectrum never came")
		}
	}
	first, second := got[s.ID], got[after]
	if first.Err != "" || second.Err != "" || len(first.Levels) != len(freqs) || second.Ticket != 2 {
		t.Fatalf("the spectra came as %+v and %+v", first, second)
	}
	// The trim at -6 dB is before the second slot, not the first.
	for i := range freqs {
		if d := second.Levels[i] - first.Levels[i]; math.Abs(d+6) > 0.1 {
			t.Fatalf("at %.0f Hz the second slot is fed %+.2f dB from the first, want -6", freqs[i], d)
		}
	}
	// The window is told the last of each slot's.
	if len(a.Spectra) != 2 {
		t.Errorf("the window is told %d spectra, want 2", len(a.Spectra))
	}
	a.handle(MeasureSpectrum{Track: tr.ID, Slot: s.ID, Freqs: freqs, Ticket: 3})
	a.tookSpectrum(<-a.spectra)
	if len(a.Spectra) != 2 {
		t.Errorf("measured again, the window is told %d spectra, want the last of each slot's", len(a.Spectra))
	}
}

func TestAViewIsGivenTheSpectrumItAskedForOnce(t *testing.T) {
	w, r, run := stage(t, goAlbum(0))
	v := r.goViews[9]
	if v == nil {
		t.Fatal("the trim has no views")
	}
	calls := 0
	var got []float64
	gunimRun := func(a Album) {
		t.Helper()
		if err := w.Client().Publish(albumTopic, a); err != nil {
			t.Fatal(err)
		}
		run(1)
	}
	v.spectra = map[int]spectrumDone{5: func(l []float64, err error, _ *gunim.UI) { calls++; got = l }}
	a := goAlbum(0)
	a.Spectra = []SlotSpectrum{{Slot: 9, Ticket: 5, Levels: []float64{1, 2}}}
	gunimRun(a)
	gunimRun(a)
	if calls != 1 || len(got) != 2 {
		t.Fatalf("the view was given its spectrum %d times, %v", calls, got)
	}
}
