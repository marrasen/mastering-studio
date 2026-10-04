package main

import (
	"context"
	"errors"
	"io"
	"math"
	"time"

	"github.com/marrasen/gunim/audio"
)

// The gain that brings a track to the target. The gain comes before the
// track's chain, so through a limiter the loudness follows it less
// than one for one: the gain is found in a few measures, each through
// the same offline copy of the chain, set back between them.

// matchWithin is how near the target a match is close enough, in LU.
const matchWithin = 0.1

// matchTries is how many measures a match takes at most.
const matchTries = 6

// matched is a match's result: the gain, and the track measured with it.
type matched struct {
	id      int
	version int
	gain    float32
	m       Measure
	err     error
}

// matchGain finds the gain, in decibels, from -24 to 24, that brings the
// track rendered from path with edit e and its chain to target LUFS,
// and returns it with the track measured at it.
func matchGain(ctx context.Context, path string, gap time.Duration, e Edit, chain []Slot, states map[int][]byte,
	target float32) (float32, Measure, error) {
	src, format, closer, err := openTrack(path)
	if err != nil {
		return 0, Measure{}, err
	}
	defer closer()
	var rk *rack
	if len(chain) > 0 {
		if rk, err = offlineRack(chain, states, format.SampleRate); err != nil {
			return 0, Measure{}, err
		}
		defer rk.close()
	}
	at := func(gain float32) (Measure, error) {
		ed := e
		ed.Gain = gain
		var r audio.Seeker = newRender(src, format.SampleRate, gap, ed)
		if rk != nil {
			// Each measure starts the plugins from silence.
			for _, lp := range rk.plugins {
				lp.p.Reset()
			}
			r = newStage(r, rk)
		}
		return measureOf(ctx, r, format.SampleRate)
	}
	clamp := func(g float64) float32 { return float32(max(-24, min(g, 24))) }
	g0 := e.Gain
	m0, err := at(g0)
	if err != nil {
		return 0, Measure{}, err
	}
	if !m0.Loud {
		return g0, m0, nil
	}
	// First as though the loudness follows the gain one for one, then
	// by how it followed the last step.
	slope := 1.0
	for range matchTries - 1 {
		off := float64(target - m0.LUFS)
		if math.Abs(off) <= matchWithin {
			break
		}
		g1 := clamp(float64(g0) + off/slope)
		if g1 == g0 {
			break
		}
		m1, err := at(g1)
		if err != nil {
			return 0, Measure{}, err
		}
		if d := float64(m1.LUFS - m0.LUFS); math.Abs(d) > 0.01 {
			slope = max(0.05, min(d/float64(g1-g0), 2))
		}
		g0, m0 = g1, m1
	}
	return g0, m0, nil
}

// measureOf reads r through, at rate, and measures it.
func measureOf(ctx context.Context, r audio.Seeker, rate int) (Measure, error) {
	lm := audio.NewLoudnessMeter(rate)
	var tp audio.TruePeakMeter
	buf := make([]float32, 2*8192)
	for {
		if ctx.Err() != nil {
			return Measure{}, ctx.Err()
		}
		n, err := r.Read(buf)
		lm.Write(buf[:2*n])
		tp.Write(buf[:2*n])
		if errors.Is(err, io.EOF) || (err == nil && n == 0) {
			break
		}
		if err != nil {
			return Measure{}, err
		}
	}
	return reading(lm, &tp, duration(r.Len(), rate)), nil
}

// match brings track id to the target, in the background.
func (a *app) match(id int) {
	t := a.track(id)
	if t == nil || t.Matching {
		return
	}
	t.Matching = true
	chain, states := a.chainOf(t)
	version, path, gap, e, target := a.version[id], t.File, a.gapOf(t), t.Edit, a.Target
	a.work.Add(1)
	go func() {
		defer a.work.Done()
		select {
		case slots <- struct{}{}:
		case <-a.ctx.Done():
			return
		}
		g, m, err := matchGain(a.ctx, path, gap, e, chain, states, target)
		<-slots
		select {
		case a.matches <- matched{id, version, g, m, err}:
		case <-a.ctx.Done():
		}
	}()
}

// matchedGain takes a match's result: the track gets the gain, and is
// measured at it, unless it changed since the match began.
func (a *app) matchedGain(r matched) {
	t := a.track(r.id)
	if t == nil {
		return
	}
	t.Matching = false
	if r.err != nil {
		a.Note = t.Title + ": " + r.err.Error()
		return
	}
	if r.version != a.version[r.id] {
		a.Note = t.Title + " changed while it was matched to the target; match it again"
		return
	}
	t.Edit.Gain = r.gain
	// The window takes the gain as an edit newer than its own.
	t.Seq++
	a.version[r.id]++
	a.dirty = true
	a.measured(measured{id: r.id, version: a.version[r.id], m: r.m})
	if a.Playing && a.Current == r.id {
		a.replayEdit()
	}
}
