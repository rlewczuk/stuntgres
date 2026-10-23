package stored

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

// These pure topology units complement the engine's genuine constructor and
// provider fixtures; no provider, registry or executable descriptor is faked.
func TestRangeTypeIntervalTopologyRecords(t *testing.T) {
	point := func(id ids.OID) catalog.AliasOrderEndpoint {
		return catalog.AliasOrderEndpoint{OID: id, Domain: sqltype.Regclass}
	}
	interval := func(lo, hi ids.OID, li, ui bool) aliasInterval {
		return aliasInterval{lower: point(lo), upper: point(hi), lowerFinite: true, upperFinite: true, lowerInclusive: li, upperInclusive: ui}
	}
	for _, tc := range []struct {
		name    string
		members []aliasInterval
		count   int
		points  []ids.OID
	}{
		{"reverse", []aliasInterval{interval(20, 30, true, true), interval(1, 10, true, true)}, 2, []ids.OID{1, 10, 20, 30}},
		{"containment", []aliasInterval{interval(10, 20, true, true), interval(1, 30, true, true)}, 1, []ids.OID{1, 10, 20, 30}},
		{"excluded_touch", []aliasInterval{interval(1, 10, true, false), interval(10, 20, false, true)}, 2, []ids.OID{1, 10, 10, 20}},
		{"included_touch", []aliasInterval{interval(1, 10, true, false), interval(10, 20, true, true)}, 1, []ids.OID{1, 10, 10, 20}},
		{"empty", []aliasInterval{{empty: true}}, 0, nil},
		{"unbounded_union", []aliasInterval{{upper: point(10), upperFinite: true, upperInclusive: true}, {lower: point(1), lowerFinite: true, lowerInclusive: true}}, 1, []ids.OID{1, 10}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			datum, known, err := finishAliasDatum(context.Background(), aliasIntervalDatum{multi: true, intervals: tc.members})
			if err != nil || !known || len(datum.intervals) != tc.count || len(datum.points) != len(tc.points) {
				t.Fatal("source topology", datum, known, err)
			}
			for i, p := range datum.points {
				if p.OID != tc.points[i] {
					t.Fatal("hidden source endpoint preorder", datum.points, tc.points)
				}
			}
			if tc.name == "reverse" && (datum.intervals[0].lower.OID != 1 || datum.intervals[1].lower.OID != 20) {
				t.Fatal("written order leaked into source topology", datum)
			}
			if tc.name == "containment" && (datum.intervals[0].lower.OID != 1 || datum.intervals[0].upper.OID != 30) {
				t.Fatal("contained source boundary changed union", datum)
			}
			if tc.name == "excluded_touch" && (datum.intervals[0].upperInclusive || datum.intervals[1].lowerInclusive) {
				t.Fatal("excluded common point was filled", datum)
			}
			if tc.name == "included_touch" && (!datum.intervals[0].lowerInclusive || !datum.intervals[0].upperInclusive) {
				t.Fatal("included outer bounds changed", datum)
			}
			if tc.name == "unbounded_union" && (datum.intervals[0].lowerFinite || datum.intervals[0].upperFinite) {
				t.Fatal("unbounded sides became finite", datum)
			}
		})
	}
	null, known, err := finishAliasDatum(context.Background(), aliasIntervalDatum{multi: true, null: true})
	if err != nil || !known || !null.null || len(null.intervals) != 0 || len(null.points) != 0 {
		t.Fatal("SQL NULL was not distinct from intervals", null, known, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := finishAliasDatum(ctx, aliasIntervalDatum{multi: true, intervals: []aliasInterval{interval(1, 2, true, true)}}); !errors.Is(err, context.Canceled) {
		t.Fatal("topology cancellation", err)
	}
}
