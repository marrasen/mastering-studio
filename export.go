package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/marrasen/gunim/audio"
)

// exportName returns the name of the file track t exports to, as the
// album numbers it: "03 Title.wav".
func exportName(number int, title string) string {
	clean := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`<>:"/\|?*`, r) || r < 32 {
			return '_'
		}
		return r
	}, title)
	return fmt.Sprintf("%02d %s.wav", number, strings.TrimSpace(clean))
}

// exportJob is a track to export: what to render, and where to.
type exportJob struct {
	id     int
	path   string
	edit   Edit
	gap    time.Duration
	out    string
	bits   int
	dither bool
}

// export renders tracks to WAV files, one at a time, each at its own
// length, from the silence before it to its fade's end, measured as it
// is written: the tracks named, or every one.
func (a *app) export(ids []int) {
	if a.Exporting || len(a.Tracks) == 0 {
		return
	}
	dir := a.ExportDir
	if dir == "" {
		// Beside the first track's file.
		dir = filepath.Join(filepath.Dir(a.Tracks[0].File), "Export")
		a.ExportDir = dir
		a.dirty = true
	}
	var jobs []exportJob
	for i, t := range a.Tracks {
		if len(ids) > 0 && !slices.Contains(ids, t.ID) {
			continue
		}
		jobs = append(jobs, exportJob{id: t.ID, path: t.File, edit: t.Edit, gap: a.Gap,
			out: filepath.Join(dir, exportName(i+1, t.Title)), bits: a.Bits, dither: a.Dither})
		if tr := a.track(t.ID); tr != nil {
			tr.Progress, tr.Exported = 0.001, ""
		}
	}
	a.Exporting = true
	go func() {
		send := func(e exported) {
			select {
			case a.progress <- e:
			case <-a.ctx.Done():
			}
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			send(exported{err: err, done: true})
			return
		}
		for _, j := range jobs {
			out, err := exportTrack(j, func(p float32) { send(exported{id: j.id, progress: p}) })
			send(exported{id: j.id, progress: 1, path: j.out, out: out, err: err})
			if a.ctx.Err() != nil {
				return
			}
		}
		send(exported{done: true})
	}()
}

// exportTrack renders job to its file, telling its progress, and
// returns its reading as written.
func exportTrack(j exportJob, progress func(float32)) (Measure, error) {
	src, format, closer, err := openTrack(j.path)
	if err != nil {
		return Measure{}, err
	}
	defer closer()
	r := newRender(src, format.SampleRate, j.gap, j.edit)
	f, err := os.Create(j.out)
	if err != nil {
		return Measure{}, err
	}
	w, err := audio.NewWAVWriter(f, format.SampleRate, j.bits, j.dither)
	if err != nil {
		_ = f.Close()
		return Measure{}, err
	}
	lm := audio.NewLoudnessMeter(format.SampleRate)
	var tp audio.TruePeakMeter
	buf := make([]float32, 2*8192)
	var done int64
	last := time.Now()
	for {
		n, rerr := r.Read(buf)
		lm.Write(buf[:2*n])
		tp.Write(buf[:2*n])
		if werr := w.Write(buf[:2*n]); werr != nil {
			_ = f.Close()
			return Measure{}, werr
		}
		done += int64(n)
		if time.Since(last) > 50*time.Millisecond {
			last = time.Now()
			progress(float32(done) / float32(max(r.Len(), 1)))
		}
		if rerr != nil || n == 0 {
			break
		}
	}
	if err := w.Close(); err != nil {
		_ = f.Close()
		return Measure{}, err
	}
	if err := f.Close(); err != nil {
		return Measure{}, err
	}
	l, ok := lm.Integrated()
	m := Measure{Loud: ok, TruePeak: float32(tp.Peak()), Peak: lm.Peak(), Length: duration(r.Len(), format.SampleRate)}
	if ok {
		m.LUFS = float32(l)
	}
	return m, nil
}

// exportProgress takes the export's progress.
func (a *app) exportProgress(e exported) {
	if e.done {
		a.Exporting = false
		if e.err != nil {
			a.Note = e.err.Error()
		}
		return
	}
	t := a.track(e.id)
	if t == nil {
		return
	}
	t.Progress = e.progress
	if e.path != "" {
		t.Progress = 0
		if e.err != nil {
			a.Note = fmt.Sprintf("%s: %v", t.Title, e.err)
			return
		}
		t.Exported, t.Out = e.path, e.out
	}
}
