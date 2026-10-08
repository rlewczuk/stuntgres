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
				{"cross_hidden_old", "DELETE FROM t RETURNING WITH (OLD AS new) old.id", referenceDiagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "old"`, Position: "43"}},
				{"cross_hidden_new", "DELETE FROM t RETURNING WITH (NEW AS old) new.id", referenceDiagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "new"`, Position: "43"}},
				{"cross_collision", "DELETE FROM t RETURNING WITH (OLD AS new, NEW AS new) id", referenceDiagnostic{Code: "42712", Message: `table name "new" specified more than once`, Position: "43"}},
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

func TestReferenceDeleteReturningParameterOccurrenceDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	for i, db := range []*sql.DB{p.memory, p.postgres} {
		t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
			r := []*referenceRecorder{p.memoryRecord, p.postgresRecord}[i]
			referenceLockExec(t, p.ctx, db, "CREATE TABLE t(id INT)", 0)
			for _, populated := range []bool{false, true} {
				wantRows := [][]any{}
				if populated {
					referenceLockExec(t, p.ctx, db, "INSERT INTO t VALUES(1),(2)", 2)
					wantRows = [][]any{{int64(1)}, {int64(2)}}
				}
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError("DELETE FROM t RETURNING $1,$1::int4", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "25"}, 2)
					s.prepared, s.selectRows, s.errorStage = prepared, true, "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					referenceRunStep(t, p.ctx, db, r, s)
					referenceRunStep(t, p.ctx, db, r, referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, wantRows))
				}
			}
		})
	}
}

func TestReferenceDeleteReturningParameterOrderControls(t *testing.T) {
	p := newReferencePair(t)
	for backend, db := range []*sql.DB{p.memory, p.postgres} {
		t.Run([]string{"native", "postgres"}[backend], func(t *testing.T) {
			a, _ := referenceLockConnections(t, p.ctx, db)
			r := []*referenceRecorder{p.memoryRecord, p.postgresRecord}[backend]
			referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT)", 0)
			for _, query := range []string{
				"DELETE FROM t RETURNING $1::int4 AS typed,$1 AS repeated",
				"DELETE FROM t WHERE id=$1 RETURNING $1 AS typed,$1::int4 AS repeated",
			} {
				for _, prepared := range []bool{false, true} {
					var executor referenceExecutor = a
					var held *sql.Stmt
					if prepared {
						var err error
						held, err = a.PrepareContext(p.ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						executor = referenceHeldStatement{referenceExecutor: a, statement: held, query: query}
					}
					for step, want := range [][][]any{{{int64(2), int64(2)}, {int64(2), int64(2)}}, {}, {{int64(2), int64(2)}, {int64(2), int64(2)}}} {
						if step != 1 {
							referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(2),(2)", 2)
						}
						s := referenceSelect(query, []string{"typed", "repeated"}, []string{"INT4", "INT4"}, want, 2)
						s.bag = true
						referenceRunStep(t, p.ctx, executor, r, s)
					}
					if held != nil {
						held.Close()
					}
					referenceRunStep(t, p.ctx, a, r, referenceSelect("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{}))
				}
			}
		})
	}
}
