// Package trim is a plugin that turns the sound up or down: the first
// plugin in the [plug] format, as small as one can be while it has a
// parameter, a gesture to undo, and a view in a chip and a panel.
package trim

import (
	"errors"
	"fmt"
	"math"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/widget"
	"github.com/marrasen/mastering-studio/plug"
)

// GainID is the ID of the gain parameter, in decibels.
const GainID uint32 = 1

// Descriptor is the plugin, as it is registered.
var Descriptor = plug.Descriptor{
	ID:      "marrasen.trim",
	Name:    "Trim",
	Vendor:  "Marras",
	Version: "0.1.0",
	Kind:    "Fx|Tools",
	Params: []plug.Param{{
		ID: GainID, Name: "Gain", Unit: "dB",
		Min: -24, Max: 24, Default: 0,
		Flags:  plug.Automatable,
		Format: func(v float64) string { return fmt.Sprintf("%+.1f dB", v) },
	}},
	New: func(h plug.Host) plug.Plugin { return &plugin{host: h} },
}

func init() { plug.Register(Descriptor) }

// glide is how long the gain takes to get most of the way to a new
// setting, so a move makes no click.
const glide = 0.005

// plugin is one trim.
type plugin struct {
	host plug.Host
	proc processor
}

// Processor implements [plug.Plugin].
func (p *plugin) Processor() plug.Processor { return &p.proc }

// State implements [plug.Plugin]: a trim keeps nothing but its gain.
func (p *plugin) State() []byte { return nil }

// SetState implements [plug.Plugin]: it takes only an empty state.
func (p *plugin) SetState(b []byte) error {
	if len(b) > 0 {
		return errors.New("trim: a trim keeps no state")
	}
	return nil
}

// Close implements [plug.Plugin].
func (p *plugin) Close() {}

// View implements [plug.Viewer]: the gain in a chip, and a slider in a
// panel. In full, the host's own view does as well.
func (p *plugin) View(s plug.Size, h plug.Host) gunim.Node {
	switch s {
	case plug.Chip:
		return newChip(h)
	case plug.Panel:
		return newPanel(h)
	default:
		return nil
	}
}

// gainText writes v, in decibels, to show.
func gainText(v float64) string { return Descriptor.Params[0].Text(v) }

// processor turns the sound up or down, gliding to each new gain.
type processor struct {
	// gain is the gain now, as a factor, and target the one it glides
	// to; step is how much of the way it goes a frame.
	gain, target, step float64
	// fresh says no gain has come since Prepare: the first jumps.
	fresh bool
}

// Prepare implements [plug.Processor].
func (p *processor) Prepare(s plug.Setup) {
	p.step = 1 - math.Exp(-1/(s.Rate*glide))
	p.gain, p.target, p.fresh = 1, 1, true
}

// Process implements [plug.Processor]: each change takes effect from
// its frame on.
func (p *processor) Process(b *plug.Block) {
	next := 0
	for i := range b.Frames {
		for next < len(b.Changes) && b.Changes[next].Frame <= i {
			p.take(b.Changes[next])
			next++
		}
		p.gain += (p.target - p.gain) * p.step
		if math.Abs(p.target-p.gain) < 1e-7 {
			p.gain = p.target
		}
		g := float32(p.gain)
		for ch := range b.Out {
			b.Out[ch][i] = b.In[ch][i] * g
		}
	}
	for ; next < len(b.Changes); next++ {
		p.take(b.Changes[next])
	}
}

// take takes a change to the gain.
func (p *processor) take(c plug.Change) {
	if c.ID != GainID {
		return
	}
	p.target = math.Pow(10, Descriptor.Params[0].Clamp(c.Value)/20)
	if p.fresh {
		p.gain, p.fresh = p.target, false
	}
}

// Latency implements [plug.Processor]: the trim is never late.
func (p *processor) Latency() int { return 0 }

// Tail implements [plug.Processor]: the trim never rings.
func (p *processor) Tail() int { return 0 }

// Reset implements [plug.Processor]: the gain stops gliding, there.
func (p *processor) Reset() { p.gain = p.target }

// chip shows the gain, in a plugin's chip in a chain.
type chip struct{ *widget.Label }

func newChip(h plug.Host) *chip {
	l := widget.NewLabel(gainText(h.Param(GainID)))
	l.NoWrap, l.MaxLines = true, 1
	return &chip{l}
}

// Follow implements [plug.Follower].
func (c *chip) Follow(id uint32, v float64, u *gunim.UI) {
	if id == GainID {
		c.Text = gainText(v)
		u.Invalidate()
	}
}

// panel is a slider for the gain.
type panel struct {
	*widget.SliderRow
	// held says a gesture on the slider has begun with the host.
	held bool
}

func newPanel(h plug.Host) *panel {
	g := Descriptor.Params[0]
	s := widget.NewSlider(float32(g.Min), float32(g.Max))
	s.Snap, s.Rest, s.HasRest = 0.1, float32(g.Default), true
	s.SetValue(float32(h.Param(GainID)), nil)
	p := &panel{SliderRow: widget.NewSliderRow(g.Name, s)}
	p.Format = func(v float32) string { return gainText(float64(v)) }
	s.OnChange = func(v float32, u *gunim.UI) gunim.Intent {
		if !p.held {
			h.Begin(GainID, u)
			p.held = true
		}
		h.Set(GainID, float64(v), u)
		return nil
	}
	s.OnCommit = func(v float32, u *gunim.UI) gunim.Intent {
		if !p.held {
			h.Begin(GainID, u)
		}
		h.Set(GainID, float64(v), u)
		h.End(GainID, u)
		p.held = false
		return nil
	}
	return p
}

// Follow implements [plug.Follower]: the slider glides to a gain set
// elsewhere, unless it is held.
func (p *panel) Follow(id uint32, v float64, u *gunim.UI) {
	if id == GainID && !p.Slider.Held() {
		p.Slider.SetValue(float32(v), u)
	}
}
