package reference_test

import (
	"bytes"
	"context"
	"stuntgres"
	"testing"
)

func TestReferenceBetweenPredicateDefaultRoundTrip(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			scalarNativeExec(t, db, "CREATE TABLE predicates(n BOOL DEFAULT (TRUE BETWEEN (NULL=TRUE) AND TRUE),i BOOL DEFAULT ((NULL IS NULL) BETWEEN FALSE AND TRUE),t BOOL DEFAULT ((TRUE IS TRUE) BETWEEN FALSE AND TRUE),d BOOL DEFAULT ((1 IS DISTINCT FROM 2) BETWEEN FALSE AND TRUE))")
			scalarNativeExec(t, db, "INSERT INTO predicates DEFAULT VALUES")
			scalarNativeTuples(t, db, "SELECT * FROM predicates", [][]any{{nil, true, true, true}})
			if !backend.pg {
				var dump bytes.Buffer
				ctx := context.Background()
				if err := stuntgres.Dump(ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(ctx, p.native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			scalarNativeExec(t, db, "INSERT INTO predicates DEFAULT VALUES")
			scalarNativeTuples(t, db, "SELECT * FROM predicates", [][]any{{nil, true, true, true}, {nil, true, true, true}})
		})
	}
}
