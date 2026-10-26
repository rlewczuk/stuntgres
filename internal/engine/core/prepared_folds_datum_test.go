package core_test

import (
	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"testing"
)

func TestPreparedFoldNaNStableGeneration(t *testing.T) {
	e := rootengine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, typ := range []string{"real", "double precision"} {
		t.Run(typ, func(t *testing.T) {
			ast, d := sequencePreparedDescription(t, e, s, "SELECT abs('NaN'::"+typ+") AS v")
			preparedFoldExecute(t, e, s, ast, &d)
			before := snapshotFoldDescription(d)
			if len(before.Accepted.Entries) == 0 {
				t.Fatal("no retained NaN artifact")
			}
			preparedFoldExecute(t, e, s, ast, &d)
			if d.Folds.Current().Generation != before.Accepted.Generation || &d.Folds.Current().Entries[0] != &before.Accepted.Entries[0] {
				t.Fatal("unchanged NaN replaced generation/backing")
			}
		})
	}
}
