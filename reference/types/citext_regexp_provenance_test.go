package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceCitextRegexpProvenance(t *testing.T) {
	for _, expression := range []string{"public.regexp_match(s,p)", "public.regexp_replace(s,p,'X')", "public.regexp_split_to_array(s,p)"} {
		t.Run(expression, func(t *testing.T) {
			citextPatternScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				citextInstall(t, p, b, c)
				for _, q := range []string{
					"CREATE TABLE cr_error(s citext,p citext)",
					"CREATE VIEW cr_error_projection AS SELECT " + expression + " AS v FROM cr_error",
					"CREATE VIEW cr_error_where AS SELECT s::text AS v FROM cr_error WHERE " + expression + " IS NOT NULL",
				} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO cr_error VALUES('a','[')", 1))
				// Independently expected from scalar SQL-body simplification, not
				// copied from native output: these row-dependent bodies inline.
				for _, q := range []string{
					"SELECT " + expression + " AS v FROM cr_error",
					"SELECT s::text AS v FROM cr_error WHERE " + expression + " IS NOT NULL",
					"SELECT v FROM cr_error_projection",
					"SELECT v FROM cr_error_where",
				} {
					failure := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("2201B", "invalid regular expression: brackets [] not balanced"))
					failure.SelectRows = true
					citextPatternRoutes(t, p, b, c, failure)
				}
			})
		})
	}
}

func citextRegexpBodyCollationSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	citextInstall(t, p, b, c)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TABLE cr_body_collation(id integer,s citext COLLATE "C",p citext COLLATE "POSIX",CHECK ((public.regexp_match(s,p) IS NOT NULL) = (s IS NOT NULL)))`, 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO cr_body_collation VALUES(1,'ABC','abc'),(2,NULL,'[')", 2))
}

func citextRegexpBodyCollationChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	citextPatternRoutes(t, p, b, c, fixture.Select("SELECT id,public.regexp_match(s,p) AS v FROM cr_body_collation ORDER BY id", []string{"id", "v"}, []string{"INT4", "_TEXT"}, [][]any{{int64(1), "{ABC}"}, {int64(2), nil}}))
	citextPatternRoutes(t, p, b, c, fixture.Select("SELECT id,s::text AS s,p::text AS p FROM cr_body_collation ORDER BY id", []string{"id", "s", "p"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "ABC", "abc"}, {int64(2), nil, "["}}))
	failure := fixture.ErrorDiagnostic("INSERT INTO cr_body_collation VALUES(3,'x','[')", fixture.ControlsReferenceDiagnostic("2201B", "invalid regular expression: brackets [] not balanced"))
	citextPatternRoutes(t, p, b, c, failure)
	citextPatternRoutes(t, p, b, c, fixture.Select("SELECT id FROM cr_body_collation ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
}

func TestReferenceCitextRegexpBodyCollation(t *testing.T) {
	citextPatternScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		citextRegexpBodyCollationSetup(t, p, b, c)
		citextRegexpBodyCollationChecks(t, p, b, c)
		if b.PG {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP TABLE cr_body_collation", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP EXTENSION citext", 0))
			citextRegexpBodyCollationSetup(t, p, b, c)
			citextRegexpBodyCollationChecks(t, p, b, c)
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil { t.Fatal(err) }
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
		citextRegexpBodyCollationChecks(t, p, b, c)
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE;CREATE SCHEMA public"); err != nil { t.Fatal(err) }
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil { t.Fatal("actual emitted CHECK replay", err) }
			pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
			citextRegexpBodyCollationChecks(t, p, pg, p.Postgres)
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE;CREATE SCHEMA public"); err != nil { t.Fatal(err) }
		}
	})
}

func citextRegexpRenamedSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	citextInstall(t, p, b, c)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE cr_rename(s citext,p citext)", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO cr_rename VALUES('a','[')", 1))
	for _, name := range []string{"regexp_match", "regexp_matches"} {
		view := "cr_view_" + name
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW " + view + " AS SELECT public." + name + "(s,p,'i') AS v FROM cr_rename", 0))
		q := "SELECT public." + name + "($1::citext,$2::citext,$3::text) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, citextRegexpValue(q, "_TEXT", "{a}", "a", "a", "i"))
		// Retained SQL reparses its original spelling after ALTER RENAME.
		// This is a lookup failure, not an invocation of the renamed wrapper.
		missing := fixture.ControlsReferenceDiagnostic("42883", "function public."+name+"(citext, citext, text) does not exist")
		missing.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		missing.Position = fixture.Position(8)
		for _, renamed := range []string{"cr_" + name, "cr_" + name + "_again"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER FUNCTION public." + name + "(citext,citext,text) RENAME TO " + renamed, 0))
			failure := fixture.ErrorDiagnostic(q, missing, "a", "[", "i")
			failure.SelectRows, failure.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, failure)
			currentSQL := "SELECT public." + renamed + "($1::citext,$2::citext,$3::text) AS v"
			currentStatement, err := c.PrepareContext(p.Ctx, currentSQL)
			if err != nil { t.Fatal(err) }
			defer currentStatement.Close()
			currentHeld := fixture.HeldStatement{Executor: c, Statement: currentStatement, SQL: currentSQL}
			fixture.RunStep(t, p.Ctx, currentHeld, b.Recorder, citextRegexpValue(currentSQL, "_TEXT", "{a}", "a", "a", "i"))
			invocation := fixture.ErrorDiagnostic(currentSQL, citextRegexpDiagnostic("2201B", "invalid regular expression: brackets [] not balanced", renamed), "a", "[", "i")
			invocation.SelectRows, invocation.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, currentHeld, b.Recorder, invocation)
			if err := currentStatement.Close(); err != nil { t.Fatal(err) }
			stored := fixture.ErrorDiagnostic("SELECT v FROM " + view, citextRegexpDiagnostic("2201B", "invalid regular expression: brackets [] not balanced", renamed))
			stored.SelectRows = true
			citextPatternRoutes(t, p, b, c, stored)
			name = renamed
		}
		if err := stmt.Close(); err != nil { t.Fatal(err) }
	}
}

func citextRegexpRenamedChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	for _, name := range []string{"regexp_match", "regexp_matches"} {
		renamed := "cr_" + name + "_again"
		for _, q := range []string{"SELECT public." + renamed + "('a'::citext,'[','i') AS v", "SELECT v FROM cr_view_" + name} {
			failure := fixture.ErrorDiagnostic(q, citextRegexpDiagnostic("2201B", "invalid regular expression: brackets [] not balanced", renamed))
			failure.SelectRows = true
			citextPatternRoutes(t, p, b, c, failure)
		}
	}
}

func TestReferenceCitextRegexpRenamed(t *testing.T) {
	citextPatternScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		citextRegexpRenamedSetup(t, p, b, c)
		citextRegexpRenamedChecks(t, p, b, c)
		if b.PG {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP VIEW cr_view_regexp_match,cr_view_regexp_matches", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP TABLE cr_rename", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP EXTENSION citext", 0))
			citextRegexpRenamedSetup(t, p, b, c)
			citextRegexpRenamedChecks(t, p, b, c)
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil { t.Fatal(err) }
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
		citextRegexpRenamedChecks(t, p, b, c)
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE;CREATE SCHEMA public"); err != nil { t.Fatal(err) }
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil { t.Fatal("actual emitted renamed-wrapper replay", err) }
			pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
			citextRegexpRenamedChecks(t, p, pg, p.Postgres)
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE;CREATE SCHEMA public"); err != nil { t.Fatal(err) }
		}
	})
}
