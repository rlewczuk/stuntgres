//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDeleteReturningDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	for i, db := range []*sql.DB{p.memory, p.postgres} {
		t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
			r := []*referenceRecorder{p.memoryRecord, p.postgresRecord}[i]
			referenceLockExec(t, p.ctx, db, "CREATE TABLE t(id INT)", 0)
			referenceLockExec(t, p.ctx, db, "INSERT INTO t VALUES(1),(2)", 2)
			for _, tc := range []struct {
				name, query string
				want        referenceDiagnostic
			}{
				{"incomplete", "DELETE FROM t RETURNING", referenceDiagnostic{Code: "42601", Message: "syntax error at end of input", Position: "24"}},
				{"column", "DELETE FROM t RETURNING zzzzz", referenceDiagnostic{Code: "42703", Message: `column "zzzzz" does not exist`, Position: "25"}},
				{"column_utf8", "/*é*/ DELETE FROM t RETURNING zzzzz", referenceDiagnostic{Code: "42703", Message: `column "zzzzz" does not exist`, Position: "31"}},
				{"old_column", "DELETE FROM t RETURNING old.zzzzz", referenceDiagnostic{Code: "42703", Message: "column old.zzzzz does not exist", Position: "25"}},
				{"qualifier", "DELETE FROM t RETURNING x.id", referenceDiagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "x"`, Position: "25"}},
				{"hidden_target", "DELETE FROM t AS d RETURNING t.id", referenceDiagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Position: "30", Hint: `Perhaps you meant to reference the table alias "d".`}},
				{"hidden_old", "DELETE FROM t RETURNING WITH (OLD AS o) old.id", referenceDiagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "old"`, Position: "41"}},
				{"duplicate_old", "DELETE FROM t RETURNING WITH (OLD AS o, OLD AS p) id", referenceDiagnostic{Code: "42601", Message: "OLD cannot be specified multiple times", Position: "41"}},
				{"duplicate_new", "DELETE FROM t RETURNING WITH (NEW AS n, NEW AS p) id", referenceDiagnostic{Code: "42601", Message: "NEW cannot be specified multiple times", Position: "41"}},
				{"target_collision", "DELETE FROM t RETURNING WITH (OLD AS t) id", referenceDiagnostic{Code: "42712", Message: `table name "t" specified more than once`, Position: "31"}},
				{"alias_collision", "DELETE FROM t RETURNING WITH (OLD AS o, NEW AS o) id", referenceDiagnostic{Code: "42712", Message: `table name "o" specified more than once`, Position: "41"}},
				{"aggregate", "DELETE FROM t RETURNING count(*)", referenceDiagnostic{Code: "42803", Message: "aggregate functions are not allowed in RETURNING", Position: "25"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						stage := "execute"
						if prepared {
							stage = "prepare"
						}
						t.Run(stage, func(t *testing.T) {
							s := referenceImplicitError(tc.query, tc.want)
							s.prepared, s.selectRows, s.errorStage = prepared, true, stage
							referenceRunStep(t, p.ctx, db, r, s)
							deleteReferenceRows(t, p, db, r, 1, 2)
						})
					}
				})
			}
			// Star expansion is checked before scanning even an empty target.
			referenceLockExec(t, p.ctx, db, "CREATE TABLE z()", 0)
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError("DELETE FROM z RETURNING *", referenceDiagnostic{Code: "42601", Message: "RETURNING must have at least one column", Position: "25"})
				s.prepared, s.selectRows, s.errorStage = prepared, true, "execute"
				if prepared {
					s.errorStage = "prepare"
				}
				referenceRunStep(t, p.ctx, db, r, s)
			}
		})
	}
}
