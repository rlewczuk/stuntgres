package reference_test

import (
	"bytes"
	"context"
	"stuntgres"
	"testing"
)

func TestReferenceBetweenGroupedDefaultRoundTrip(t *testing.T) {
	ctx := context.Background()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			scalarNativeExec(t, db, "CREATE TABLE sentinel(i INTEGER)")
			scalarNativeExec(t, db, "INSERT INTO sentinel VALUES(7)")
			scalarNativeExec(t, db, "CREATE TABLE grouped_defaults(b BOOL DEFAULT (TRUE BETWEEN (1<2) AND (2<3)),a BOOL DEFAULT ((TRUE OR FALSE) BETWEEN FALSE AND TRUE),s BOOL DEFAULT (TRUE BETWEEN SYMMETRIC TRUE AND (2 BETWEEN 1 AND 3)),n BOOL DEFAULT (TRUE NOT BETWEEN SYMMETRIC (FALSE OR TRUE) AND (1 BETWEEN 0 AND 2)),o BOOL DEFAULT (TRUE NOT BETWEEN (1 BETWEEN 0 AND 2) AND TRUE))")
			scalarNativeExec(t, db, "CREATE TABLE deferred(b BOOL DEFAULT (TRUE BETWEEN (1/0=0) AND TRUE))")
			scalarNativeExec(t, db, "INSERT INTO grouped_defaults DEFAULT VALUES")
			want := [][]any{{true, true, true, false, false}}
			scalarNativeTuples(t, db, "SELECT * FROM grouped_defaults", want)
			if !backend.pg {
				var dump bytes.Buffer
				if err := stuntgres.Dump(ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(ctx, p.native); err != nil {
					t.Fatal(err)
				}
				scalarNativeExec(t, db, "CREATE TABLE sentinel(i INTEGER)")
				scalarNativeExec(t, db, "INSERT INTO sentinel VALUES(7)")
				if err := stuntgres.Restore(ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
					scalarNativeTuples(t, db, "SELECT i FROM sentinel", [][]any{{int64(7)}})
					t.Fatal("grouped BETWEEN dump cannot be restored", err)
				}
				scalarNativeTuples(t, db, "SELECT * FROM grouped_defaults", want)
			}
			scalarNativeExec(t, db, "INSERT INTO grouped_defaults DEFAULT VALUES")
			scalarNativeTuples(t, db, "SELECT * FROM grouped_defaults", [][]any{{true, true, true, false, false}, {true, true, true, false, false}})
			scalarNativeTuples(t, db, "SELECT i FROM sentinel", [][]any{{int64(7)}})
			_, err := db.Exec("INSERT INTO deferred DEFAULT VALUES")
			referenceState(t, err, "22012")
			referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"})
			scalarNativeTuples(t, db, "SELECT * FROM deferred", [][]any{})
			scalarNativeTuples(t, db, "SELECT i FROM sentinel", [][]any{{int64(7)}})
		})
	}
}
