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
				got, err := v.CheckEarthCube(EarthCheck(i + 1))
				if err != nil || got != want {
					t.Fatalf("kind%d got %v/%v want %v", i+1, got, err, want)
				}
			}
			if _, err := v.CheckEarthCube(0); err == nil {
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
			got, err := v.CheckEarthCube(EarthOnSurface)
			if err != nil || got != (i == 0) {
				t.Fatalf("adjacent %v got %v/%v", x, got, err)
			}
		}
	}
	if _, err := Null(d).CheckEarthCube(EarthOnSurface); err == nil {
		t.Fatal("physical NULL check bypass")
	}
}
