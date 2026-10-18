package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/ids"
)

// Selection occurrence identity is VersionID, not RowID. A MERGE descendant
// based on an invisible peer head coexists with the retained snapshot version.
func TestMergeUpdateCaptureBaseOverlay(t *testing.T) {
	for _, multiple := range []bool{false, true} {
		for _, matching := range []bool{false, true} {
			name := "singleMismatch"
			if multiple {
				name = "multipleMismatch"
			}
			if matching {
				name += "MatchingControl"
			}
			t.Run(name, func(t *testing.T) {
				captured := []rowVersion{{row: 1, version: 10, born: 1}, {row: 2, version: 20, born: 1}}
				before := append([]rowVersion(nil), captured...)
				retained, err := selectQuerySource(context.Background(), &querySource{committed: captured}, 1, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer retained.close()
				base := ids.VersionID(11)
				if matching {
					base = 10
				}
				own := []rowVersion{{row: 1, version: 100, base: base}}
				if multiple {
					own = append(own, rowVersion{row: 2, version: 200, base: 20})
				}
				own = append(own, rowVersion{row: 3, version: 300})
				selected, err := selectQuerySource(context.Background(), &querySource{committed: captured, own: own}, 1, &deletionRoot{rows: map[ids.RowID]rowEnd{1: {version: 10, end: 2}}}, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer selected.close()
				var got []ids.VersionID
				var identity []ids.RowID
				for i := range selected.count() {
					v := selected.versionAt(selected.rawIndex(i))
					got = append(got, v.version)
					identity = append(identity, v.row)
				}
				second := ids.VersionID(20)
				if multiple {
					second = 200
				}
				want := []ids.VersionID{10, second, 100, 300}
				if matching {
					want = []ids.VersionID{100, second, 300}
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatal("canonical version bag", got, want, identity)
				}
				if !matching && (identity[0] != 1 || identity[2] != 1 || own[0].base != 11) {
					t.Fatal("base mismatch lost logical identity", identity, own)
				}
				if !reflect.DeepEqual(captured, before) || retained.count() != 2 || retained.versionAt(retained.rawIndex(0)).version != 10 {
					t.Fatal("mutated retained prefix", captured, retained)
				}
			})
		}
	}
}

func TestMergeUpdateCaptureCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	source, err := selectQuerySource(ctx, &querySource{committed: []rowVersion{{row: 1, version: 10, born: 1}}, own: []rowVersion{{row: 1, version: 100, base: 11}}}, 1, nil, nil)
	if source != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("capture escaped cancellation", source, err)
	}
}
