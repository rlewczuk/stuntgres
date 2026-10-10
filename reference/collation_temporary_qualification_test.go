package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceCollationTemporaryQualification(t *testing.T) {
	dsn := "reference/" + t.Name()
	roleReferenceCleanup(t, []string{"coll_temp_actor", "coll_temp_other"})
	collationScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		collationExec(t, p, b, `CREATE USER coll_temp_actor`, `CREATE USER coll_temp_other`)
		if b.pg {
			collationExec(t, p, b, `ALTER ROLE coll_temp_actor PASSWORD 'role-core-fixture'`, `ALTER ROLE coll_temp_other PASSWORD 'role-core-fixture'`)
		}
		d := p.nativeConnector.Driver().(*stuntgres.Driver)
		actorDB := roleScenarioLoginPool(t, d, b.pg, dsn, "coll_temp_actor")
		actorDB.SetMaxOpenConns(2)
		creator, sameRole := referenceLockConnections(t, p.ctx, actorDB)
		otherDB := roleScenarioLoginPool(t, d, b.pg, dsn, "coll_temp_other")
		otherRole := sequenceCoreConn(t, p.ctx, otherDB)
		run := func(conn *sql.Conn, s referenceStep) {
			t.Helper()
			// Reserved login connections are unwrapped: assert their public cursor
			// and complete diagnostics directly, never middleware recording.
			if !s.selectRows {
				result, err := conn.ExecContext(p.ctx, s.query)
				deleteReferenceCount(t, result, err, s.affected)
				return
			}
			var rows *sql.Rows
			var err error
			if s.prepared {
				stmt, prepareErr := conn.PrepareContext(p.ctx, s.query)
				if prepareErr != nil {
					if s.diagnostic == nil {
						t.Fatal(prepareErr)
					}
					referenceAssertDiagnostic(t, prepareErr, s.diagnostic)
					return
				}
				defer stmt.Close()
				rows, err = stmt.QueryContext(p.ctx)
			} else {
				rows, err = conn.QueryContext(p.ctx, s.query)
			}
			if s.diagnostic != nil {
				if rows != nil {
					rows.Close()
				}
				referenceAssertDiagnostic(t, err, s.diagnostic)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			columns, err := rows.Columns()
			if err != nil || len(columns) != 1 || columns[0] != s.columns[0] {
				t.Fatal(columns, err)
			}
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != 1 {
				t.Fatal(types, err)
			}
			if types[0].DatabaseTypeName() != s.types[0] {
				t.Fatal(types[0].DatabaseTypeName())
			}
			if !rows.Next() {
				t.Fatal("missing actual creator value", rows.Err())
			}
			var value string
			if err := rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
			if value != s.rows[0][0] {
				t.Fatalf("actual value %q, expected %v", value, s.rows[0][0])
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
		run(creator, referenceExec(`CREATE COLLATION pg_temp.coll_temp_private FROM pg_catalog."C"`, 0))
		var schemas, physical string
		if err := creator.QueryRowContext(p.ctx, `SELECT current_schemas(true)`).Scan(&schemas); err != nil {
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
		value := referenceSelect(query, []string{"v"}, []string{"TEXT"}, [][]any{{"a"}})
		for _, prepared := range []bool{false, true} {
			step := value
			step.prepared = prepared
			run(creator, step)
			denied := referenceErrorDiagnostic(query, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for schema " + physical, Position: "12"})
			denied.selectRows, denied.prepared = true, prepared
			run(sameRole, denied)
			run(otherRole, denied)
			collationStep(t, p, b, step) // Genuine bootstrap superuser, distinct physical session.
		}
		run(creator, referenceSelect(`SELECT 'a' COLLATE pg_temp.coll_temp_private AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{"a"}}))
		run(creator, referenceExec(`DROP COLLATION pg_temp.coll_temp_private`, 0))
	})
}
