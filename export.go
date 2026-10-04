package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/marrasen/gunim/audio"
)

// exportName returns the name track t exports to, as the album numbers
// it, without its extension: "03 Title".
func exportName(number int, title string) string {
	clean := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`<>:"/\|?*`, r) || r < 32 {
			return '_'
		}
		return r
	}, title)
	return fmt.Sprintf("%02d %s", number, strings.TrimSpace(clean))
}

// trackTags are what an exported file says of itself.
type trackTags struct {
	Title, Artist, Album, Year, Genre string
	// Track is its number on the release, of Total.
	Track, Total int
}

// wavTags are the tags as a WAV file's LIST INFO chunk holds them.
func (t trackTags) wavTags() []audio.WAVTag {
	return []audio.WAVTag{{ID: "INAM", Value: t.Title}, {ID: "IART", Value: t.Artist}, {ID: "IPRD", Value: t.Album},
		{ID: "ITRK", Value: fmt.Sprintf("%d/%d", t.Track, t.Total)}, {ID: "ICRD", Value: t.Year}, {ID: "IGNR", Value: t.Genre}}
}

// soundWriter is a file's encoder, taking interleaved stereo frames.
type soundWriter interface {
	Write(frames []float32) error
	Close() error
}

// encodeMP3, where an encoder is at hand, makes one writing w, of sound
// at rate, at kbps, tagged.
var encodeMP3 func(w io.WriteSeeker, rate, kbps int, tags trackTags) (soundWriter, error)

// exportJob is a track to export: what to render, and where to.
type exportJob struct {
	id     int
	path   string
	edit   Edit
	gap    time.Duration
	chain  []Slot
	states map[int][]byte
	// base is the files' path but for their extension; wav and mp3 say
	// which to write, the WAV of bits, dithered or not, the MP3 at kbps.
	base     string
	wav, mp3 bool
	bits     int
	dither   bool
	kbps     int
	tags     trackTags
	// version is the track's, as it is exported.
	version int
}

// exportDir is where tracks export to: the folder chosen, or one beside
// the first track's file.
func (a *app) exportDir() string {
	if a.ExportDir != "" || len(a.Tracks) == 0 {
		return a.ExportDir
	}
	return filepath.Join(filepath.Dir(a.Tracks[0].File), "Export")
}

// export renders tracks to files, each at its own length, from the
// silence before it to its fade's end, measured as it is written: the
// tracks named, or every one. Tracks export side by side, as many at
// once as measuring takes, each through its own copy of its chain.
func (a *app) export(ids []int) {
	if a.Exporting || len(a.Tracks) == 0 {
		return
	}
	dir := a.exportDir()
	if a.ExportDir == "" {
		a.ExportDir = dir
		a.dirty = true
	}
	var jobs []exportJob
	for i, t := range a.Tracks {
		if len(ids) > 0 && !slices.Contains(ids, t.ID) {
			continue
		}
		chain, states := a.chainOf(&a.Tracks[i])
		jobs = append(jobs, exportJob{id: t.ID, path: t.File, edit: t.Edit, gap: a.Gap, chain: chain, states: states,
			base: filepath.Join(dir, exportName(i+1, t.Title)), wav: a.ExportWAV || !a.ExportMP3,
			mp3: a.ExportMP3 && encodeMP3 != nil, bits: a.Bits, dither: a.Dither, kbps: a.MP3Rate,
			tags: trackTags{Title: t.Title, Artist: a.Artist, Album: a.Title, Year: a.Year, Genre: a.Genre,
				Track: i + 1, Total: len(a.Tracks)},
			version: a.version[t.ID]})
		if tr := a.track(t.ID); tr != nil {
			tr.Progress, tr.Exported = 0.001, ""
		}
	}
	a.Exporting = true
	a.work.Add(1)
	go func() {
		defer a.work.Done()
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
		var wg sync.WaitGroup
		for _, j := range jobs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case slots <- struct{}{}:
				case <-a.ctx.Done():
					return
				}
				defer func() { <-slots }()
				out, path, err := exportTrack(a.ctx, j, func(p float32) { send(exported{id: j.id, progress: p}) })
				send(exported{id: j.id, version: j.version, progress: 1, path: path, out: out, err: err})
			}()
		}
		wg.Wait()
		send(exported{done: true})
	}()
}

// exportTrack renders job once, to each of its files, telling its
// progress, and returns its reading as written, and the first file's
// path.
func exportTrack(ctx context.Context, j exportJob, progress func(float32)) (Measure, string, error) {
	src, format, closer, err := openTrack(j.path)
	if err != nil {
		return Measure{}, "", err
	}
	defer closer()
	r, done, err := rendered(src, format.SampleRate, j.gap, j.edit, j.chain, j.states)
	if err != nil {
		return Measure{}, "", err
	}
	defer done()
	type sink struct {
		path string
		f    *os.File
		w    soundWriter
	}
	var sinks []sink
	// fail closes the files made, and takes them away: no file half
	// written stays.
	fail := func(err error) (Measure, string, error) {
		for _, s := range sinks {
			_ = s.f.Close()
			_ = os.Remove(s.path)
		}
		return Measure{}, "", err
	}
	open := func(ext string, encoder func(f *os.File) (soundWriter, error)) error {
		path := j.base + ext
		f, err := os.Create(path)
		if err != nil {
			return err
		}
		w, err := encoder(f)
		if err != nil {
			_ = f.Close()
			_ = os.Remove(path)
			return err
		}
		sinks = append(sinks, sink{path, f, w})
		return nil
	}
	if j.wav || !j.mp3 {
		if err := open(".wav", func(f *os.File) (soundWriter, error) {
			w, err := audio.NewWAVWriter(f, format.SampleRate, j.bits, j.dither)
			if err == nil {
				w.Tags = j.tags.wavTags()
			}
			return w, err
		}); err != nil {
			return fail(err)
		}
	}
	if j.mp3 && encodeMP3 != nil {
		if err := open(".mp3", func(f *os.File) (soundWriter, error) {
			return encodeMP3(f, format.SampleRate, j.kbps, j.tags)
		}); err != nil {
			return fail(err)
		}
	}
	lm := audio.NewLoudnessMeter(format.SampleRate)
	var tp audio.TruePeakMeter
	buf := make([]float32, 2*8192)
	var at int64
	last := time.Now()
	for {
		if ctx.Err() != nil {
			return fail(ctx.Err())
		}
		n, rerr := r.Read(buf)
		lm.Write(buf[:2*n])
		tp.Write(buf[:2*n])
		for _, s := range sinks {
			if err := s.w.Write(buf[:2*n]); err != nil {
				return fail(err)
			}
		}
		at += int64(n)
		if time.Since(last) > 50*time.Millisecond {
			last = time.Now()
			progress(float32(at) / float32(max(r.Len(), 1)))
		}
		if rerr != nil || n == 0 {
			break
		}
	}
	for _, s := range sinks {
		if err := s.w.Close(); err != nil {
			return fail(err)
		}
		if err := s.f.Close(); err != nil {
			return fail(err)
		}
	}
	return reading(lm, &tp, duration(r.Len(), format.SampleRate)), sinks[0].path, nil
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
		// What was written is the track measured, as it was exported.
		a.measured(measured{id: e.id, version: e.version, m: e.out})
	}
}
