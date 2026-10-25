package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePgClassControls(t *testing.T) {
	p := fixture.NewScenario(t)
	seed := p.Backends()[0]
	fixture.PGClassExec(t, p, seed, seed.DB, "CREATE TABLE pc18_ctl_heap(id integer)", "CREATE INDEX pc18_ctl_idx ON pc18_ctl_heap(id)", "CREATE TABLE pc18_ctl_former(id integer)", "CREATE INDEX pc18_ctl_former_idx ON pc18_ctl_former(id)", "DROP INDEX pc18_ctl_former_idx", "CREATE VIEW pc18_ctl_view WITH(security_invoker=false) AS SELECT id FROM pc18_ctl_heap", "CREATE TABLE pc18_ctl_root(id integer) PARTITION BY RANGE(id)", "CREATE TABLE pc18_ctl_leaf PARTITION OF pc18_ctl_root FOR VALUES FROM(0) TO(10)")
	dump := fixture.ViewDump(t, p)
	pgClassBackends(t, p, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		q := "SELECT relname,relkind,relhasindex,relhassubclass,reloptions,relpartbound IS NULL AS no_bound FROM pg_class WHERE " + fixture.PGTableViewsNames("relname", []string{"pc18_ctl_former", "pc18_ctl_heap", "pc18_ctl_idx", "pc18_ctl_leaf", "pc18_ctl_root", "pc18_ctl_view"}) + " ORDER BY relname"
		want := func(history bool) [][]any {
			return [][]any{{"pc18_ctl_former", "r", history, false, nil, true}, {"pc18_ctl_heap", "r", true, false, nil, true}, {"pc18_ctl_idx", "i", false, false, nil, true}, {"pc18_ctl_leaf", "r", false, false, nil, false}, {"pc18_ctl_root", "p", false, true, nil, true}, {"pc18_ctl_view", "v", false, false, "{security_invoker=false}", true}}
		}
		checkStep := func(history bool) fixture.Step {
			return fixture.Select(q, []string{"relname", "relkind", "relhasindex", "relhassubclass", "reloptions", "no_bound"}, []string{"NAME", "CHAR", "BOOL", "BOOL", "_TEXT", "BOOL"}, want(history))
		}
		check := func(db fixture.Executor, history bool) { pgClassCheck(t, p, b, db, checkStep(history)) }
		if b.PG {
			t.Run("ActualDumpReplay", func(t *testing.T) {
				if _, err := b.DB.ExecContext(p.Ctx, string(dump)); err != nil {
					t.Fatal("PG replay actual native Dump", err)
				}
				pgClassCheck(t, p, b, b.DB, checkStep(false))
			})
			// Independent SQL installation observes the PostgreSQL contract even
			// when the actual native Dump witness above fails. Do not rewrite
			// emitted Dump SQL or repin its unwanted default-option expansion.
			t.Run("InstalledOptions", func(t *testing.T) {
				fixture.PGClassExec(t, p, b, b.DB, "CREATE VIEW pc18_ctl_oracle_view WITH(security_invoker=false) AS SELECT id FROM pc18_ctl_heap")
				probe := "SELECT reloptions FROM pg_class WHERE relname='pc18_ctl_oracle_view'"
				held := fixture.ViewHold(t, p, b, probe)
				pgClassCheck(t, p, b, held, fixture.Select(probe, []string{"reloptions"}, []string{"_TEXT"}, [][]any{{"{security_invoker=false}"}}))
				fixture.PGClassExec(t, p, b, b.DB, "ALTER VIEW pc18_ctl_oracle_view SET(security_invoker=true)")
				pgClassCheck(t, p, b, held, fixture.Select(probe, []string{"reloptions"}, []string{"_TEXT"}, [][]any{{"{security_invoker=true}"}}))
			})
			return
		}
		held := fixture.ViewHold(t, p, b, q)
		check(b.DB, true)
		check(held, true)
		old, err := b.DB.QueryContext(p.Ctx, "SELECT reloptions FROM pg_class WHERE relname='pc18_ctl_view'")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		types, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		oldBound, err := b.DB.QueryContext(p.Ctx, "SELECT relpartbound FROM pg_class WHERE relname='pc18_ctl_leaf'")
		if err != nil {
			t.Fatal(err)
		}
		defer oldBound.Close()
		boundTypes, err := oldBound.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(string(dump)+"CREATE SEQUENCE pc18_ctl_bad CACHE 0;")); err == nil {
			t.Fatal("invalid restore succeeded")
		} else {
			want := fixture.ControlsReferenceDiagnostic("22023", "CACHE (0) must be greater than zero")
			fixture.AssertDiagnostic(t, err, &want)
		}
		check(b.DB, true)
		check(held, true)
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		check(held, true)
		owner := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		fixture.PGClassExec(t, p, b, owner, "CREATE TEMP TABLE pc18_ctl_temp(id integer)", "CREATE INDEX pc18_ctl_tidx ON pc18_ctl_temp(id)", "DROP INDEX pc18_ctl_tidx", "CREATE TEMP TABLE pc18_ctl_tchild() INHERITS(pc18_ctl_temp)", "DROP TABLE pc18_ctl_tchild", "CREATE TEMP VIEW pc18_ctl_tview WITH(security_invoker=false) AS SELECT id FROM pc18_ctl_temp")
		var tempOID int64
		if err := owner.QueryRowContext(p.Ctx, "SELECT 'pc18_ctl_temp'::regclass::oid").Scan(&tempOID); err != nil {
			t.Fatal(err)
		}
		temp := func() {
			pgClassCheck(t, p, b, owner, fixture.Select("SELECT oid=$1 AS identity,relpersistence,relhasindex,relhassubclass FROM pg_class WHERE relname='pc18_ctl_temp'", []string{"identity", "relpersistence", "relhasindex", "relhassubclass"}, []string{"BOOL", "CHAR", "BOOL", "BOOL"}, [][]any{{true, "t", true, true}}, tempOID))
			pgClassCheck(t, p, b, owner, fixture.Select("SELECT reloptions FROM pg_class WHERE relname='pc18_ctl_tview'", []string{"reloptions"}, []string{"_TEXT"}, [][]any{{"{security_invoker=false}"}}))
		}
		// Unsupported retained dependencies and prepared transactions refuse
		// replacement atomically, not merely the failed SQL replay path.
		fixture.PGClassExec(t, p, b, owner, "CREATE TABLE pc18_ctl_boundary(id integer)", "CREATE TEMP TABLE pc18_ctl_block() INHERITS(pc18_ctl_boundary)")
		for _, control := range []func() error{
			func() error { return stuntgres.Reset(p.Ctx, p.Native) },
			func() error { return stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)) },
		} {
			fixture.TransactionOrdinary(t, control(), "stuntgres: catalog replacement with live temporary table inheritance of persistent parents is unsupported")
			check(held, true)
			temp()
		}
		fixture.PGClassExec(t, p, b, owner, "DROP TABLE pc18_ctl_block", "BEGIN", "PREPARE TRANSACTION 'pc18_ctl_prepared'")
		defer owner.ExecContext(context.Background(), "ROLLBACK PREPARED 'pc18_ctl_prepared'")
		for _, control := range []func() error{
			func() error { return stuntgres.Reset(p.Ctx, p.Native) },
			func() error { return stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)) },
		} {
			fixture.TransactionOrdinary(t, control(), "stuntgres: cannot replace catalog while prepared transactions exist")
			check(held, true)
			temp()
		}
		fixture.PGClassExec(t, p, b, owner, "ROLLBACK PREPARED 'pc18_ctl_prepared'")
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		pgClassCheck(t, p, b, held, fixture.Select(q, []string{"relname", "relkind", "relhasindex", "relhassubclass", "reloptions", "no_bound"}, []string{"NAME", "CHAR", "BOOL", "BOOL", "_TEXT", "BOOL"}, nil))
		temp()
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		check(b.DB, false)
		check(held, false)
		temp()
		pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT reltype='pc18_ctl_heap'::regtype::oid AS rowtype,oid='pc18_ctl_heap'::regclass::oid AS identity FROM pg_class WHERE relname='pc18_ctl_heap'", []string{"rowtype", "identity"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		if redump := fixture.ViewDump(t, p); !bytes.Equal(redump, dump) {
			t.Fatal("redump changed", string(dump), string(redump))
		}
		pgClassRetained(t, old, types, []string{"reloptions"}, []string{"_TEXT"}, []any{"{security_invoker=false}"})
		pgClassRetained(t, oldBound, boundTypes, []string{"relpartbound"}, []string{"PG_NODE_TREE"}, []any{`{PartitionBound strategy range default false lower ({type 23 typmod -1 collation 0 value "0"}) upper ({type 23 typmod -1 collation 0 value "10"})}`})
	})
}

func TestReferencePgClassSmoke(t *testing.T) {
	pgClassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		s := fixture.Select("SELECT * FROM pg_catalog.pg_class WHERE FALSE", fixture.PGClassColumns, pgClassTypes, nil)
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgClassMetadata(t, rows, b.PG) }
		pgClassCheck(t, p, b, b.DB, s)
		fixture.PGClassExec(t, p, b, b.DB, "CREATE TABLE pc18_smoke_heap(id integer CHECK(id>0))", "CREATE INDEX pc18_smoke_idx ON pc18_smoke_heap(id)", "CREATE SEQUENCE pc18_smoke_seq", "CREATE VIEW pc18_smoke_view WITH(security_invoker=false) AS SELECT id FROM pc18_smoke_heap", "CREATE TABLE pc18_smoke_root(id integer) PARTITION BY RANGE(id)", "CREATE TABLE pc18_smoke_leaf PARTITION OF pc18_smoke_root FOR VALUES FROM(0) TO(10)", "GRANT SELECT ON pc18_smoke_heap TO PUBLIC")
		q := "SELECT relname,relkind,relhasindex,relhassubclass,reloptions,relpartbound IS NULL AS no_bound FROM pg_class WHERE " + fixture.PGTableViewsNames("relname", []string{"pc18_smoke_heap", "pc18_smoke_idx", "pc18_smoke_seq", "pc18_smoke_view", "pc18_smoke_root", "pc18_smoke_leaf"}) + " ORDER BY relname"
		held := fixture.ViewHold(t, p, b, q)
		want := [][]any{{"pc18_smoke_heap", "r", true, false, nil, true}, {"pc18_smoke_idx", "i", false, false, nil, true}, {"pc18_smoke_leaf", "r", false, false, nil, false}, {"pc18_smoke_root", "p", false, true, nil, true}, {"pc18_smoke_seq", "S", false, false, nil, true}, {"pc18_smoke_view", "v", false, false, "{security_invoker=false}", true}}
		check := func() {
			pgClassCheck(t, p, b, held, fixture.Select(q, []string{"relname", "relkind", "relhasindex", "relhassubclass", "reloptions", "no_bound"}, []string{"NAME", "CHAR", "BOOL", "BOOL", "_TEXT", "BOOL"}, want))
		}
		check()
		pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT relacl FROM pg_class WHERE relname='pc18_smoke_heap'", []string{"relacl"}, []string{"_ACLITEM"}, [][]any{{"{" + fixture.PGTableViewsOwner(b) + "=arwdDxtm/" + fixture.PGTableViewsOwner(b) + ",=r/" + fixture.PGTableViewsOwner(b) + "}"}}))
		fixture.PGClassExec(t, p, b, b.DB, "DROP INDEX pc18_smoke_idx", "ALTER VIEW pc18_smoke_view SET(security_invoker=true)")
		want = append(want[:1], want[2:]...)
		want[len(want)-1][4] = "{security_invoker=true}"
		check()
	})
}
