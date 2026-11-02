package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func hstoreSubscriptCatalogCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, member bool) {
	t.Helper()
	q := `SELECT p.prosrc,p.probin,p.proisstrict,p.proretset,p.provolatile::text AS v,p.proparallel::text AS par,p.prorettype='internal'::regtype AS result,p.proargtypes::text AS args,l.lanname::text AS language,p.procost::text AS cost,p.prorows::text AS est FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.oid='hstore_subscript_handler(internal)'::regprocedure`
	hstoreRun(t, p, b, c, fixture.Select(q, []string{"prosrc", "probin", "proisstrict", "proretset", "v", "par", "result", "args", "language", "cost", "est"}, []string{"TEXT", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"hstore_subscript_handler", "$libdir/hstore", true, false, "i", "s", true, "2281", "c", "1", "0"}}))
	hstoreRun(t, p, b, c, fixture.Select(`SELECT typname::text AS t,typsubscript::regproc::text AS s FROM pg_type WHERE oid IN ('hstore'::regtype,'_hstore'::regtype) ORDER BY typname`, []string{"t", "s"}, []string{"TEXT", "TEXT"}, [][]any{{"_hstore", "array_subscript_handler"}, {"hstore", "hstore_subscript_handler"}}))
	hstoreRun(t, p, b, c, fixture.Select(`SELECT deptype::text AS d FROM pg_depend WHERE classid='pg_type'::regclass AND objid='hstore'::regtype AND refclassid='pg_proc'::regclass AND refobjid='hstore_subscript_handler(internal)'::regprocedure`, []string{"d"}, []string{"TEXT"}, [][]any{{"n"}}))
	rows := [][]any{}
	if member {
		rows = [][]any{{"e"}}
	}
	hstoreRun(t, p, b, c, fixture.Select(`SELECT deptype::text AS d FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='hstore_subscript_handler(internal)'::regprocedure AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='hstore')`, []string{"d"}, []string{"TEXT"}, rows))
}

func TestReferenceHstoreSubscriptCatalog(t *testing.T) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreRun(t, p, b, c, fixture.Select(`SELECT count(*) AS n FROM pg_proc WHERE proname='hstore_subscript_handler'`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		hstoreInstall(t, p, b, c)
		hstoreSubscriptCatalogCheck(t, p, b, c, true)
		hstoreRun(t, p, b, c, fixture.Exec(`CREATE DOMAIN hs_sub_dom AS hstore`, 0))
		hstoreRun(t, p, b, c, fixture.Select(`SELECT typsubscript::regproc::text AS s FROM pg_type WHERE oid='hs_sub_dom'::regtype`, []string{"s"}, []string{"TEXT"}, [][]any{{"-"}}))
		member := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop function hstore_subscript_handler(internal) because extension hstore requires it")
		member.Hint = "You can drop extension hstore instead."
		for _, prepared := range []bool{false, true} {
			hstoreError(t, p, b, c, `DROP FUNCTION hstore_subscript_handler(internal)`, prepared, false, false, member)
		}
		hstoreRun(t, p, b, c, fixture.Exec(`DROP DOMAIN hs_sub_dom`, 0))
		// A detached handler still owns the type's subscripting; the type is an
		// extension member, so the extension is reported as the dependent.
		hstoreRun(t, p, b, c, fixture.Exec(`ALTER EXTENSION hstore DROP FUNCTION hstore_subscript_handler(internal)`, 0))
		hstoreSubscriptCatalogCheck(t, p, b, c, false)
		detached := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop function hstore_subscript_handler(internal) because other objects depend on it")
		detached.Detail = "extension hstore depends on function hstore_subscript_handler(internal)"
		detached.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		for _, prepared := range []bool{false, true} {
			hstoreError(t, p, b, c, `DROP FUNCTION hstore_subscript_handler(internal)`, prepared, false, false, detached)
		}
		s := fixture.Select(`SELECT ('a=>1'::hstore)['a'] AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{"1"}})
		hstoreRun(t, p, b, c, s)
		hstoreRun(t, p, b, c, fixture.Prepared(s))
		hstoreRun(t, p, b, c, fixture.Exec(`ALTER EXTENSION hstore ADD FUNCTION hstore_subscript_handler(internal)`, 0))
		hstoreSubscriptCatalogCheck(t, p, b, c, true)
		hstoreError(t, p, b, c, `DROP FUNCTION hstore_subscript_handler(internal)`, false, false, false, member)
		hstoreRun(t, p, b, c, fixture.Exec(`DROP EXTENSION hstore`, 0))
		hstoreRun(t, p, b, c, fixture.Select(`SELECT count(*) AS n FROM pg_proc WHERE proname='hstore_subscript_handler'`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}

const hstoreSubscriptViewDef = ` SELECT id,
    h['a'::text] AS a,
    h[id] AS byid,
    ('"k"=>"v"'::hstore)['k'::text] AS lit,
    h[NULL::text] AS n,
    (COALESCE(h, '"z"=>"0"'::hstore))['z'::text] AS z
   FROM hs_sub;`

// Restored definitions carry the dump's explicit source aliases, so only the
// original definition's display is compared.
func hstoreSubscriptViewVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, definition bool) {
	t.Helper()
	if definition {
		hstoreRun(t, p, b, c, fixture.Select(`SELECT pg_get_viewdef('hs_sub_view'::regclass) AS d`, []string{"d"}, []string{"TEXT"}, [][]any{{hstoreSubscriptViewDef}}))
	}
	s := fixture.Select(`SELECT id,a,byid,lit,n,z FROM hs_sub_view ORDER BY id`, []string{"id", "a", "byid", "lit", "n", "z"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "1", nil, "v", nil, nil}, {int64(2), nil, nil, "v", nil, "0"}, {int64(3), nil, "three", "v", nil, nil}})
	hstoreRun(t, p, b, c, s)
	hstoreRun(t, p, b, c, fixture.Prepared(s))
	// The handler is reached through the type, never recorded by the rule.
	hstoreRun(t, p, b, c, fixture.Select(`SELECT pg_describe_object(refclassid,refobjid,refobjsubid) AS ref,deptype::text AS d FROM pg_depend WHERE classid='pg_rewrite'::regclass AND objid=(SELECT oid FROM pg_rewrite WHERE ev_class='hs_sub_view'::regclass) ORDER BY 1`, []string{"ref", "d"}, []string{"TEXT", "TEXT"}, [][]any{{"column h of table hs_sub", "n"}, {"column id of table hs_sub", "n"}, {"type hstore", "n"}, {"view hs_sub_view", "i"}}))
}

func TestReferenceHstoreSubscriptStored(t *testing.T) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreSubscriptSeed(t, p, b, c)
		hstoreRun(t, p, b, c, fixture.Exec(`UPDATE hs_sub SET h['3']='three' WHERE id=3`, 1))
		hstoreRun(t, p, b, c, fixture.Exec(`CREATE VIEW hs_sub_view AS SELECT id,h['a'] AS a,h[id] AS byid,('k=>v'::hstore)['k'] AS lit,h[NULL] AS n,(COALESCE(h,'z=>0'))['z'] AS z FROM hs_sub`, 0))
		hstoreSubscriptViewVerify(t, p, b, c, true)
		if b.PG {
			return
		} // Reset/Dump/Restore are native public APIs only.
		dump := hstoreDump(t, p, p.Native)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		hstoreSubscriptViewVerify(t, p, b, c, false)
		destination := fixture.NewFixture(t, false, true)
		if _, err := destination.Native.ExecContext(p.Ctx, "CREATE TABLE hs_sub_displace(v integer); CREATE EXTENSION hstore"); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, destination.Native, bytes.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		target := fixture.Backend{DB: destination.Memory, Recorder: destination.MemoryRecord}
		hstoreSubscriptViewVerify(t, p, target, destination.Memory, false)
		if after := hstoreDump(t, p, destination.Native); !bytes.Equal(dump, after) {
			t.Fatalf("restored destination changed deterministic dump\nbefore=%s\nafter=%s", dump, after)
		}
		if p.Postgres != nil {
			pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, string(dump)); err != nil {
				t.Fatalf("actual emitted SQL replay: %v", err)
			}
			hstoreSubscriptViewVerify(t, p, pg, p.Postgres, false)
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
		}
	})
}
