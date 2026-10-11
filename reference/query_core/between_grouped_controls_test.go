package reference_test

import (
	"bytes"
	"context"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBetweenGroupedDefaultRoundTrip(t *testing.T) {
	ctx := context.Background()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			fixture.ScalarNativeExec(t, db, "CREATE TABLE sentinel(i INTEGER)")
			fixture.ScalarNativeExec(t, db, "INSERT INTO sentinel VALUES(7)")
			fixture.ScalarNativeExec(t, db, "CREATE TABLE grouped_defaults(b BOOL DEFAULT (TRUE BETWEEN (1<2) AND (2<3)),a BOOL DEFAULT ((TRUE OR FALSE) BETWEEN FALSE AND TRUE),s BOOL DEFAULT (TRUE BETWEEN SYMMETRIC TRUE AND (2 BETWEEN 1 AND 3)),n BOOL DEFAULT (TRUE NOT BETWEEN SYMMETRIC (FALSE OR TRUE) AND (1 BETWEEN 0 AND 2)),o BOOL DEFAULT (TRUE NOT BETWEEN (1 BETWEEN 0 AND 2) AND TRUE))")
			fixture.ScalarNativeExec(t, db, "CREATE TABLE deferred(b BOOL DEFAULT (TRUE BETWEEN (1/0=0) AND TRUE))")
			fixture.ScalarNativeExec(t, db, "INSERT INTO grouped_defaults DEFAULT VALUES")
			want := [][]any{{true, true, true, false, false}}
			fixture.ScalarNativeTuples(t, db, "SELECT * FROM grouped_defaults", want)
			if !backend.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				fixture.ScalarNativeExec(t, db, "CREATE TABLE sentinel(i INTEGER)")
				fixture.ScalarNativeExec(t, db, "INSERT INTO sentinel VALUES(7)")
				if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					fixture.ScalarNativeTuples(t, db, "SELECT i FROM sentinel", [][]any{{int64(7)}})
					t.Fatal("grouped BETWEEN dump cannot be restored", err)
				}
				fixture.ScalarNativeTuples(t, db, "SELECT * FROM grouped_defaults", want)
			}
			fixture.ScalarNativeExec(t, db, "INSERT INTO grouped_defaults DEFAULT VALUES")
			fixture.ScalarNativeTuples(t, db, "SELECT * FROM grouped_defaults", [][]any{{true, true, true, false, false}, {true, true, true, false, false}})
			fixture.ScalarNativeTuples(t, db, "SELECT i FROM sentinel", [][]any{{int64(7)}})
			_, err := db.Exec("INSERT INTO deferred DEFAULT VALUES")
			fixture.State(t, err, "22012")
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"})
			fixture.ScalarNativeTuples(t, db, "SELECT * FROM deferred", [][]any{})
			fixture.ScalarNativeTuples(t, db, "SELECT i FROM sentinel", [][]any{{int64(7)}})
		})
	}
}
