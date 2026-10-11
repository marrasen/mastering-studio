package eq

// The look of the graph behind the list of bands: how dark it goes, so
// the rows read over it; how far each row's card of glass blurs what is
// behind it; how much of the card's own colour shows; and how bright the
// curve and the points stay over the dark. The list's tuner turns them,
// for the window's while it runs; defaultLook is where each starts, and
// where Reset puts it back.
type backdropLook struct {
	Dark, Blur, Card, Curve float64
}

var defaultLook = backdropLook{Dark: 0.6, Blur: 8, Card: 0.55, Curve: 1}

// look is the look now. It is the window's, read and set on its
// goroutine alone.
var look = defaultLook
