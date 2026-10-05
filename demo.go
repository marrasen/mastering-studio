package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/marrasen/gunim/audio"
)

// The demo: six songs made in code, of an album of made-up names, and a
// project of them, to try the studio on, and to show it with.

// demoRate is the songs' rate.
const demoRate = 44100

// demoSong is a song of the demo: its tempo, key and sections.
type demoSong struct {
	title string
	bpm   float64
	// root is the key's root in the bass, in hertz.
	root  float64
	minor bool
	parts []demoPart
	seed  uint64
	gain  float64
}

// demoPart is a section of a song, bars long, and how loud each
// instrument plays in it, from 0 to 1.
type demoPart struct {
	bars                         int
	drums, bass, pad, lead, hats float64
}

var (
	intro  = demoPart{4, 0, 0.3, 0.6, 0, 0}
	verse  = demoPart{8, 0.8, 0.7, 0.4, 0, 0.5}
	chorus = demoPart{8, 1, 0.9, 0.7, 0.6, 0.8}
	bridge = demoPart{4, 0.3, 0.5, 0.8, 0.3, 0.2}
	outro  = demoPart{4, 0, 0, 0.5, 0, 0}
)

var demoSongs = []demoSong{
	{"Lantern Season", 112, 55, true, []demoPart{intro, verse, chorus, verse, chorus, bridge, chorus, outro}, 1, 0.9},
	{"Paper Boats", 96, 49, false, []demoPart{intro, verse, verse, chorus, bridge, chorus, outro}, 2, 0.7},
	{"Undertow", 124, 41.2, true, []demoPart{intro, verse, chorus, verse, chorus, chorus, outro}, 3, 1.1},
	{"Glass Orchard", 88, 58.3, false, []demoPart{intro, bridge, verse, chorus, bridge, chorus, outro}, 4, 0.6},
	{"Long Way Home", 104, 46.2, true, []demoPart{intro, verse, chorus, bridge, verse, chorus, chorus, outro}, 5, 0.85},
	{"Static Bloom", 130, 51.9, false, []demoPart{intro, verse, chorus, verse, chorus, bridge, chorus, outro}, 6, 1.0},
}

// writeDemo writes the demo's songs to dir, as WAV files, and a project
// of them, and returns the project's path. What is there already stays
// as it is: the project as it was worked on, measures and all.
func writeDemo(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	files := make([]string, len(demoSongs))
	errs := make([]error, len(demoSongs))
	var wg sync.WaitGroup
	for i, s := range demoSongs {
		files[i] = fmt.Sprintf("%02d %s.wav", i+1, s.title)
		song := filepath.Join(dir, files[i])
		if _, err := os.Stat(song); err == nil {
			continue
		}
		wg.Go(func() { errs[i] = writeSong(song, s) })
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "Lantern Season"+albumExt)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	curves := CurveS | CurveI
	p := project{Release: Release{Artist: "The Quiet Harbour", Title: "Lantern Season", Year: "2026", Genre: "Indie"},
		Gap: 2 * time.Second, Target: -14, Bits: 16, Dither: true, ExportDir: "Masters", Volume: 0.8,
		Follow: FollowScroll, Curves: &curves}
	for i, s := range demoSongs {
		k := keptTrack{Title: s.title, File: files[i]}
		if i%3 == 1 {
			k.Edit.FadeOut = Fade{Length: 3500 * time.Millisecond, Curve: Natural}
		}
		p.Tracks = append(p.Tracks, k)
	}
	first := &p.Tracks[0]
	first.Note = "Low end a touch heavy in the chorus"
	first.Marks = []Mark{{ID: 1, At: 34400 * time.Millisecond, Text: "Chorus lift"},
		{ID: 2, At: 77 * time.Second, Text: "Check the hats"}}
	first.Edit.FadeIn = Fade{Length: 600 * time.Millisecond, Curve: Smooth}
	first.Edit.FadeOut = Fade{Length: 4 * time.Second, Curve: Natural}
	b, err := json.MarshalIndent(p, "", "\t")
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, b, 0o644)
}

// writeSong writes s to a 16-bit WAV file at path.
func writeSong(path string, s demoSong) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w, err := audio.NewWAVWriter(f, demoRate, 16, true)
	if err == nil {
		err = w.Write(demoRoom(s.render()))
	}
	if err == nil {
		err = w.Close()
	}
	return errors.Join(err, f.Close())
}

// render plays the song: drums, bass on the root, a pad of the chords,
// and a lead in the choruses, after a second of silence and before two.
func (s demoSong) render() []float32 {
	rng := rand.New(rand.NewPCG(s.seed, 7))
	beat := 60 / s.bpm
	bar := 4 * beat
	total := 3.0
	for _, p := range s.parts {
		total += float64(p.bars) * bar
	}
	n := int(total * demoRate)
	out := make([]float32, 2*n)
	// The chords, in semitones from the root.
	prog := [][]float64{{0, 4, 7}, {5, 9, 12}, {7, 11, 14}, {-3, 0, 4}}
	riff := []float64{12, 16, 19, 21, 19, 16, 14, 12}
	if s.minor {
		prog = [][]float64{{0, 3, 7}, {-4, 0, 3}, {-2, 2, 5}, {-5, -2, 2}}
		riff = []float64{12, 15, 19, 17, 15, 12, 10, 12}
	}
	hz := func(semi float64) float64 { return s.root * math.Pow(2, semi/12) }
	t0, bars := 1.0, 0
	var low, hiss float64
	for _, p := range s.parts {
		for b := range p.bars {
			chord := prog[bars%len(prog)]
			start := t0 + float64(b)*bar
			for i := int(start * demoRate); i < int((start+bar)*demoRate) && i < n; i++ {
				t := float64(i)/demoRate - start
				onBeat, onEighth := math.Mod(t, beat), math.Mod(t, beat/2)
				var l, r float64
				if p.drums > 0 {
					// A kick on one and three, a snare on two and four.
					if int(t/beat)%2 == 0 {
						f := 50 + 90*math.Exp(-onBeat*30)
						k := math.Sin(2*math.Pi*f*onBeat) * math.Exp(-onBeat*7) * 0.9 * p.drums
						l, r = l+k, r+k
					} else {
						sn := (rng.Float64()*2 - 1) * math.Exp(-onBeat*18) * 0.45
						sn += math.Sin(2*math.Pi*190*onBeat) * math.Exp(-onBeat*25) * 0.3
						l, r = l+sn*p.drums, r+sn*p.drums
					}
				}
				if p.hats > 0 {
					noise := rng.Float64()*2 - 1
					hiss += (noise - hiss) * 0.6
					h := (noise - hiss) * math.Exp(-onEighth*60) * 0.18 * p.hats
					l, r = l+h*0.7, r+h
				}
				if p.bass > 0 {
					saw := 2*math.Mod(hz(chord[0])*t, 1) - 1
					low += (saw - low) * 0.08
					v := low * 0.55 * p.bass * (0.6 + 0.4*math.Exp(-onEighth*6))
					l, r = l+v, r+v
				}
				if p.pad > 0 {
					var v float64
					for k, semi := range chord {
						f := hz(semi + 24)
						v += math.Sin(2*math.Pi*f*t+float64(k))*0.08 + math.Sin(2*math.Pi*f*1.003*t)*0.05
					}
					sway := 0.5 + 0.5*math.Sin(2*math.Pi*0.25*float64(i)/demoRate)
					l += v * p.pad * (0.7 + 0.3*sway)
					r += v * p.pad * (1 - 0.3*sway)
				}
				if p.lead > 0 {
					f := hz(chord[0] + 24 + riff[(int(t/(beat/2))+bars)%len(riff)])
					v := (math.Sin(2*math.Pi*f*t) + 0.3*math.Sin(4*math.Pi*f*t)) * math.Exp(-onEighth*4) * 0.12 * p.lead
					l, r = l+v*0.6, r+v
				}
				out[2*i] += float32(l * s.gain * 0.5)
				out[2*i+1] += float32(r * s.gain * 0.5)
			}
			bars++
		}
		t0 += float64(p.bars) * bar
	}
	return out
}

// demoRoom adds a room to a mix: a few echoes a side, of different
// lengths left and right, so the sound spreads across the stereo field.
func demoRoom(x []float32) []float32 {
	n := len(x) / 2
	out := make([]float32, len(x))
	copy(out, x)
	lens := [2][]int{{3114, 3234, 2982, 2844}, {2554, 2712, 2376, 2232}}
	for ch := range 2 {
		for _, l := range lens[ch] {
			buf := make([]float64, l)
			var lp float64
			for i := range n {
				y := buf[i%l]
				lp += (y - lp) * 0.4
				buf[i%l] = float64(x[2*i]+x[2*i+1])/2 + lp*0.78
				out[2*i+ch] += float32(y * 0.07)
			}
		}
	}
	return out
}
