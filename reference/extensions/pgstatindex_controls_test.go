package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Populated physical values intentionally need not equal PostgreSQL's layout.
// Independently require its page equation, usable root, and finite percentages.
func pgstatindexPopulated(t *testing.T, p *fixture.Pair, c *sql.Conn, rec *fixture.Recorder, call string) {
	t.Helper()
	q := "SELECT version=4 AND tree_level>=0 AND index_size=(1+internal_pages+leaf_pages+empty_pages+deleted_pages)*8192 AND root_block_no>0 AND root_block_no<index_size/8192 AND internal_pages>=0 AND leaf_pages>0 AND empty_pages>=0 AND deleted_pages>=0 AND avg_leaf_density>0 AND avg_leaf_density<=100 AND leaf_fragmentation>=0 AND leaf_fragmentation<=100 AS valid FROM " + call
	s := fixture.Select(q, []string{"valid"}, []string{"BOOL"}, [][]any{{true}})
	fixture.RunStep(t, p.Ctx, c, rec, s)
	fixture.RunStep(t, p.Ctx, c, rec, fixture.Prepared(s))
}

func TestReferencePgstatindexControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ps_control_reader"})
	pgstatindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE ROLE ps_control_reader", "GRANT USAGE ON SCHEMA ps_part TO ps_control_reader", "GRANT pg_stat_scan_tables TO ps_control_reader",
			"CREATE VIEW ps_part.ps_text_view AS SELECT * FROM ps_part.pgstatindex('ps_part.ps_index'::text)",
			"CREATE VIEW ps_part.ps_oid_view AS SELECT * FROM ps_part.pgstatindex('ps_part.ps_index'::regclass)",
			"CREATE VIEW ps_part.ps_scalar_view AS SELECT (ps_part.pgstatindex('ps_part.ps_index'::regclass)).version AS v",
			"ALTER TABLE ps_part.ps_heap OWNER TO ps_control_reader",
			"ALTER VIEW ps_part.ps_oid_view OWNER TO ps_control_reader",
			"ALTER INDEX ps_part.ps_index RENAME TO ps_kept",
			"CREATE TABLE ps_part.ps_other(k integer)", "INSERT INTO ps_part.ps_other VALUES(5)", "CREATE INDEX ps_index ON ps_part.ps_other(k)",
		} {
			count := int64(0)
			if q == "INSERT INTO ps_part.ps_other VALUES(5)" {
				count = 1
			}
			run(fixture.Exec(q, count))
		}
		post := func(target *sql.Conn, rec *fixture.Recorder) {
			fixture.RunStep(t, p.Ctx, target, rec, fixture.Select("SELECT pg_get_userbyid(relowner) AS owner FROM pg_class WHERE oid='ps_part.ps_heap'::regclass", []string{"owner"}, []string{"NAME"}, [][]any{{"ps_control_reader"}}))
			fixture.RunStep(t, p.Ctx, target, rec, fixture.Select("SELECT pg_get_userbyid(relowner) AS owner FROM pg_class WHERE oid='ps_part.ps_oid_view'::regclass", []string{"owner"}, []string{"NAME"}, [][]any{{"ps_control_reader"}}))
			pgstatindexEmpty(t, p, target, "SELECT * FROM ps_part.ps_oid_view")
			pgstatindexPopulated(t, p, target, rec, "ps_part.ps_text_view")
			fixture.RunStep(t, p.Ctx, target, rec, fixture.Select("SELECT v FROM ps_part.ps_scalar_view", []string{"v"}, []string{"INT4"}, [][]any{{int64(4)}}))
			fixture.RunStep(t, p.Ctx, target, rec, fixture.Exec("SET SESSION AUTHORIZATION ps_control_reader", 0))
			pgstatindexEmpty(t, p, target, "SELECT * FROM ps_part.pgstatindex($1::text)", "ps_part.ps_kept")
			pgstatindexEmpty(t, p, target, "SELECT * FROM ps_part.pgstatindex($1::regclass)", "ps_part.ps_kept")
			fixture.RunStep(t, p.Ctx, target, rec, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		post(c, b.Recorder)
		if b.PG {
			return
		}
		q := "SELECT (ps_part.pgstatindex($1::regclass)).version AS v"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		readHeld := func() {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(4)}}, "ps_part.ps_kept"))
		}
		readHeld()
		old, err := c.QueryContext(p.Ctx, "SELECT v FROM ps_part.ps_scalar_view")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if !old.Next() {
			t.Fatal("retained row missing", old.Err())
		}
		var v int64
		if err := old.Scan(&v); err != nil || v != 4 {
			t.Fatal("retained row", v, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("retained tail", old.Err())
		}
		old.Close()
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		post(c, b.Recorder)
		readHeld()
		before := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump+"\nINVALID SQL;")); err == nil {
			t.Fatal("invalid restore accepted")
		}
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("failed restore published state")
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("canceled restore", err)
		}
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("canceled restore published state")
		}
		post(c, b.Recorder)
		readHeld()
		other, dest, rec := pgcryptoControlDestination(t, p, "ps_control_destination")
		// Roles and memberships are destination authority, not catalog-dump
		// contents. Provision the same scan-role membership as on the source;
		// Restore must still remap the dumped owners and object ACLs itself.
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec("CREATE ROLE ps_control_reader", 0))
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec("GRANT pg_stat_scan_tables TO ps_control_reader", 0))
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec("CREATE TABLE destination_collision(k integer)", 0))
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec("CREATE TEMP TABLE ps_session_keep(k integer)", 0))
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec("INSERT INTO ps_session_keep VALUES(19)", 1))
		if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		post(dest, rec)
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Select("SELECT k FROM ps_session_keep", []string{"k"}, []string{"INT4"}, [][]any{{int64(19)}}))
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec("INSERT INTO ps_part.ps_heap VALUES(8)", 1))
		pgstatindexPopulated(t, p, dest, rec, "ps_part.pgstatindex('ps_part.ps_kept'::regclass)")
		// Destination writes cannot mutate the source's effective root or rows.
		pgstatindexEmpty(t, p, c, "SELECT * FROM ps_part.ps_oid_view")
		run(fixture.Select("SELECT k FROM ps_part.ps_heap", []string{"k"}, []string{"INT4"}, nil))
	})
}

func TestReferencePgstatindexTransactions(t *testing.T) {
	pgstatindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		for _, q := range []string{"BEGIN", "INSERT INTO ps_part.ps_heap VALUES(1),(2),(3)", "SAVEPOINT ps_save", "UPDATE ps_part.ps_heap SET k=20 WHERE k=2", "DELETE FROM ps_part.ps_heap WHERE k=3"} {
			count := int64(0)
			if strings.HasPrefix(q, "INSERT") {
				count = 3
			}
			if strings.HasPrefix(q, "UPDATE") || strings.HasPrefix(q, "DELETE") {
				count = 1
			}
			run(fixture.Exec(q, count))
		}
		for _, typ := range []string{"text", "regclass"} {
			pgstatindexPopulated(t, p, c, b.Recorder, "ps_part.pgstatindex('ps_part.ps_index'::"+typ+")")
		}
		run(fixture.Select("SELECT k FROM ps_part.ps_heap ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(20)}}))
		run(fixture.Exec("ROLLBACK TO SAVEPOINT ps_save", 0))
		run(fixture.Select("SELECT k FROM ps_part.ps_heap ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
		pgstatindexPopulated(t, p, c, b.Recorder, "ps_part.pgstatindex('ps_part.ps_index'::regclass)")
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT k FROM ps_part.ps_heap", []string{"k"}, []string{"INT4"}, nil))
		run(fixture.Exec("INSERT INTO ps_part.ps_heap VALUES(7)", 1))
		pgstatindexPopulated(t, p, c, b.Recorder, "ps_part.pgstatindex('ps_part.ps_index'::text)")
	})
}
