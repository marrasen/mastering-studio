package main

import (
	"time"

	"github.com/marrasen/gunim/icon"
)

// Carry is where a track picked while another plays starts: as far
// through it as the other was, at the same moment, or at its start.
type Carry int

const (
	// CarryPercent starts it as far through as the other was.
	CarryPercent Carry = iota
	// CarryTime starts it at the same moment, or at its start where it
	// is shorter.
	CarryTime
	// CarryStart starts it at its start.
	CarryStart
	carries
)

// SetCarry sets where a track picked while another plays starts.
type SetCarry struct{ Carry Carry }

// carryLooks are each way's icon, and what its button says it does.
var carryLooks = [carries]struct {
	icon *icon.Icon
	tip  string
}{
	{icon.Percent, "Switching tracks: as far through"},
	{icon.Clock, "Switching tracks: at the same time"},
	{icon.ArrowLeftToLine, "Switching tracks: from the start"},
}

// carried is where in track to the moment at of track from carries to,
// as Carry says, in the render's time: never past its end, where it
// would end at once.
func (a *app) carried(from, to *Track, at time.Duration) time.Duration {
	toLen := lengthOf(*to, a.gapOf(to))
	switch a.Carry {
	case CarryStart:
		return 0
	case CarryPercent:
		if from == nil {
			return 0
		}
		if fromLen := lengthOf(*from, a.gapOf(from)); fromLen > 0 {
			at = time.Duration(float64(at) / float64(fromLen) * float64(toLen))
		}
	case CarryTime, carries:
	}
	if at >= toLen {
		return 0
	}
	return at
}
