package plug

import (
	"math"
	"strings"
	"testing"
)

func TestParamValuesStayInRangeAndOnSteps(t *testing.T) {
	p := Param{Min: -24, Max: 24, Steps: 4}
	for _, c := range []struct{ in, want float64 }{{-30, -24}, {30, 24}, {5, 0}, {7, 12}, {-13, -12}} {
		if got := p.Clamp(c.in); got != c.want {
			t.Errorf("Clamp(%g) = %g, want %g", c.in, got, c.want)
		}
	}
	smooth := Param{Min: 20, Max: 20000}
	if n := smooth.Normal(10010); math.Abs(n-0.5) > 1e-9 {
		t.Errorf("Normal halfway = %g, want 0.5", n)
	}
	if v := smooth.Plain(smooth.Normal(440)); math.Abs(v-440) > 1e-9 {
		t.Errorf("Plain(Normal(440)) = %g", v)
	}
	if s := (Param{Unit: "dB"}).Text(-1.5); s != "-1.50 dB" {
		t.Errorf("Text = %q", s)
	}
}

func TestRegisterRefusesMistakes(t *testing.T) {
	newer := func(Host) Plugin { return nil }
	for name, d := range map[string]Descriptor{
		"no ID":       {Name: "X", New: newer},
		"no New":      {ID: "test.nonew", Name: "X"},
		"two params":  {ID: "test.two", Name: "X", New: newer, Params: []Param{{ID: 1, Max: 1}, {ID: 1, Max: 1}}},
		"empty range": {ID: "test.range", Name: "X", New: newer, Params: []Param{{ID: 1}}},
		"default out": {ID: "test.def", Name: "X", New: newer, Params: []Param{{ID: 1, Max: 1, Default: 2}}},
	} {
		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("%s: Register took it", name)
				}
			}()
			Register(d)
		}()
	}
}

func TestRegisteredPluginsAreListedAndFound(t *testing.T) {
	newer := func(Host) Plugin { return nil }
	Register(Descriptor{ID: "test.b", Name: "Bee", Vendor: "zed", New: newer})
	Register(Descriptor{ID: "test.a", Name: "Ay", Vendor: "Zed", New: newer})
	if _, ok := Lookup("test.a"); !ok {
		t.Fatal("Lookup did not find test.a")
	}
	var names []string
	for _, d := range All() {
		if strings.HasPrefix(d.ID, "test.") {
			names = append(names, d.Name)
		}
	}
	if strings.Join(names, ",") != "Ay,Bee" {
		t.Errorf("All lists %v, want Ay then Bee", names)
	}
	defer func() {
		if recover() == nil {
			t.Error("a second test.a was registered")
		}
	}()
	Register(Descriptor{ID: "test.a", Name: "Again", New: newer})
}
