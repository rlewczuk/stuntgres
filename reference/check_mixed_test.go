package reference_test

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"
)

// Allocated relation identities are checked independently on each backend.
// The failed CREATE publishes no relation, so no portable lookup can recover it.
func TestReferenceCheckMixedConstraintDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, query, constraint string }{
		{"explicit", "CREATE TABLE t(a INT CONSTRAINT c NOT NULL,CONSTRAINT c CHECK(TRUE))", "c"},
		{"generated", "CREATE TABLE t(a INT CONSTRAINT t_a_check NOT NULL CHECK(a>0))", "t_a_check"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.name+strconv.FormatBool(prepared), func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					db := b.db
					ctx := context.WithValue(p.ctx, referenceRecorderKey{}, b.recorder)
					var result sql.Result
					var err error
					if prepared {
						stmt, e := db.PrepareContext(ctx, tc.query)
						if e != nil {
							t.Fatal(e)
						}
						result, err = stmt.ExecContext(ctx)
						stmt.Close()
					} else {
						result, err = db.ExecContext(ctx, tc.query)
					}
					if result != nil {
						t.Fatal("failed CREATE exposed result")
					}
					got := referenceExtractDiagnostic(t, err)
					referenceState(t, err, "23505")
					prefix := "Key (conrelid, contypid, conname)=("
					suffix := ", 0, " + tc.constraint + ") already exists."
					if !strings.HasPrefix(got.Detail, prefix) || !strings.HasSuffix(got.Detail, suffix) {
						t.Fatalf("invalid key detail: %q", got.Detail)
					}
					id := strings.TrimSuffix(strings.TrimPrefix(got.Detail, prefix), suffix)
					oid, e := strconv.ParseUint(id, 10, 32)
					if e != nil || oid == 0 || strconv.FormatUint(oid, 10) != id {
						t.Fatalf("invalid allocated OID %q", id)
					}
					want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "pg_constraint_conrelid_contypid_conname_index"`, Detail: prefix + id + suffix, Schema: "pg_catalog", Table: "pg_constraint", Constraint: "pg_constraint_conrelid_contypid_conname_index"}
					referenceAssertDiagnostic(t, err, &want)
					referenceCheckRun(t, ctx, db, b.recorder, referenceExec("CREATE TABLE t(a INT)", 0))
					referenceCheckRun(t, ctx, db, b.recorder, referenceExec("DROP TABLE t", 0))
				}
			})
		}
	}
}
