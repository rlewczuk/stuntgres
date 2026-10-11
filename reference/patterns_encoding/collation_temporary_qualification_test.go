package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCollationTemporaryQualification(t *testing.T) {
	dsn := "reference/" + t.Name()
	fixture.RoleReferenceCleanup(t, []string{"coll_temp_actor", "coll_temp_other"})
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.CollationExec(t, p, b, `CREATE USER coll_temp_actor`, `CREATE USER coll_temp_other`)
		if b.PG {
			fixture.CollationExec(t, p, b, `ALTER ROLE coll_temp_actor PASSWORD 'role-core-fixture'`, `ALTER ROLE coll_temp_other PASSWORD 'role-core-fixture'`)
		}
		d := p.NativeConnector.Driver().(*stuntgres.Driver)
		actorDB := fixture.RoleScenarioLoginPool(t, d, b.PG, dsn, "coll_temp_actor")
		actorDB.SetMaxOpenConns(2)
		creator, sameRole := fixture.LockConnections(t, p.Ctx, actorDB)
		otherDB := fixture.RoleScenarioLoginPool(t, d, b.PG, dsn, "coll_temp_other")
		otherRole := fixture.SequenceCoreConn(t, p.Ctx, otherDB)
		run := func(conn *sql.Conn, s fixture.Step) {
			t.Helper()
			// Reserved login connections are unwrapped: assert their public cursor
			// and complete diagnostics directly, never middleware recording.
			if !s.SelectRows {
				result, err := conn.ExecContext(p.Ctx, s.Query)
				fixture.DeleteReferenceCount(t, result, err, s.Affected)
				return
			}
			var rows *sql.Rows
			var err error
			if s.Prepared {
				stmt, prepareErr := conn.PrepareContext(p.Ctx, s.Query)
				if prepareErr != nil {
					if s.Diagnostic == nil {
						t.Fatal(prepareErr)
					}
					fixture.AssertDiagnostic(t, prepareErr, s.Diagnostic)
					return
				}
				defer stmt.Close()
				rows, err = stmt.QueryContext(p.Ctx)
			} else {
				rows, err = conn.QueryContext(p.Ctx, s.Query)
			}
			if s.Diagnostic != nil {
				if rows != nil {
					rows.Close()
				}
				fixture.AssertDiagnostic(t, err, s.Diagnostic)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			columns, err := rows.Columns()
			if err != nil || len(columns) != 1 || columns[0] != s.Columns[0] {
				t.Fatal(columns, err)
			}
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != 1 {
				t.Fatal(types, err)
			}
			if types[0].DatabaseTypeName() != s.Types[0] {
				t.Fatal(types[0].DatabaseTypeName())
			}
			if !rows.Next() {
				t.Fatal("missing actual creator value", rows.Err())
			}
			var value string
			if err := rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
			if value != s.Rows[0][0] {
				t.Fatalf("actual value %q, expected %v", value, s.Rows[0][0])
			}
			if rows.Next() {
				t.Fatal("unexpected additional creator value")
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		}
		run(creator, fixture.Exec(`CREATE COLLATION pg_temp.coll_temp_private FROM pg_catalog."C"`, 0))
		var schemas, physical string
		if err := creator.QueryRowContext(p.Ctx, `SELECT current_schemas(true)`).Scan(&schemas); err != nil {
			t.Fatal(err)
		}
		for _, name := range strings.Split(strings.Trim(schemas, "{}"), ",") {
			name = strings.Trim(name, `"`)
			if strings.HasPrefix(name, "pg_temp_") {
				physical = name
			}
		}
		if physical == "" {
			t.Fatal("creator TEMP namespace absent", schemas)
		}
		query := `SELECT 'a' COLLATE "` + strings.ReplaceAll(physical, `"`, `""`) + `".coll_temp_private AS v`
		value := fixture.Select(query, []string{"v"}, []string{"TEXT"}, [][]any{{"a"}})
		for _, prepared := range []bool{false, true} {
			step := value
			step.Prepared = prepared
			run(creator, step)
			denied := fixture.ErrorDiagnostic(query, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for schema " + physical, Position: "12"})
			denied.SelectRows, denied.Prepared = true, prepared
			run(sameRole, denied)
			run(otherRole, denied)
			fixture.CollationStep(t, p, b, step) // Genuine bootstrap superuser, distinct physical session.
		}
		run(creator, fixture.Select(`SELECT 'a' COLLATE pg_temp.coll_temp_private AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{"a"}}))
		run(creator, fixture.Exec(`DROP COLLATION pg_temp.coll_temp_private`, 0))
	})
}
