package reference_test

import (
	"bytes"
	"context"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBetweenPredicateDefaultRoundTrip(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			fixture.ScalarNativeExec(t, db, "CREATE TABLE predicates(n BOOL DEFAULT (TRUE BETWEEN (NULL=TRUE) AND TRUE),i BOOL DEFAULT ((NULL IS NULL) BETWEEN FALSE AND TRUE),t BOOL DEFAULT ((TRUE IS TRUE) BETWEEN FALSE AND TRUE),d BOOL DEFAULT ((1 IS DISTINCT FROM 2) BETWEEN FALSE AND TRUE))")
			fixture.ScalarNativeExec(t, db, "INSERT INTO predicates DEFAULT VALUES")
			fixture.ScalarNativeTuples(t, db, "SELECT * FROM predicates", [][]any{{nil, true, true, true}})
			if !backend.PG {
				var dump bytes.Buffer
				ctx := context.Background()
				if err := stuntgres.Dump(ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			fixture.ScalarNativeExec(t, db, "INSERT INTO predicates DEFAULT VALUES")
			fixture.ScalarNativeTuples(t, db, "SELECT * FROM predicates", [][]any{{nil, true, true, true}, {nil, true, true, true}})
		})
	}
}
