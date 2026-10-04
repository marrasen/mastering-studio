package main

import (
	"math"
	"math/cmplx"

	"github.com/marrasen/gunim/audio"
)

// A file's spectrogram, made as its file is scanned: its pitches over
// its length, a column every gramHop frames, each of gramRows rows of
// pitch from 20 Hz to 20 kHz, evenly by octaves, of its level in
// decibels, packed in a byte.

const (
	// gramHop is the frames between columns, and gramSize the frames
	// each is taken over.
	gramHop  = 1024
	gramSize = 4096
	// gramRows are the rows of pitch.
	gramRows = 160
	// gramFloor is the level, in decibels, of a byte of 0, and a byte
	// is gramStep decibels.
	gramFloor = -120.0
	gramStep  = 0.5
)

// Gram is a file's spectrogram: Cols columns of gramRows bytes each,
// the lowest pitch first, a column every gramHop frames at Rate.
type Gram struct {
	Cols int
	Rate int
	Data []byte
}

// at is column c's level at row r, in decibels.
func (g *Gram) at(c, r int) float64 {
	return gramFloor + gramStep*float64(g.Data[c*gramRows+r])
}

// gramScan takes a file's frames as it is scanned, and makes its
// spectrogram.
type gramScan struct {
	g      *Gram
	fft    *audio.FFT
	window []float64
	// ring holds the last gramSize frames, mono, filled to n, and since
	// counts the frames since the last column.
	ring  []float64
	n     int
	since int
	x     []complex128
	// rows are the bins each row of pitch spans.
	rows [gramRows][2]int
}

func newGramScan(rate int) *gramScan {
	s := &gramScan{g: &Gram{Rate: rate}, fft: audio.NewFFT(gramSize), window: make([]float64, gramSize),
		ring: make([]float64, gramSize), x: make([]complex128, gramSize)}
	for i := range s.window {
		s.window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/gramSize)
	}
	bin := func(hz float64) float64 { return hz * gramSize / float64(rate) }
	for r := range gramRows {
		lo := bin(20 * math.Pow(1000, float64(r)/gramRows))
		hi := bin(20 * math.Pow(1000, float64(r+1)/gramRows))
		b0 := int(math.Round(lo))
		b1 := max(b0+1, int(math.Round(hi)))
		s.rows[r] = [2]int{min(b0, gramSize/2-1), min(b1, gramSize/2)}
	}
	// Half a window of silence first, so the first column is centred on
	// the first frame.
	s.n = gramSize / 2
	return s
}

// write takes frames, interleaved stereo.
func (s *gramScan) write(frames []float32) {
	for i := 0; i+1 < len(frames); i += 2 {
		s.ring[s.n%gramSize] = float64(frames[i]+frames[i+1]) / 2
		s.n++
		s.since++
		if s.since == gramHop {
			s.since = 0
			s.column()
		}
	}
}

// column adds the column of the last gramSize frames.
func (s *gramScan) column() {
	for i := range gramSize {
		s.x[i] = complex(s.ring[(s.n+i)%gramSize]*s.window[i], 0)
	}
	s.fft.Transform(s.x)
	// A full-scale sine is a magnitude of a quarter of the size, through
	// the window.
	norm := 4.0 / gramSize
	for _, b := range &s.rows {
		var peak float64
		for k := b[0]; k < b[1]; k++ {
			peak = max(peak, cmplx.Abs(s.x[k]))
		}
		db := 20 * math.Log10(max(peak*norm, 1e-9))
		s.g.Data = append(s.g.Data, byte(max(0, min((db-gramFloor)/gramStep, 255))))
	}
	s.g.Cols++
}

// done returns the spectrogram, its last columns run on through half a
// window of silence.
func (s *gramScan) done() *Gram {
	tail := make([]float32, 2*(gramSize/2))
	s.write(tail)
	return s.g
}
