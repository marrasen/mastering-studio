package main

import (
	"errors"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/widget"
	"github.com/marrasen/mastering-studio/plug"
	"github.com/marrasen/mastering-studio/plug/trim"
)

// A plugin of the tests' own that reads whole tracks, and counts the
// documents it is given.
func init() {
	plug.Register(plug.Descriptor{ID: "test.reader", Name: "Reader", Vendor: "Tests",
		New: func(plug.Host) plug.Plugin { return &trackReader{} }})
}

type trackReader struct {
	keeper
	docs []plug.Document
	pane *readerPane
}

func (r *trackReader) SetDocument(d plug.Document) { r.docs = append(r.docs, d) }
func (r *trackReader) Analysed() float64           { return 0 }

// View implements [plug.Viewer]: a pane that counts what it is told.
func (r *trackReader) View(s plug.Size, _ plug.Host) gunim.Node {
	if s != plug.Panel {
		return nil
	}
	r.pane = &readerPane{Label: widget.NewLabel("Reader")}
	return r.pane
}

// readerPane is the reader's pane: it counts the states and documents
// it is told of.
type readerPane struct {
	*widget.Label
	states, docs int
}

func (p *readerPane) FollowState(*gunim.UI)    { p.states++ }
func (p *readerPane) FollowDocument(*gunim.UI) { p.docs++ }

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

// docSource returns slot id's source, as the app keeps it.
func docSource(t *testing.T, a *app, id int) *renderedSource {
	t.Helper()
	sd := a.docs[id]
	if sd == nil {
		t.Fatalf("slot %d has no document", id)
	}
	return sd.doc.src
}

// readAt reads n frames of src from at.
func readAt(t *testing.T, src plug.Source, at int64, n int) [][]float32 {
	t.Helper()
	dst := [][]float32{make([]float32, n), make([]float32, n)}
	got, err := src.ReadAt(dst, at)
	if err != nil || got != n {
		t.Fatalf("reading %d frames from %d read %d: %v", n, at, got, err)
	}
	return dst
}

func TestEachSlotIsGivenTheTrackAsItReachesIt(t *testing.T) {
	a, tr, s := goApp(t)
	t.Cleanup(a.closeDocs)
	tr.File, tr.Frames = writeNoise(t, 2, 0.2), 2*rate
	tr.Format.SampleRate = rate
	a.handle(SetGap{})
	a.handle(AddPlugin{Track: tr.ID, Choice: PluginChoice{Path: goPath, Class: "test.reader", Name: "Reader"}})
	after := tr.Chain[1].ID
	a.handle(SetGoParam{Track: tr.ID, Slot: s.ID, ID: trim.GainID, Value: -6, Gesture: 1})
	a.syncDocs()
	first, second := docSource(t, a, s.ID), docSource(t, a, after)
	if first.Frames() != 2*rate || second.Rate() != rate || first.Channels() != 2 {
		t.Fatalf("the sources are %d frames at %v Hz", first.Frames(), second.Rate())
	}
	// Read anywhere, out of order: the second slot is fed the track
	// through the trim, 6 dB down.
	for _, at := range []int64{50000, 0, 70000, 3} {
		in, out := readAt(t, first, at, 1000), readAt(t, second, at, 1000)
		for i := range 1000 {
			if want := in[0][i] * float32(math.Pow(10, -6.0/20)); math.Abs(float64(out[0][i]-want)) > 1e-4 {
				t.Fatalf("frame %d of the second slot's source is %v, want the first's %v less 6 dB, %v", at+int64(i),
					out[0][i], in[0][i], want)
			}
		}
	}
	// Past the end, nothing.
	if n, err := second.ReadAt([][]float32{make([]float32, 10)}, second.Frames()); n != 0 || err == nil {
		t.Errorf("past the end the source read %d (%v)", n, err)
	}
	// The plugin loaded for the sound was given its document.
	if e := a.goEngineOf(tr, after); e == nil {
		t.Error("the reader is not loaded for the sound")
	} else if rd, ok := e.p.(*trackReader); !ok || len(rd.docs) == 0 {
		t.Error("the reader loaded for the sound was given no document")
	}
	// The trim changed: the second slot's track is new, the first's not.
	a.handle(SetGoParam{Track: tr.ID, Slot: s.ID, ID: trim.GainID, Value: -3, Gesture: 2})
	a.syncDocs()
	if docSource(t, a, s.ID) != first || docSource(t, a, after).Version() != 2 {
		t.Fatalf("after the trim changed, the sources are versions %d and %d", docSource(t, a, s.ID).Version(),
			docSource(t, a, after).Version())
	}
	// The old version is let go: no more to read, and its file gone.
	if _, err := second.ReadAt([][]float32{make([]float32, 10)}, 0); !errors.Is(err, errSourceGone) {
		t.Errorf("a source let go reads with %v", err)
	}
	name := second.file.Name()
	a.handle(RemovePlugin{Track: tr.ID, Slot: after})
	a.syncDocs()
	if _, ok := trackDocs.Load(after); ok || a.docs[after] != nil {
		t.Error("the document of a slot gone stays")
	}
	waitGone(t, name)
}

// waitGone waits a moment for the file at name to go.
func waitGone(t *testing.T, name string) {
	t.Helper()
	for range 200 {
		if _, err := os.Stat(name); os.IsNotExist(err) {
			return
		}
		sleepBriefly()
	}
	t.Errorf("the file %s stays", name)
}

func TestTheViewsOfAPluginThatReadsTracksAreGivenItsDocumentOnce(t *testing.T) {
	al := goAlbum(0)
	al.Tracks[0].Chain = append(al.Tracks[0].Chain, Slot{ID: 12, Path: goPath, Class: "test.reader", Name: "Reader"})
	doc := &trackDoc{src: &renderedSource{version: 1}}
	trackDocs.Store(12, doc)
	t.Cleanup(func() { trackDocs.Delete(12) })
	w, r, run := stage(t, al)
	v := r.goViews[12]
	if v == nil {
		t.Fatal("the reader has no views")
	}
	rd, ok := v.p.(*trackReader)
	if !ok || len(rd.docs) != 1 || rd.docs[0] != doc {
		t.Fatalf("the reader's views were given %v", rd.docs)
	}
	if err := w.Client().Publish(albumTopic, al); err != nil {
		t.Fatal(err)
	}
	run(2)
	if len(rd.docs) != 1 {
		t.Errorf("the same document was given %d times", len(rd.docs))
	}
	// The pane, as it opens, is told the state once; a new document, it
	// is told of.
	al.ShowGo, al.ShowGos = 12, 1
	if err := w.Client().Publish(albumTopic, al); err != nil {
		t.Fatal(err)
	}
	run(2)
	if rd.pane == nil || rd.pane.states != 1 {
		t.Fatalf("opened, the reader's pane was told the state %v times", rd.pane)
	}
	trackDocs.Store(12, &trackDoc{src: &renderedSource{version: 2}})
	if err := w.Client().Publish(albumTopic, al); err != nil {
		t.Fatal(err)
	}
	run(2)
	if len(rd.docs) != 2 || rd.pane.docs != 1 {
		t.Errorf("a new document was given %d times all told, and the pane told of %d", len(rd.docs), rd.pane.docs)
	}
}

func sleepBriefly() { time.Sleep(5 * time.Millisecond) }
