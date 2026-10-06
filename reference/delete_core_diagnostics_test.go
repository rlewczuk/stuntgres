//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDeleteCoreDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		name     string
		db       *sql.DB
		recorder *referenceRecorder
	}{
		{"memory", p.memory, p.memoryRecord},
		{"postgres", p.postgres, p.postgresRecord},
	} {
		t.Run(backend.name, func(t *testing.T) {
			run := func(t *testing.T, s referenceStep) referenceObserved {
				t.Helper()
				return referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			}
			run(t, referenceExec("CREATE TABLE t(id INT)", 0))
			run(t, referenceExec("INSERT INTO t VALUES(1),(2)", 2))
			for _, tc := range []struct {
				name, query string
				want        referenceDiagnostic
			}{
				{"missing_relation", "DELETE FROM absent", referenceDiagnostic{Code: "42P01", Message: `relation "absent" does not exist`, Position: "13"}},
				{"missing_relation_utf8", "/*é*/ DELETE FROM absent", referenceDiagnostic{Code: "42P01", Message: `relation "absent" does not exist`, Position: "19"}},
				{"missing_column", "DELETE FROM t WHERE zzzzz=1", referenceDiagnostic{Code: "42703", Message: `column "zzzzz" does not exist`, Position: "21"}},
				{"missing_column_utf8", "/*é*/ DELETE FROM t WHERE zzzzz=1", referenceDiagnostic{Code: "42703", Message: `column "zzzzz" does not exist`, Position: "27"}},
				{"where_integer", "DELETE FROM t WHERE 1", referenceDiagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "21"}},
				{"operator_types", "DELETE FROM t WHERE id=TRUE", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer = boolean", Position: "23", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
				{"boolean_input", "DELETE FROM t WHERE 'truth'", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`, Position: "21"}},
				{"incomplete_where", "DELETE FROM t WHERE", referenceDiagnostic{Code: "42601", Message: "syntax error at end of input", Position: "20"}},
				{"hidden_target", "DELETE FROM t AS d WHERE t.id=1", referenceDiagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Position: "26", Hint: `Perhaps you meant to reference the table alias "d".`}},
				{"unknown_qualifier", "DELETE FROM t AS d WHERE x.id=1", referenceDiagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "x"`, Position: "26"}},
				{"qualified_missing_column", "DELETE FROM t AS d WHERE d.zzzzz=1", referenceDiagnostic{Code: "42703", Message: "column d.zzzzz does not exist", Position: "26"}},
				{"quoted_qualified_missing_column", `DELETE FROM t AS "D" WHERE "D"."Zzzzz"=1`, referenceDiagnostic{Code: "42703", Message: "column D.Zzzzz does not exist", Position: "28"}},
				{"malformed_suffix", "DELETE FROM t WHERE id=1 xyz", referenceDiagnostic{Code: "42601", Message: `syntax error at or near "xyz"`, Position: "26"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						stage := "execute"
						if prepared {
							stage = "prepare"
						}
						t.Run(stage, func(t *testing.T) {
							step := referenceImplicitError(tc.query, tc.want)
							step.prepared = prepared
							step.selectRows = true
							step.errorStage = stage
							observed := run(t, step)
							// Verbose oracle capture includes the complete portable
							// record and actual stage, not just SQLSTATE parity.
							t.Logf("stage=%s diagnostic=%#v", observed.errorStage, observed.diagnostic)
							run(t, referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
						})
					}
				})
			}
		})
	}
}

func TestReferenceDeleteCoreDiagnosticBoundaries(t *testing.T) {
	p := newReferencePair(t)
	for i, db := range []*sql.DB{p.memory, p.postgres} {
		t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
			r := []*referenceRecorder{p.memoryRecord, p.postgresRecord}[i]
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, db, r, s) }
			run(referenceExec("CREATE TABLE t(id INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1),(2)", 2))
			for _, prepared := range []bool{false, true} {
				want := referenceDiagnostic{Code: "42P18", Message: "could not determine data type of parameter $1"}
				stage := "execute"
				if prepared {
					stage = "prepare"
				}
				if i == 1 && !prepared {
					want = referenceDiagnostic{Code: "42P02", Message: "there is no parameter $2", Position: "24"}
				}
				s := referenceImplicitError("DELETE FROM t WHERE id=$2", want)
				s.prepared, s.selectRows, s.errorStage = prepared, true, stage
				run(s)
				deleteReferenceRows(t, p, db, r, 1, 2)
			}
			query := "DELETE FROM t WHERE id=1; DELETE FROM t WHERE id=2"
			if i == 0 {
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError(query, referenceDiagnostic{Code: "0A000", Message: "multiple statements are unsupported"})
					s.prepared, s.selectRows = prepared, true
					s.errorStage = "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					run(s)
					deleteReferenceRows(t, p, db, r, 1, 2)
				}
			} else {
				// Simple PostgreSQL queries do support scripts. The rejection
				// boundary is independently tested at semantic Prepare only.
				s := referenceImplicitError(query, referenceDiagnostic{Code: "42601", Message: "cannot insert multiple commands into a prepared statement"})
				s.prepared, s.errorStage = true, "prepare"
				run(s)
				deleteReferenceRows(t, p, db, r, 1, 2)
				run(referenceExec(query, 1)) // lib/pq exposes the last command tag.
				deleteReferenceRows(t, p, db, r)
			}
		})
	}
}

func TestReferenceDeleteCoreExcludedForms(t *testing.T) {
	p := newReferencePair(t)
	for i, db := range []*sql.DB{p.memory, p.postgres} {
		t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
			a, _ := referenceLockConnections(t, p.ctx, db)
			r := []*referenceRecorder{p.memoryRecord, p.postgresRecord}[i]
			referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT)", 0)
			referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1),(2)", 2)
			if i == 0 {
				for _, tc := range []struct{ query, message, position string }{
					{"DELETE FROM t USING t AS other WHERE t.id=other.id", "DELETE suffix is unsupported", "15"},
					{"DELETE FROM t RETURNING id", "DELETE suffix is unsupported", "15"},
					{"DELETE FROM t WHERE CURRENT OF c", "DELETE CURRENT OF is unsupported", "21"},
				} {
					for _, prepared := range []bool{false, true} {
						s := referenceImplicitError(tc.query, referenceDiagnostic{Code: "0A000", Message: tc.message, Position: tc.position})
						s.prepared, s.selectRows, s.errorStage = prepared, true, "execute"
						if prepared {
							s.errorStage = "prepare"
						}
						referenceRunStep(t, p.ctx, a, r, s)
						deleteReferenceRows(t, p, a, r, 1, 2)
					}
				}
				return
			}
			// These are genuine PostgreSQL successes, not syntax-error parity
			// with the explicitly excluded native capabilities above.
			referenceLockExec(t, p.ctx, a, "DELETE FROM t USING t AS other WHERE t.id=other.id AND t.id=1", 1)
			deleteReferenceRows(t, p, a, r, 2)
			referenceRunStep(t, p.ctx, a, r, referenceSelect("DELETE FROM t RETURNING id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
			deleteReferenceRows(t, p, a, r)
			referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1),(2)", 2)
			tx, err := a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceLockExec(t, p.ctx, tx, "DECLARE c CURSOR FOR SELECT id FROM t ORDER BY id FOR UPDATE", 0)
			referenceRunStep(t, p.ctx, tx, r, referenceSelect("FETCH NEXT FROM c", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			referenceLockExec(t, p.ctx, tx, "DELETE FROM t WHERE CURRENT OF c", 1)
			deleteReferenceRows(t, p, tx, r, 2)
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			deleteReferenceRows(t, p, a, r, 2)
		})
	}
}

func TestReferenceDeleteCorePermissionDiagnostic(t *testing.T) {
	roleReferenceCleanup(t, []string{"delete_denied"})
	p := newReferencePair(t)
	for i, db := range []*sql.DB{p.memory, p.postgres} {
		t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
			a, _ := referenceLockConnections(t, p.ctx, db)
			// Seed inside the backend case: isolated schema resets must not
			// turn the permission witness into missing-relation precedence.
			referenceLockExec(t, p.ctx, a, "CREATE USER delete_denied", 0)
			referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT)", 0)
			referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1),(2)", 2)
			if i == 1 {
				// The reset oracle schema has no PUBLIC USAGE grant.
				// Admit name lookup without granting table DELETE.
				referenceLockExec(t, p.ctx, a, "GRANT USAGE ON SCHEMA public TO delete_denied", 0)
			}
			referenceLockExec(t, p.ctx, a, "SET SESSION AUTHORIZATION delete_denied", 0)
			t.Cleanup(func() { a.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION") })
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError("DELETE FROM t", referenceDiagnostic{Code: "42501", Message: "permission denied for table t"})
				s.prepared, s.selectRows, s.errorStage = prepared, true, "execute"
				referenceRunStep(t, p.ctx, a, []*referenceRecorder{p.memoryRecord, p.postgresRecord}[i], s)
			}
			referenceLockExec(t, p.ctx, a, "RESET SESSION AUTHORIZATION", 0)
			deleteReferenceRows(t, p, a, []*referenceRecorder{p.memoryRecord, p.postgresRecord}[i], 1, 2)
		})
	}
}
