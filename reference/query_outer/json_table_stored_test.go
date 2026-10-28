package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonTableStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec(`CREATE DOMAIN jt_stored_int AS int CHECK(VALUE>0)`, 0))
	run(fixture.Exec(`CREATE FUNCTION jt_default() RETURNS int LANGUAGE SQL AS 'SELECT 9'`, 0))
	run(fixture.Exec(`CREATE TABLE jt_stored(doc jsonb,threshold int)`, 0))
	run(fixture.Exec(`INSERT INTO jt_stored VALUES($1::jsonb,100)`, 1, jsonTableSiblingDocument))
	run(fixture.Exec(`CREATE VIEW jt_view AS SELECT j.* FROM jt_stored d CROSS JOIN JSON_TABLE(d.doc,'$[*]?(@.id < $max)' AS root PASSING d.threshold AS max COLUMNS(r FOR ORDINALITY,id jt_stored_int,formatted text FORMAT JSON PATH '$.id' WITHOUT WRAPPER,found int EXISTS PATH '$.id',fallback int PATH '$.absent' DEFAULT jt_default() ON EMPTY,NESTED '$.a[*]' AS ap COLUMNS(ao FOR ORDINALITY,av int PATH '$'),NESTED '$.b[*]' AS bp COLUMNS(bo FOR ORDINALITY,bv text PATH '$'))) j`, 0))
	s := fixture.Select(`SELECT r,id,ao,av,bo,bv,fallback FROM jt_view`, []string{"r", "id", "ao", "av", "bo", "bv", "fallback"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "TEXT", "INT4"}, [][]any{
		{int64(1), int64(7), int64(1), int64(10), nil, nil, int64(9)},
		{int64(1), int64(7), int64(2), int64(20), nil, nil, int64(9)},
		{int64(1), int64(7), nil, nil, int64(1), "x", int64(9)},
		{int64(2), int64(8), nil, nil, nil, nil, int64(9)},
		{int64(3), int64(9), nil, nil, int64(1), "z", int64(9)},
	})
	s.Bag = true
	fixture.SQLJSONBoth(t, p, b, c, s)
	extra := fixture.Select(`SELECT id,formatted,found,fallback FROM jt_view`, []string{"id", "formatted", "found", "fallback"}, []string{"INT4", "TEXT", "INT4", "INT4"}, [][]any{{int64(7), `7`, int64(1), int64(9)}, {int64(7), `7`, int64(1), int64(9)}, {int64(7), `7`, int64(1), int64(9)}, {int64(8), `8`, int64(1), int64(9)}, {int64(9), `9`, int64(1), int64(9)}})
	extra.Bag = true
	fixture.SQLJSONBoth(t, p, b, c, extra)
	return s
}

func TestReferenceJSONTableStored(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"jt_stored_reader"})
	fixture.JSONTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		check := jsonTableStored(t, p, b, c)
		stmt, err := c.PrepareContext(p.Ctx, check.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: check.Query}
		// ALTER FUNCTION ... RENAME is not admitted by the existing parser.
		// Preserve both supported renames and exercise the captured routine's
		// real ACL revision below, without adding adjacent routine DDL.
		for _, q := range []string{`ALTER DOMAIN jt_stored_int RENAME TO jt_renamed_int`, `ALTER TABLE jt_stored RENAME COLUMN doc TO payload`} {
			run(fixture.Exec(q, 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, check)
		}
		fixture.JSONPathNavigateReplay(t, p, b, c, check)
		if b.PG {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, check)
		} else {
			// Reset/Restore replaces logical domain identities, not held result formals.
			stale := fixture.ErrorDiagnostic(check.Query, fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type"))
			stale.SelectRows, stale.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, stale)
			fixture.SQLJSONBoth(t, p, b, c, check)
		}
		if !b.PG {
			var before bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
			for _, slot := range []int{0, 1, 2, 3} {
				args := []string{`'{}'::jsonb`, `1`, `2`, `3`}
				args[slot] = "$1"
				script := `/*é🙂*/ CREATE TABLE jt_bad_restore(v int); INSERT INTO jt_bad_restore VALUES((SELECT a FROM JSON_TABLE(` + args[0] + `,'$' PASSING ` + args[1] + ` AS x COLUMNS(a int PATH '$.x' DEFAULT ` + args[2] + ` ON EMPTY,NESTED '$.n[*]' COLUMNS(b int PATH '$' DEFAULT ` + args[3] + ` ON ERROR)))));`
				d := fixture.ControlsReferenceDiagnostic("42P02", "parameters are not allowed in restore")
				d.Position = fixture.Position(len([]rune(script[:strings.Index(script, "$1")])) + 1)
				fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("failed restore changed catalog")
				}
			}
		}
		run(fixture.Exec(`CREATE ROLE jt_stored_reader`, 0))
		run(fixture.Exec(`GRANT USAGE ON SCHEMA public TO jt_stored_reader`, 0))
		run(fixture.Exec(`GRANT SELECT ON jt_view TO jt_stored_reader`, 0))
		run(fixture.Exec(`SET SESSION AUTHORIZATION jt_stored_reader`, 0))
		defer c.ExecContext(p.Ctx, `RESET SESSION AUTHORIZATION`)
		readerStmt, err := c.PrepareContext(p.Ctx, check.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer readerStmt.Close()
		readerHeld := fixture.HeldStatement{Executor: c, Statement: readerStmt, SQL: check.Query}
		fixture.RunStep(t, p.Ctx, readerHeld, b.Recorder, check)
		run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
		run(fixture.Exec(`REVOKE EXECUTE ON FUNCTION jt_default() FROM PUBLIC`, 0))
		run(fixture.Exec(`SET SESSION AUTHORIZATION jt_stored_reader`, 0))
		denied := fixture.ErrorDiagnostic(check.Query, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function jt_default"))
		denied.SelectRows, denied.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, readerHeld, b.Recorder, denied)
		run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
		run(fixture.Exec(`GRANT EXECUTE ON FUNCTION jt_default() TO jt_stored_reader`, 0))
		run(fixture.Exec(`SET SESSION AUTHORIZATION jt_stored_reader`, 0))
		fixture.RunStep(t, p.Ctx, readerHeld, b.Recorder, check)
		run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
		if err := readerStmt.Close(); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec(`GRANT EXECUTE ON FUNCTION jt_default() TO PUBLIC`, 0))
		run(fixture.Exec(`REVOKE EXECUTE ON FUNCTION jt_default() FROM jt_stored_reader`, 0))
		run(fixture.Exec(`REVOKE SELECT ON jt_view FROM jt_stored_reader`, 0))
		run(fixture.Exec(`REVOKE USAGE ON SCHEMA public FROM jt_stored_reader`, 0))
		run(fixture.Exec(`DROP ROLE jt_stored_reader`, 0))
	})
	t.Run("declared type held invalidation", func(t *testing.T) {
		fixture.JSONTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec(`CREATE DOMAIN jt_generation AS int`, 0))
			// Capture the old identity independently, before preparing/dropping it.
			// PostgreSQL exposes its stale internal type cache here; native instead
			// deliberately applies its existing held-result generation boundary.
			var oldOID int64
			if b.PG {
				if err := c.QueryRowContext(p.Ctx, `SELECT oid::bigint FROM pg_type WHERE typname='jt_generation' AND typnamespace='public'::regnamespace`).Scan(&oldOID); err != nil {
					t.Fatal(err)
				}
			}
			q := `SELECT * FROM JSON_TABLE('[]','$[*]' COLUMNS(v jt_generation))`
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, nil))
			run(fixture.Exec(`DROP DOMAIN jt_generation`, 0))
			run(fixture.Exec(`CREATE DOMAIN jt_generation AS text`, 0))
			if b.PG {
				rows, err := stmt.QueryContext(p.Ctx)
				if rows != nil {
					for rows.Next() {
					}
					if err == nil {
						err = rows.Err()
					}
					rows.Close()
				}
				if err == nil {
					t.Fatal("held dropped type unexpectedly accepted")
				}
				got := fixture.ExtractDiagnostic(t, err)
				if !regexp.MustCompile(fmt.Sprintf(`^cache lookup failed for type %d$`, oldOID)).MatchString(got.Message) {
					t.Fatalf("stale type diagnostic: %+v; old OID=%d", got, oldOID)
				}
				d := fixture.ControlsReferenceDiagnostic("XX000", fmt.Sprintf("cache lookup failed for type %d", oldOID))
				fixture.AssertDiagnostic(t, err, &d)
			} else {
				s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type"))
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
			fixture.SQLJSONBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"TEXT"}, nil))
		})
	})
}
