//go:build reference

package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteCoreDiagnostics(t *testing.T) {
	p := fixture.NewPair(t)
	for _, backend := range []struct {
		name     string
		db       *sql.DB
		recorder *fixture.Recorder
	}{
		{"memory", p.Memory, p.MemoryRecord},
		{"postgres", p.Postgres, p.PostgresRecord},
	} {
		t.Run(backend.name, func(t *testing.T) {
			run := func(t *testing.T, s fixture.Step) fixture.Observed {
				t.Helper()
				return fixture.RunStep(t, p.Ctx, backend.db, backend.recorder, s)
			}
			run(t, fixture.Exec("CREATE TABLE t(id INT)", 0))
			run(t, fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
			for _, tc := range []struct {
				name, query string
				want        fixture.Diagnostic
			}{
				{"missing_relation", "DELETE FROM absent", fixture.Diagnostic{Code: "42P01", Message: `relation "absent" does not exist`, Position: "13"}},
				{"missing_relation_utf8", "/*é*/ DELETE FROM absent", fixture.Diagnostic{Code: "42P01", Message: `relation "absent" does not exist`, Position: "19"}},
				{"missing_column", "DELETE FROM t WHERE zzzzz=1", fixture.Diagnostic{Code: "42703", Message: `column "zzzzz" does not exist`, Position: "21"}},
				{"missing_column_utf8", "/*é*/ DELETE FROM t WHERE zzzzz=1", fixture.Diagnostic{Code: "42703", Message: `column "zzzzz" does not exist`, Position: "27"}},
				{"where_integer", "DELETE FROM t WHERE 1", fixture.Diagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "21"}},
				{"operator_types", "DELETE FROM t WHERE id=TRUE", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer = boolean", Position: "23", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
				{"boolean_input", "DELETE FROM t WHERE 'truth'", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`, Position: "21"}},
				{"incomplete_where", "DELETE FROM t WHERE", fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input", Position: "20"}},
				{"hidden_target", "DELETE FROM t AS d WHERE t.id=1", fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Position: "26", Hint: `Perhaps you meant to reference the table alias "d".`}},
				{"unknown_qualifier", "DELETE FROM t AS d WHERE x.id=1", fixture.Diagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "x"`, Position: "26"}},
				{"qualified_missing_column", "DELETE FROM t AS d WHERE d.zzzzz=1", fixture.Diagnostic{Code: "42703", Message: "column d.zzzzz does not exist", Position: "26"}},
				{"quoted_qualified_missing_column", `DELETE FROM t AS "D" WHERE "D"."Zzzzz"=1`, fixture.Diagnostic{Code: "42703", Message: "column D.Zzzzz does not exist", Position: "28"}},
				{"malformed_suffix", "DELETE FROM t WHERE id=1 xyz", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "xyz"`, Position: "26"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						stage := "execute"
						if prepared {
							stage = "prepare"
						}
						t.Run(stage, func(t *testing.T) {
							step := fixture.ImplicitError(tc.query, tc.want)
							step.Prepared = prepared
							step.SelectRows = true
							step.ErrorStage = stage
							observed := run(t, step)
							// Verbose oracle capture includes the complete portable
							// record and actual stage, not just SQLSTATE parity.
							t.Logf("stage=%s diagnostic=%#v", observed.ErrorStage, observed.Diagnostic)
							run(t, fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
						})
					}
				})
			}
		})
	}
}

func TestReferenceDeleteCorePermissionDiagnostic(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"delete_denied"})
	p := fixture.NewPair(t)
	for i, db := range []*sql.DB{p.Memory, p.Postgres} {
		t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
			a, _ := fixture.LockConnections(t, p.Ctx, db)
			// Seed inside the backend case: isolated schema resets must not
			// turn the permission witness into missing-relation precedence.
			fixture.LockExec(t, p.Ctx, a, "CREATE USER delete_denied", 0)
			fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT)", 0)
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1),(2)", 2)
			if i == 1 {
				// The reset oracle schema has no PUBLIC USAGE grant.
				// Admit name lookup without granting table DELETE.
				fixture.LockExec(t, p.Ctx, a, "GRANT USAGE ON SCHEMA public TO delete_denied", 0)
			}
			fixture.LockExec(t, p.Ctx, a, "SET SESSION AUTHORIZATION delete_denied", 0)
			t.Cleanup(func() { a.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION") })
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("DELETE FROM t", fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"})
				s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
				fixture.RunStep(t, p.Ctx, a, []*fixture.Recorder{p.MemoryRecord, p.PostgresRecord}[i], s)
			}
			fixture.LockExec(t, p.Ctx, a, "RESET SESSION AUTHORIZATION", 0)
			fixture.DeleteReferenceRows(t, p, a, []*fixture.Recorder{p.MemoryRecord, p.PostgresRecord}[i], 1, 2)
		})
	}
}
