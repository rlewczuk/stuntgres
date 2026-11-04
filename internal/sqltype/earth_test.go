package sqltype

import (
	"context"
	"math"
	"strconv"
	"testing"
)

func TestEarthCubeChecks(t *testing.T) {
	d, err := Cube(48001, "cube", "public.cube")
	if err != nil {
		t.Fatal(err)
	}
	origin, err := d.Input(context.Background(), "(0)", -1)
	if err != nil {
		t.Fatal(err)
	}
	if !origin.IsEarthCubeOrigin(d) {
		t.Fatal("canonical source origin rejected")
	}
	for _, input := range []string{"()", "(1)", "(0,0)", "(NaN)"} {
		bad, err := d.Input(context.Background(), input, -1)
		if err != nil {
			t.Fatal(err)
		}
		if bad.IsEarthCubeOrigin(d) {
			t.Fatal("noncanonical source origin accepted", input)
		}
		if _, err := origin.EarthCubeCheckLeaf(EarthOnSurface, bad); err == nil {
			t.Fatal("distance leaf accepted a substituted source origin", input)
		}
	}
	foreign, err := Cube(48002, "cube", "foreign.cube")
	if err != nil {
		t.Fatal(err)
	}
	foreignOrigin, err := foreign.Input(context.Background(), "(0)", -1)
	if err != nil {
		t.Fatal(err)
	}
	if foreignOrigin.IsEarthCubeOrigin(d) {
		t.Fatal("foreign generation became a source origin")
	}
	for _, tc := range []struct {
		input                      string
		point, dimensions, surface bool
	}{
		{"()", true, true, false}, {"6378168", true, true, true},
		{"(6378168,0)", true, true, true}, {"(6378168,0,0)", true, true, true},
		{"(6378168,0,0,0)", true, false, true},
		{"(0),(1)", false, true, false},
		{"[(-6378168,-0),(-6378168,0)]", true, true, true},
		{"(6378168,NaN)", true, true, true}, {"(6378168,0,NaN)", true, true, true},
		{"(NaN)", true, true, false}, {"Infinity", true, true, false},
		{"(6378168,NaN),(6378168,NaN)", false, true, true},
		{"6378162", true, true, true}, {"6378174", true, true, true},
		{"6378161", true, true, false}, {"6378175", true, true, false},
	} {
		t.Run(tc.input, func(t *testing.T) {
			v, err := d.Input(context.Background(), tc.input, -1)
			if err != nil {
				t.Fatal(err)
			}
			for i, want := range []bool{tc.point, tc.dimensions, tc.surface} {
				kind := EarthCheck(i + 1)
				leaf, err := v.EarthCubeCheckLeaf(kind, origin)
				got := kind.MatchLeaf(leaf)
				if err != nil || got != want {
					t.Fatalf("kind%d got %v/%v want %v", i+1, got, err, want)
				}
			}
			if _, err := v.EarthCubeCheckLeaf(0, origin); err == nil {
				t.Fatal("invalid kind accepted")
			}
		})
	}
	for _, pair := range [][2]float64{{6378161.621832001, math.Nextafter(6378161.621832001, math.Inf(-1))}, {6378174.378168, math.Nextafter(6378174.378168, math.Inf(1))}} {
		for i, x := range pair {
			v, err := d.Input(context.Background(), strconv.FormatFloat(x, 'g', -1, 64), -1)
			if err != nil {
				t.Fatal(err)
			}
			leaf, err := v.EarthCubeCheckLeaf(EarthOnSurface, origin)
			got := EarthOnSurface.MatchLeaf(leaf)
			if err != nil || got != (i == 0) {
				t.Fatalf("adjacent %v got %v/%v", x, got, err)
			}
		}
	}
	if _, err := Null(d).EarthCubeCheckLeaf(EarthOnSurface, origin); err == nil {
		t.Fatal("physical NULL check bypass")
	}
}
