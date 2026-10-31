package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgstatindexIdentity(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ps_identity_reader"})
	fixture.SchemaReferenceCleanup(t, "ps_part_moved")
	pgstatindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE SCHEMA ps_part_moved", 0))
		defer func() {
			c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			if _, err := c.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS ps_part_moved CASCADE"); err != nil {
				t.Error(err)
			}
		}()
		for _, q := range []string{"CREATE ROLE ps_identity_reader", "GRANT USAGE ON SCHEMA ps_part,ps_part_moved TO ps_identity_reader", "GRANT EXECUTE ON FUNCTION ps_part.pgstatindex(regclass) TO ps_identity_reader"} {
			run(fixture.Exec(q, 0))
		}
		for _, q := range []string{"CREATE INDEX \"123\" ON ps_part.ps_heap(k)", "CREATE INDEX \"-\" ON ps_part.ps_heap(k)", "CREATE INDEX \"Quoted Index\" ON ps_part.ps_heap(k)", "SET search_path=ps_part,pg_catalog"} {
			run(fixture.Exec(q, 0))
		}
		for _, name := range []string{"123", "-", "\"Quoted Index\"", "ps_part.\"Quoted Index\""} {
			pgstatindexEmpty(t, p, c, "SELECT * FROM pgstatindex($1::text)", name)
		}
		pgstatindexBoth(t, p, b, c, pgstatindexError("SELECT * FROM pgstatindex('-'::regclass)", "XX000", "could not open relation with OID 0"))
		pgstatindexBoth(t, p, b, c, pgstatindexError("SELECT * FROM pgstatindex('123'::regclass)", "XX000", "could not open relation with OID 123"))
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'ps_part.ps_index'::regclass::oid").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		pgstatindexEmpty(t, p, c, "SELECT * FROM pgstatindex($1::oid)", oid)
		textSQL := "SELECT (pgstatindex('ps_part.ps_index'::text)).leaf_pages>0 AS populated"
		oidSQL := "SELECT (pgstatindex('ps_part.ps_index'::regclass)).leaf_pages>0 AS populated"
		identitySQL := "SELECT (pgstatindex($1::oid)).leaf_pages>0 AS populated"
		hold := func(q string) fixture.HeldStatement {
			st, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			return fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		}
		ht, hi, hp := hold(textSQL), hold(oidSQL), hold(identitySQL)
		defer ht.Statement.Close()
		defer hi.Statement.Close()
		defer hp.Statement.Close()
		for _, h := range []fixture.HeldStatement{ht, hi} {
			fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(h.SQL, []string{"populated"}, []string{"BOOL"}, [][]any{{false}}))
		}
		fixture.RunStep(t, p.Ctx, hp, b.Recorder, fixture.Select(identitySQL, []string{"populated"}, []string{"BOOL"}, [][]any{{false}}, oid))
		run(fixture.Exec("CREATE VIEW ps_part.ps_bound AS SELECT (ps_part.pgstatindex('ps_part.ps_index'::regclass)).leaf_pages AS n", 0))
		run(fixture.Exec("ALTER INDEX ps_part.ps_index RENAME TO ps_original", 0))
		run(fixture.Exec("CREATE TABLE ps_part.ps_replacement(k integer)", 0))
		run(fixture.Exec("INSERT INTO ps_part.ps_replacement VALUES(1)", 1))
		run(fixture.Exec("CREATE INDEX ps_index ON ps_part.ps_replacement(k)", 0))
		fixture.RunStep(t, p.Ctx, ht, b.Recorder, fixture.Select(textSQL, []string{"populated"}, []string{"BOOL"}, [][]any{{true}}))
		// DDL invalidates the prepared query: PostgreSQL reanalyzes its original
		// text, so even the regclass literal now resolves to the replacement.
		fixture.RunStep(t, p.Ctx, hi, b.Recorder, fixture.Select(oidSQL, []string{"populated"}, []string{"BOOL"}, [][]any{{true}}))
		// A supplied numeric identity cannot follow that reused name. The stored
		// view independently retains its original regclass dependency as well.
		fixture.RunStep(t, p.Ctx, hp, b.Recorder, fixture.Select(identitySQL, []string{"populated"}, []string{"BOOL"}, [][]any{{false}}, oid))
		run(fixture.Select("SELECT n FROM ps_part.ps_bound", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		// Views carry installed identity, not the current routine spelling.
		run(fixture.Exec("ALTER FUNCTION ps_part.pgstatindex(regclass) RENAME TO ps_stats", 0))
		run(fixture.Select("SELECT n FROM ps_part.ps_bound", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		pgstatindexEmpty(t, p, c, "SELECT * FROM ps_part.ps_stats($1::regclass)", "ps_part.ps_original")
		run(fixture.Exec("SET SESSION AUTHORIZATION ps_identity_reader", 0))
		pgstatindexEmpty(t, p, c, "SELECT * FROM ps_part.ps_stats($1::regclass)", "ps_part.ps_original")
		pgstatindexBoth(t, p, b, c, pgstatindexError("SELECT * FROM ps_part.pgstatindex(NULL::text)", "42501", "permission denied for function pgstatindex"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("ALTER FUNCTION ps_part.ps_stats(regclass) SET SCHEMA ps_part_moved", 0))
		run(fixture.Select("SELECT n FROM ps_part.ps_bound", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		pgstatindexEmpty(t, p, c, "SELECT * FROM ps_part_moved.ps_stats($1::regclass)", "ps_part.ps_original")
		run(fixture.Exec("ALTER FUNCTION ps_part_moved.ps_stats(regclass) SET SCHEMA ps_part", 0))
		run(fixture.Exec("ALTER FUNCTION ps_part.ps_stats(regclass) RENAME TO pgstatindex", 0))
		run(fixture.Exec("ALTER EXTENSION pgstattuple SET SCHEMA ps_part_moved", 0))
		run(fixture.Select("SELECT n FROM ps_part.ps_bound", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		pgstatindexEmpty(t, p, c, "SELECT * FROM ps_part_moved.pgstatindex($1::regclass)", "ps_part.ps_original")
		run(fixture.Exec("SET SESSION AUTHORIZATION ps_identity_reader", 0))
		pgstatindexEmpty(t, p, c, "SELECT * FROM ps_part_moved.pgstatindex($1::regclass)", "ps_part.ps_original")
		pgstatindexBoth(t, p, b, c, pgstatindexError("SELECT * FROM ps_part_moved.pgstatindex(NULL::text)", "42501", "permission denied for function pgstatindex"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("CREATE FUNCTION ps_part.pgstatindex(text) RETURNS integer LANGUAGE SQL AS 'SELECT 77'", 0))
		pgstatindexBoth(t, p, b, c, fixture.Select("SELECT ps_part.pgstatindex('anything'::text) AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(77)}}))
		run(fixture.Exec("SET SESSION AUTHORIZATION ps_identity_reader", 0))
		pgstatindexBoth(t, p, b, c, fixture.Select("SELECT ps_part.pgstatindex('anything'::text) AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(77)}}))
		pgstatindexBoth(t, p, b, c, pgstatindexError("SELECT * FROM ps_part_moved.pgstatindex(NULL::text)", "42501", "permission denied for function pgstatindex"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("DROP FUNCTION ps_part.pgstatindex(text)", 0))
		run(fixture.Exec("DROP EXTENSION pgstattuple CASCADE", 0))
		run(fixture.Exec("CREATE EXTENSION pgstattuple SCHEMA ps_part", 0))
		pgstatindexEmpty(t, p, c, "SELECT * FROM ps_part.pgstatindex($1::regclass)", "ps_part.ps_original")
		run(fixture.Exec("SET SESSION AUTHORIZATION ps_identity_reader", 0))
		pgstatindexBoth(t, p, b, c, pgstatindexError("SELECT * FROM ps_part.pgstatindex(NULL::regclass)", "42501", "permission denied for function pgstatindex"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
