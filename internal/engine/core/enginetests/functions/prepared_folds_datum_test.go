package functions_test

import (
	"testing"

	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func TestPreparedFoldNaNStableGeneration(t *testing.T) {
	e := rootengine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, typ := range []string{"real", "double precision"} {
		t.Run(typ, func(t *testing.T) {
			ast, d := enginetest.PreparedDescription(t, e, s, "SELECT abs('NaN'::"+typ+") AS v")
			enginetest.PreparedFoldExecute(t, e, s, ast, &d)
			before := enginetest.SnapshotFoldDescription(d)
			if len(before.Accepted.Entries) == 0 {
				t.Fatal("no retained NaN artifact")
			}
			enginetest.PreparedFoldExecute(t, e, s, ast, &d)
			if d.Folds.Current().Generation != before.Accepted.Generation || &d.Folds.Current().Entries[0] != &before.Accepted.Entries[0] {
				t.Fatal("unchanged NaN replaced generation/backing")
			}
		})
	}
}
