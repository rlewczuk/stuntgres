//go:build reference

package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
)

func TestReferenceBtreeIndexExcludedDefinitionsDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, query, message, position, index string
		execute                               bool
	}{
		{"unique", "CREATE UNIQUE INDEX i ON t(n)", "unique indexes are unsupported", "8", "i", false},
		{"concurrent", "CREATE INDEX CONCURRENTLY i ON t(n)", "concurrent indexes are unsupported", "14", "i", false},
		{"expression", "CREATE INDEX ix ON t((c+1))", "expression indexes are unsupported", "22", "ix", false},
		{"spaced-expression", "CREATE INDEX ix ON t ((c+1))", "expression indexes are unsupported", "23", "ix", false},
		{"unicode-expression", "/*é*/ CREATE INDEX ix ON t((c+1))", "expression indexes are unsupported", "28", "ix", false},
		{"partial", "CREATE INDEX i ON t(n) WHERE n>0", "partial indexes are unsupported", "24", "i", false},
		{"collation", "CREATE INDEX i ON t(s COLLATE \"C\")", "index collations are unsupported", "23", "i", false},
		{"opclass", "CREATE INDEX i ON t(n int4_ops)", "index operator classes are unsupported", "23", "i", false},
		{"include", "CREATE INDEX i ON t(n) INCLUDE(s)", "included index columns are unsupported", "24", "i", false},
		{"storage", "CREATE INDEX i ON t(n) WITH(fillfactor=80)", "index storage parameters are unsupported", "24", "i", false},
		{"tablespace", "CREATE INDEX i ON t(n) TABLESPACE pg_default", "index tablespaces are unsupported", "24", "i", false},
		{"method", "CREATE INDEX i ON t USING hash(n)", "non-btree indexes are unsupported", "", "i", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferencePair(t)
			referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *referenceRecorder) {
				for _, step := range []referenceStep{referenceExec("CREATE TABLE t(n INT,c INT,s TEXT)", 0), referenceExec("INSERT INTO t VALUES(2,1,'one')", 1)} {
					referenceRunStep(t, p.ctx, db, recorder, step)
				}
				if db == p.memory {
					var before bytes.Buffer
					if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
						t.Fatal(err)
					}
					step := referenceImplicitError(tc.query, referenceDiagnostic{Code: "0A000", Message: tc.message, Position: tc.position})
					step.errorStage = "execute"
					referenceRunStep(t, p.ctx, db, recorder, step)
					step = referencePrepared(step)
					if !tc.execute {
						step.errorStage = "prepare"
					}
					referenceRunStep(t, p.ctx, db, recorder, step)
					var after bytes.Buffer
					if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || before.String() != after.String() {
						t.Fatal("exclusion changed dump", err, after.String())
					}
					referenceRunStep(t, p.ctx, db, recorder, referenceSelect("SELECT relname FROM pg_class WHERE relname='i' OR relname='ix'", []string{"relname"}, []string{"NAME"}, nil))
					referenceRunStep(t, p.ctx, db, recorder, referenceSelect("SELECT c,s FROM t", []string{"c", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
					referenceRunStep(t, p.ctx, db, recorder, referenceExec("CREATE INDEX i ON t(n)", 0))
					referenceRunStep(t, p.ctx, db, recorder, referenceExec("ALTER TABLE t ALTER c TYPE TEXT USING c::text", 0))
					referenceRunStep(t, p.ctx, db, recorder, referenceSelect("SELECT c FROM t", []string{"c"}, []string{"TEXT"}, [][]any{{"1"}}))
				} else {
					// Provider-valid real SQL: COLLATE C is on TEXT; int4_ops is on INT.
					referenceRunStep(t, p.ctx, db, recorder, referenceExec(tc.query, 0))
					referenceRunStep(t, p.ctx, db, recorder, referenceSelect("SELECT relname FROM pg_class WHERE relname=$1", []string{"relname"}, []string{"NAME"}, [][]any{{tc.index}}, tc.index))
					referenceRunStep(t, p.ctx, db, recorder, referencePrepared(referenceExec("INSERT INTO t VALUES(3,2,'two')", 1)))
					referenceRunStep(t, p.ctx, db, recorder, referenceSelect("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}))
				}
			})
		})
	}
}
