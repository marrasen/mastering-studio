package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"sync/atomic"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"

	// The Go plugins built in.
	_ "github.com/marrasen/gunim/audio/plug/dynamics"
	_ "github.com/marrasen/gunim/audio/plug/enhancer"
	_ "github.com/marrasen/gunim/audio/plug/eq"
	_ "github.com/marrasen/gunim/audio/plug/imager"
	_ "github.com/marrasen/gunim/audio/plug/maximizer"
	_ "github.com/marrasen/gunim/audio/plug/trim"
)

// The chain's half of the Go plugins, in package plug: built into the
// studio, each in a slot of Path goPath and Class its ID, run through a
// goEngine, and kept as their settings, a slot's state.

// goPath is the Path of a slot of a Go plugin.
const goPath = "go:"

// goPlugin says the slot is of a Go plugin.
func (s Slot) goPlugin() bool { return s.Path == goPath }

// goChoices are the Go plugins, to add to a chain.
func goChoices() []PluginChoice {
	ds := plug.All()
	out := make([]PluginChoice, 0, len(ds))
	for _, d := range ds {
		out = append(out, PluginChoice{Path: goPath, Class: d.ID, Name: d.Name, Vendor: d.Vendor, Kind: d.Kind})
	}
	return out
}

// goSettings are a Go plugin's settings, as its slot's state keeps them:
// its parameters' values, by ID, and its own state.
type goSettings struct {
	Params map[uint32]float64 `json:",omitempty"`
	State  []byte             `json:",omitempty"`
}

// readSettings reads b, as packed: nothing is no settings.
func readGoSettings(b []byte) (goSettings, error) {
	var s goSettings
	if len(b) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return goSettings{}, fmt.Errorf("the plugin's settings: %w", err)
	}
	return s, nil
}

func (s goSettings) pack() []byte {
	b, err := json.Marshal(s)
	if err != nil {
		// A map of numbers and bytes always encodes.
		panic(err)
	}
	return b
}

// values returns the value of each of d's parameters in s, clamped,
// or its default where s has none.
func (s goSettings) values(d *plug.Descriptor) map[uint32]float64 {
	out := map[uint32]float64{}
	for _, p := range d.Params {
		v, ok := s.Params[p.ID]
		if !ok {
			v = p.Default
		}
		out[p.ID] = p.Clamp(v)
	}
	return out
}

// goBlock is the most frames a Go plugin processes at once.
const goBlock = 512

// goEngine runs a Go plugin in a chain: its settings, its processor,
// and the blocks it processes, made of the chain's interleaved frames.
// As the processor must, it takes no lock and makes nothing new on the
// audio's goroutine: the parameters cross to it through atomics.
type goEngine struct {
	d    plug.Descriptor
	p    plug.Plugin
	proc plug.Processor
	// params are the parameters, in the descriptor's order.
	params []goParam
	// changes, chans and block are the audio goroutine's, for a block.
	changes []plug.Change
	chans   [][]float32
	block   plug.Block
	pos     int64
}

// goParam is a parameter of a Go plugin loaded: its value, in bits, and
// a count of its sets, which the audio's goroutine, having seen up to
// seen, takes as a change for the next block. Sets between two blocks
// come as one, of the last value.
type goParam struct {
	id   uint32
	bits atomic.Uint64
	sets atomic.Uint64
	seen uint64
}

func (p *goParam) value() float64 { return math.Float64frombits(p.bits.Load()) }

// set sets the value: its bits first, so the block that sees the count
// sees the value too.
func (p *goParam) set(v float64) {
	p.bits.Store(math.Float64bits(v))
	p.sets.Add(1)
}

// param returns parameter id, or nil.
func (e *goEngine) param(id uint32) *goParam {
	for i := range e.params {
		if e.params[i].id == id {
			return &e.params[i]
		}
	}
	return nil
}

// newGoPlugin loads slot s's Go plugin at rate, of state, offline or
// not.
func newGoPlugin(s Slot, state []byte, rate int, offline bool) (*livePlugin, error) {
	d, ok := plug.Lookup(s.Class)
	if !ok {
		return nil, fmt.Errorf("%s is not built into this studio", s.Name)
	}
	e := &goEngine{d: d, chans: [][]float32{make([]float32, goBlock), make([]float32, goBlock)},
		params: make([]goParam, len(d.Params)), changes: make([]plug.Change, 0, len(d.Params))}
	for i, p := range d.Params {
		e.params[i].id = p.ID
	}
	e.p = d.New(soundHost{e})
	e.proc = e.p.Processor()
	e.proc.Prepare(plug.Setup{Rate: float64(rate), MaxBlock: goBlock, Channels: 2, Offline: offline})
	if err := e.SetState(state); err != nil {
		e.p.Close()
		return nil, err
	}
	lp := &livePlugin{slot: s.ID, p: e, gain: 1}
	if s.Gained {
		lp.gain = float32(math.Pow(10, float64(s.Gain)/20))
	}
	for _, p := range d.Params {
		if p.Flags&plug.Bypass != 0 {
			lp.bypassID, lp.hasBypass = p.ID, true
		}
	}
	lp.setBypass(s.Bypass)
	return lp, nil
}

// setParam sets parameter id to v, from the next block on.
func (e *goEngine) setParam(id uint32, v float64) {
	p, ok := e.d.Param(id)
	if !ok {
		return
	}
	e.param(id).set(p.Clamp(v))
}

// Set implements [engine]: v runs from 0 to 1.
func (e *goEngine) Set(id uint32, v float64) {
	if p, ok := e.d.Param(id); ok {
		e.setParam(id, p.Plain(v))
	}
}

// State implements [engine]: the settings, packed.
func (e *goEngine) State() ([]byte, error) {
	s := goSettings{Params: map[uint32]float64{}}
	for i := range e.params {
		s.Params[e.params[i].id] = e.params[i].value()
	}
	s.State = e.p.State()
	return s.pack(), nil
}

// SetState implements [engine]: every parameter is set again, from the
// next block on.
func (e *goEngine) SetState(b []byte) error {
	s, err := readGoSettings(b)
	if err != nil {
		return err
	}
	if err := e.p.SetState(s.State); err != nil {
		return err
	}
	values := s.values(&e.d)
	for i := range e.params {
		e.params[i].set(values[e.params[i].id])
	}
	return nil
}

// Process implements [engine]: the frames, a block at a time, each
// channel apart.
func (e *goEngine) Process(frames []float32) {
	n := len(frames) / 2
	for at := 0; at < n; at += goBlock {
		k := min(goBlock, n-at)
		l, r := e.chans[0][:k], e.chans[1][:k]
		for i := range k {
			l[i], r[i] = frames[2*(at+i)], frames[2*(at+i)+1]
		}
		e.changes = e.changes[:0]
		for i := range e.params {
			p := &e.params[i]
			if n := p.sets.Load(); n != p.seen {
				p.seen = n
				e.changes = append(e.changes, plug.Change{ID: p.id, Value: p.value()})
			}
		}
		e.block = plug.Block{In: e.chans, Out: e.chans, Frames: k, Changes: e.changes,
			Position: plug.Position{Frame: e.pos, Playing: true}}
		e.proc.Process(&e.block)
		for i := range k {
			frames[2*(at+i)], frames[2*(at+i)+1] = l[i], r[i]
		}
		e.pos += int64(k)
	}
}

func (e *goEngine) Latency() int            { return e.proc.Latency() }
func (e *goEngine) Reset()                  { e.proc.Reset() }
func (e *goEngine) SetPosition(frame int64) { e.pos = frame }
func (e *goEngine) Close()                  { e.p.Close() }

// Flush, Edits and EditorOpen implement [engine]: a Go plugin's views
// are in the studio's own window, and tell of their changes as they
// make them.
func (e *goEngine) Flush()           {}
func (e *goEngine) Edits() uint64    { return 0 }
func (e *goEngine) EditorOpen() bool { return false }

// OpenEditor implements [engine]: the window opens a Go plugin's views.
func (e *goEngine) OpenEditor(string) error {
	return errors.New("a Go plugin's views open in the studio's window")
}

// soundHost is the host of a Go plugin loaded to make sound: it shows no
// views, so it makes no gestures.
type soundHost struct{ e *goEngine }

func (h soundHost) Param(id uint32) float64 {
	if p := h.e.param(id); p != nil {
		return p.value()
	}
	return 0
}

func (soundHost) Begin(uint32, *gunim.UI)        {}
func (soundHost) Set(uint32, float64, *gunim.UI) {}
func (soundHost) End(uint32, *gunim.UI)          {}
func (soundHost) BeginEdit(string, *gunim.UI)    {}
func (soundHost) Changed(*gunim.UI)              {}
func (soundHost) EndEdit(*gunim.UI)              {}

// Sound implements [plug.Host]: the sound's own copy draws nothing.
func (soundHost) Sound() (in, out plug.Sound) { return nil, nil }

// Traced implements [plug.Host]: the sound's copy keeps no views.
func (soundHost) Traced(uint32, int64) (float32, bool) { return 0, false }

// goEngineOf returns slot id's Go plugin, loaded on track t, or nil.
func (a *app) goEngineOf(t *Track, id int) *goEngine {
	r := a.racks[t.ID]
	if r == nil {
		return nil
	}
	if lp := r.find(id); lp != nil {
		if e, ok := lp.p.(*goEngine); ok {
			return e
		}
	}
	return nil
}

// setGoParam sets parameter id of slot sid's Go plugin, on track tid,
// to v: in its settings, and in the plugin loaded.
func (a *app) setGoParam(tid, sid int, id uint32, v float64) {
	t, i := a.slot(tid, sid)
	if i < 0 || !t.Chain[i].goPlugin() {
		return
	}
	d, ok := plug.Lookup(t.Chain[i].Class)
	if !ok {
		return
	}
	p, ok := d.Param(id)
	if !ok {
		return
	}
	s, err := readGoSettings(a.states[sid])
	if err != nil {
		a.Note = err.Error()
		return
	}
	// A state is replaced, never written over: a step of the history
	// may hold it.
	s.Params = maps.Clone(s.Params)
	if s.Params == nil {
		s.Params = map[uint32]float64{}
	}
	s.Params[id] = p.Clamp(v)
	a.states[sid] = s.pack()
	if e := a.goEngineOf(t, sid); e != nil {
		e.setParam(id, s.Params[id])
	}
	a.dirty = true
	a.remeasureChain(t.ID)
}

// setGoState sets the state of slot sid's Go plugin, on track tid, to
// st: in its settings, and in the plugin loaded.
func (a *app) setGoState(tid, sid int, st []byte) {
	t, i := a.slot(tid, sid)
	if i < 0 || !t.Chain[i].goPlugin() {
		return
	}
	s, err := readGoSettings(a.states[sid])
	if err != nil {
		a.Note = err.Error()
		return
	}
	s.State = st
	if e := a.goEngineOf(t, sid); e != nil {
		if err = e.p.SetState(st); err != nil {
			a.Note = fmt.Sprintf("%s: %v", t.Chain[i].title(), err)
			return
		}
	}
	a.states[sid] = s.pack()
	a.dirty = true
	a.remeasureChain(t.ID)
}

// showGoSettings gives each slot of a Go plugin its settings, for the
// window to show.
func (a *app) showGoSettings() {
	for _, ts := range [][]Track{a.Tracks, a.References} {
		for k := range ts {
			for i := range ts[k].Chain {
				s := &ts[k].Chain[i]
				if !s.goPlugin() {
					continue
				}
				d, ok := plug.Lookup(s.Class)
				if !ok {
					continue
				}
				set, err := readGoSettings(a.states[s.ID])
				if err != nil {
					continue
				}
				v := set.values(&d)
				// Its own bypass, as the chain's light has it: the
				// plugin is told at once, its state only as it is next
				// read, which would leave the views a while behind.
				for _, p := range d.Params {
					if p.Flags&plug.Bypass != 0 {
						v[p.ID] = 0
						if s.Bypass {
							v[p.ID] = 1
						}
					}
				}
				// Written only as they change: the window holds the
				// album as last published.
				if !maps.Equal(v, s.Values) {
					s.Values = v
				}
				if !bytes.Equal(set.State, s.GoState) {
					s.GoState = set.State
				}
			}
		}
	}
}
