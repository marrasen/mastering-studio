package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/marrasen/mastering-studio/plug"
)

// Tracks for plugins that read them whole, as [plug.TrackPlugin]: each
// Go plugin's slot has a document of one source, the track as it
// reaches the slot, edited and through the plugins before it. A source
// renders only once a plugin reads it, offline, in the background, into
// a file of its own, which any goroutine then reads at any place. A
// change to the track, or to the plugins before the slot, makes a new
// version; the old one's file goes.

// trackDocs holds each Go plugin's slot's document, by slot, for the
// window's views to find, as livePlugins holds the plugins playing.
var trackDocs sync.Map

// trackDoc is a slot's document: its one source, played whole from the
// start of the track.
type trackDoc struct {
	src *renderedSource
}

// Sources implements [plug.Document].
func (d *trackDoc) Sources() []plug.Source { return []plug.Source{d.src} }

// Regions implements [plug.Document].
func (d *trackDoc) Regions() []plug.Region {
	return []plug.Region{{Source: d.src.ID(), Length: d.src.frames}}
}

// renderedSource is a track as it reaches a slot, rendered to a file as
// it is first read.
type renderedSource struct {
	version int
	name    string
	rate    int
	frames  int64
	// What it renders from: the track's file, its gap and edit, and the
	// plugins before the slot, with their states.
	path   string
	gap    time.Duration
	edit   Edit
	chain  []Slot
	states map[int][]byte

	start  sync.Once
	ctx    context.Context
	cancel context.CancelFunc
	// mu guards what the render has done: the frames written to file,
	// and whether it ended, and how; changed wakes the readers waiting.
	mu       sync.Mutex
	changed  *sync.Cond
	file     *os.File
	written  int64
	finished bool
	gone     bool
	err      error
	stopped  chan struct{}
}

func newRenderedSource(version int, t *Track, gap time.Duration, chain []Slot, states map[int][]byte) *renderedSource {
	start, end := t.Edit.span(t.Format.SampleRate, t.Frames)
	ctx, cancel := context.WithCancel(context.Background())
	s := &renderedSource{version: version, name: t.Title, rate: t.Format.SampleRate,
		frames: frames(gap, t.Format.SampleRate) + end - start,
		path:   t.File, gap: gap, edit: t.Edit, chain: chain, states: states, ctx: ctx, cancel: cancel,
		stopped: make(chan struct{})}
	// The gain after the chain comes after the slot too.
	s.edit.Out = 0
	s.changed = sync.NewCond(&s.mu)
	return s
}

// ID implements [plug.Source]: a document has one source.
func (s *renderedSource) ID() int { return 1 }

// Version implements [plug.Source].
func (s *renderedSource) Version() int { return s.version }

// Name implements [plug.Source].
func (s *renderedSource) Name() string { return s.name }

// Rate implements [plug.Source].
func (s *renderedSource) Rate() float64 { return float64(s.rate) }

// Channels implements [plug.Source].
func (s *renderedSource) Channels() int { return 2 }

// Frames implements [plug.Source].
func (s *renderedSource) Frames() int64 { return s.frames }

// errSourceGone is what a read of a source let go of, as a newer
// version took its place, returns.
var errSourceGone = errors.New("the track changed since")

// ReadAt implements [plug.Source]: it waits, the first time, while the
// track renders as far as dst reaches.
func (s *renderedSource) ReadAt(dst [][]float32, at int64) (int, error) {
	if len(dst) == 0 || at < 0 {
		return 0, nil
	}
	n := min(int64(len(dst[0])), s.frames-at)
	if n <= 0 {
		return 0, io.EOF
	}
	s.start.Do(func() { go s.render() })
	s.mu.Lock()
	for s.written < at+n && !s.finished {
		s.changed.Wait()
	}
	if s.gone {
		s.mu.Unlock()
		return 0, errSourceGone
	}
	if s.written < at+n {
		n = max(0, s.written-at)
	}
	err, f := s.err, s.file
	s.mu.Unlock()
	if n == 0 {
		if err == nil {
			err = io.EOF
		}
		return 0, err
	}
	raw := make([]byte, 8*n)
	if _, err := f.ReadAt(raw, 8*at); err != nil {
		return 0, err
	}
	for i := range n {
		l := math.Float32frombits(binary.LittleEndian.Uint32(raw[8*i:]))
		r := math.Float32frombits(binary.LittleEndian.Uint32(raw[8*i+4:]))
		if len(dst) == 1 {
			dst[0][i] = (l + r) / 2
			continue
		}
		dst[0][i], dst[1][i] = l, r
		for _, ch := range dst[2:] {
			ch[i] = 0
		}
	}
	return int(n), nil
}

// renderChunk is how many frames the render writes at a time.
const renderChunk = 1 << 14

// render renders the track through the chain into a file, waking the
// readers as it goes.
func (s *renderedSource) render() {
	defer close(s.stopped)
	err := s.renderAll()
	s.mu.Lock()
	s.finished = true
	if err != nil && s.err == nil {
		s.err = err
	}
	s.changed.Broadcast()
	s.mu.Unlock()
}

func (s *renderedSource) renderAll() error {
	select {
	case slots <- struct{}{}:
	case <-s.ctx.Done():
		return errSourceGone
	}
	defer func() { <-slots }()
	f, err := os.CreateTemp("", "mastering-track-*.f32")
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.file = f
	s.mu.Unlock()
	src, _, closer, err := openTrack(s.path)
	if err != nil {
		return err
	}
	defer closer()
	r, done, err := rendered(src, s.rate, s.gap, s.edit, s.chain, s.states)
	if err != nil {
		return err
	}
	defer done()
	if st, ok := r.(*chainStage); ok && len(s.chain) > 0 {
		st.r.measureSteps(s.rate)
	}
	frames := make([]float32, 2*renderChunk)
	raw := make([]byte, 8*renderChunk)
	for at := int64(0); at < s.frames; {
		if s.ctx.Err() != nil {
			return errSourceGone
		}
		n, err := readFull(r, frames)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		n = int(min(int64(n), s.frames-at))
		if n == 0 {
			// The file ended short of what it said: the rest is silence.
			clear(frames)
			n = int(min(renderChunk, s.frames-at))
		}
		for i := range 2 * n {
			binary.LittleEndian.PutUint32(raw[4*i:], math.Float32bits(frames[i]))
		}
		if _, err := f.WriteAt(raw[:8*n], 8*at); err != nil {
			return err
		}
		at += int64(n)
		s.mu.Lock()
		s.written = at
		s.changed.Broadcast()
		s.mu.Unlock()
	}
	return nil
}

// close lets the source go: the render stops, the readers waiting are
// told, and its file goes once the render has.
func (s *renderedSource) close() {
	s.cancel()
	s.mu.Lock()
	s.gone, s.finished = true, true
	s.changed.Broadcast()
	s.mu.Unlock()
	go func() {
		// A render never started never will; one started stops at its
		// next chunk.
		s.start.Do(func() { close(s.stopped) })
		<-s.stopped
		s.mu.Lock()
		f := s.file
		s.mu.Unlock()
		if f != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
		}
	}()
}

// docKey says what a slot's source renders from: a new key, a new
// version.
type docKey string

// readFull reads src until frames are full or it ends, as a stage gives
// a chunk at a time.
func readFull(src interface {
	Read([]float32) (int, error)
}, frames []float32) (int, error) {
	n := 0
	for 2*n < len(frames) {
		k, err := src.Read(frames[2*n:])
		n += k
		if err != nil {
			return n, err
		}
		if k == 0 {
			// Offline, nothing more comes later.
			return n, io.EOF
		}
	}
	return n, nil
}

// slotDoc is a slot's document as the app keeps it: what it renders
// from, and its version.
type slotDoc struct {
	key     docKey
	version int
	doc     *trackDoc
}

// syncDocs gives each Go plugin's slot a document of the track as it
// reaches the slot, anew where the track or the plugins before it
// changed, and the plugins loaded for its sound theirs; the documents of
// slots gone go.
func (a *app) syncDocs() {
	keep := map[int]bool{}
	for _, list := range [][]Track{a.Tracks, a.References} {
		for k := range list {
			t := &list[k]
			for i, s := range t.Chain {
				if !s.goPlugin() {
					continue
				}
				keep[s.ID] = true
				key := a.docKeyOf(t, i)
				sd := a.docs[s.ID]
				if sd != nil && sd.key == key {
					continue
				}
				version := 1
				if sd != nil {
					version = sd.version + 1
					sd.doc.src.close()
				}
				states := map[int][]byte{}
				for _, c := range t.Chain[:i] {
					states[c.ID] = a.states[c.ID]
				}
				doc := &trackDoc{src: newRenderedSource(version, t, a.gapOf(t), slices.Clone(t.Chain[:i]), states)}
				a.docs[s.ID] = &slotDoc{key: key, version: version, doc: doc}
				trackDocs.Store(s.ID, doc)
				if e := a.goEngineOf(t, s.ID); e != nil {
					if tp, ok := e.p.(plug.TrackPlugin); ok {
						tp.SetDocument(doc)
					}
				}
			}
		}
	}
	for id, sd := range a.docs {
		if !keep[id] {
			sd.doc.src.close()
			delete(a.docs, id)
			trackDocs.Delete(id)
		}
	}
}

// docKeyOf is what slot i of track t's source renders from: the track's
// file and how it is edited and spaced, and the plugins before the
// slot, each with its state.
func (a *app) docKeyOf(t *Track, i int) docKey {
	h := fnv.New64a()
	for _, c := range t.Chain[:i] {
		_, _ = fmt.Fprintf(h, "%d %s %s %v %d|", c.ID, c.Path, c.Class, c.Bypass, len(a.states[c.ID]))
		_, _ = h.Write(a.states[c.ID])
	}
	return docKey(fmt.Sprintf("%s|%d|%v|%+v|%x", t.File, t.Frames, a.gapOf(t), t.Edit, h.Sum64()))
}

// closeDocs lets every document go, as the app ends.
func (a *app) closeDocs() {
	for id, sd := range a.docs {
		sd.doc.src.close()
		delete(a.docs, id)
		trackDocs.Delete(id)
	}
}
