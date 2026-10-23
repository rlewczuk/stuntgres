package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Capture the real owner before any ALTER or imported stream. The PostgreSQL
// replay login is a separate bootstrap role, not necessarily the system owner.
// Locate cleanup by captured builtin identity so every partial/failing path,
// including Reset before a failed Restore, restores the actual original tuple.
func routineAlterAbsCleanup(t *testing.T, p *fixture.Pair, c *sql.Conn) func() {
	t.Helper()
	var oid int64
	var owner string
	if err := c.QueryRowContext(p.Ctx, "SELECT p.oid,r.rolname FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid='pg_catalog.abs(integer)'::regprocedure").Scan(&oid, &owner); err != nil {
		t.Fatal(err)
	}
	quote := func(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
	return func() {
		t.Helper()
		if _, err := c.ExecContext(p.Ctx, "SET SESSION AUTHORIZATION DEFAULT"); err != nil {
			t.Error("restore original builtin authority", err)
			return
		}
		var schema, name string
		if err := c.QueryRowContext(p.Ctx, "SELECT n.nspname,p.proname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE p.oid=$1::oid", oid).Scan(&schema, &name); err != nil {
			t.Error("locate original builtin identity", err)
			return
		}
		qualified := quote(schema) + "." + quote(name) + "(integer)"
		if _, err := c.ExecContext(p.Ctx, "ALTER FUNCTION "+qualified+" OWNER TO "+quote(owner)); err != nil {
			t.Error("restore original builtin owner", err)
			return
		}
		if schema != "pg_catalog" {
			if _, err := c.ExecContext(p.Ctx, "ALTER FUNCTION "+qualified+" SET SCHEMA pg_catalog"); err != nil {
				t.Error("restore original builtin schema", err)
				return
			}
			qualified = `"pg_catalog".` + quote(name) + "(integer)"
		}
		if name != "abs" {
			if _, err := c.ExecContext(p.Ctx, "ALTER FUNCTION "+qualified+" RENAME TO abs"); err != nil {
				t.Error("restore original builtin name", err)
			}
		}
	}
}

func TestReferenceRoutineAlterPersistence(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ra_saved_owner"})
	fixture.SchemaReferenceCleanup(t, "ra_saved", "ra_saved_dest")
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Owner-only persistence deliberately has no explicit namespace ACL:
		// schemas naturally authorize the recipient; ACL Dump boundaries remain.
		routineExec(t, p, b, c, "CREATE ROLE ra_saved_owner", "CREATE SCHEMA ra_saved AUTHORIZATION ra_saved_owner", "CREATE SCHEMA ra_saved_dest AUTHORIZATION ra_saved_owner",
			"CREATE FUNCTION ra_saved.pick(integer) RETURNS integer RETURN $1+1", "CREATE FUNCTION ra_saved.pick(text) RETURNS bigint RETURN 104::bigint",
			"CREATE FUNCTION ra_saved.callee(bigint) RETURNS bigint RETURN $1+1000", "CREATE FUNCTION ra_saved.hard() RETURNS bigint RETURN ra_saved.callee(1)",
			"CREATE VIEW ra_saved.result AS SELECT ra_saved.callee(1) AS v", "CREATE TABLE ra_saved.data(v bigint DEFAULT ra_saved.callee(1))",
			"CREATE TABLE ra_saved.rule_log(v bigint)",
			"CREATE VIEW ra_saved.rule_input AS SELECT v FROM ra_saved.rule_log",
			"CREATE RULE ra_saved_insert AS ON INSERT TO ra_saved.rule_input DO INSTEAD INSERT INTO ra_saved.rule_log VALUES(ra_saved.callee(NEW.v))",
			"CREATE PROCEDURE ra_saved.p(integer) LANGUAGE SQL AS $$SELECT $1$$",
			"ALTER FUNCTION ra_saved.pick(integer) RENAME TO renamed", "ALTER FUNCTION ra_saved.renamed(integer) SET SCHEMA ra_saved_dest", "ALTER FUNCTION ra_saved_dest.renamed(integer) OWNER TO ra_saved_owner",
			"ALTER ROUTINE ra_saved.callee(bigint) RENAME TO captured", "ALTER FUNCTION ra_saved.captured(bigint) SET SCHEMA ra_saved_dest",
			"CREATE FUNCTION ra_saved.callee(integer) RETURNS bigint RETURN $1+2000",
			"ALTER PROCEDURE ra_saved.p(integer) RENAME TO moved_p", "ALTER PROCEDURE ra_saved.moved_p(integer) SET SCHEMA ra_saved_dest", "ALTER PROCEDURE ra_saved_dest.moved_p(integer) OWNER TO ra_saved_owner")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ra_saved.data DEFAULT VALUES", 1))
		check := func(c *sql.Conn, b fixture.Backend) {
			t.Helper()
			routineValue(t, p, b, c, "SELECT ra_saved_dest.renamed(4) AS v", "v", "INT4", int64(5))
			routineValue(t, p, b, c, "SELECT ra_saved.pick('x'::text) AS v", "v", "INT8", int64(104))
			routineValue(t, p, b, c, "SELECT ra_saved.hard() AS v", "v", "INT8", int64(1001))
			routineValue(t, p, b, c, "SELECT ra_saved.callee(1) AS v", "v", "INT8", int64(2001))
			routineAlterRows(t, p, b, c, "SELECT v FROM ra_saved.result", []string{"v"}, []string{"INT8"}, [][]any{{int64(1001)}})
			routineAlterRows(t, p, b, c, "SELECT v FROM ra_saved.data", []string{"v"}, []string{"INT8"}, [][]any{{int64(1001)}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ra_saved.rule_input VALUES(1)", 1))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ra_saved.rule_input SELECT ra_saved_dest.captured(1)", 1))
			routineAlterRows(t, p, b, c, "SELECT v FROM ra_saved.rule_log ORDER BY v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1001)}, {int64(2001)}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM ra_saved.rule_log", 2))
			routineAlterRows(t, p, b, c, "SELECT proname,prokind,proowner='ra_saved_owner'::regrole AS owned FROM pg_proc WHERE pronamespace='ra_saved_dest'::regnamespace AND proname IN ('renamed','moved_p') ORDER BY proname", []string{"proname", "prokind", "owned"}, []string{"NAME", "CHAR", "BOOL"}, [][]any{{"moved_p", "p", true}, {"renamed", "f", true}})
		}
		check(c, b)
		if b.PG {
			return
		}
		routineAlterReplay(t, p, b, c, check, "DROP SCHEMA IF EXISTS ra_saved CASCADE; DROP SCHEMA IF EXISTS ra_saved_dest CASCADE")
	})
}

func routineAlterReplay(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check func(*sql.Conn, fixture.Backend), pgCleanup string) {
	t.Helper()
	for range 2 {
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		for _, bad := range []io.Reader{
			strings.NewReader(dump.String() + "\nALTER FUNCTION ra_saved_dest.renamed(integer) OWNER TO ra_missing_owner;"),
			strings.NewReader(dump.String() + "\nALTER FUNCTION ra_missing(integer) RENAME TO bad;"),
			io.MultiReader(bytes.NewReader(dump.Bytes()), routineReadFailure{}),
		} {
			if err := stuntgres.Restore(p.Ctx, p.Native, bad); err == nil {
				t.Fatal("invalid Restore published")
			}
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(dump.Bytes(), after.Bytes()) {
				t.Fatal("failed Restore changed catalog/data")
			}
			check(c, b)
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); err == nil {
			t.Fatal("canceled Restore published")
		}
		check(c, b)
		held, err := c.QueryContext(p.Ctx, "SELECT 7::integer AS v")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		var v int64
		if !held.Next() {
			t.Fatal("retained rows", held.Err())
		}
		if err := held.Scan(&v); err != nil || v != 7 {
			t.Fatal(v, err)
		}
		if held.Next() || held.Err() != nil {
			t.Fatal("retained rows", held.Err())
		}
		held.Close()
		check(c, b)
		connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/fresh")
		if err != nil {
			t.Fatal(err)
		}
		fresh := sql.OpenDB(connector)
		if _, err := fresh.ExecContext(p.Ctx, "CREATE ROLE ra_saved_owner"); err != nil {
			fresh.Close()
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
			fresh.Close()
			t.Fatal(err)
		}
		var got int64
		if err := fresh.QueryRowContext(p.Ctx, "SELECT ra_saved_dest.renamed(4)+ra_saved.hard()+(SELECT v FROM ra_saved.data)").Scan(&got); err != nil || got != 2007 {
			fresh.Close()
			t.Fatal(got, err)
		}
		if strings.Contains(dump.String(), "ra_saved_abs") {
			if err := fresh.QueryRowContext(p.Ctx, "SELECT pg_catalog.ra_saved_abs(-4::integer)").Scan(&got); err != nil || got != 4 {
				fresh.Close()
				t.Fatal(got, err)
			}
			var hash string
			if err := fresh.QueryRowContext(p.Ctx, "SELECT v FROM ra_saved.provider_view").Scan(&hash); err != nil || hash != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
				fresh.Close()
				t.Fatal(hash, err)
			}
		}
		fresh.Close()
		if p.Postgres != nil {
			func() {
				pg, err := p.Postgres.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer pg.Close()
				restoreAbs := routineAlterAbsCleanup(t, p, pg)
				defer restoreAbs()
				var roleCount int
				if err := pg.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_roles WHERE rolname='ra_saved_owner'").Scan(&roleCount); err != nil {
					t.Fatal(err)
				}
				if roleCount == 0 {
					if _, err := pg.ExecContext(p.Ctx, "CREATE ROLE ra_saved_owner"); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := pg.ExecContext(p.Ctx, pgCleanup); err != nil {
					t.Fatal(err)
				}
				if _, err := pg.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal("actual emitted SQL replay", err)
				}
				check(pg, fixture.Backend{PG: true, Recorder: p.PostgresRecord})
				restoreAbs()
				if _, err := pg.ExecContext(p.Ctx, "SET check_function_bodies=true; "+pgCleanup+"; DROP ROLE ra_saved_owner"); err != nil {
					t.Fatal(err)
				}
			}()
		}
	}
}

func TestReferenceRoutineAlterProviderPersistence(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ra_saved_owner"})
	fixture.SchemaReferenceCleanup(t, "ra_saved", "ra_saved_dest")
	t.Run("provider only", routineAlterProviderOnlyPersistence)
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		restoreAbs := routineAlterAbsCleanup(t, p, c)
		defer restoreAbs()
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "DROP EXTENSION IF EXISTS pgcrypto CASCADE"); err != nil {
				t.Error(err)
			}
		}()
		// Keep real member/builtin OWNER changes separate from unsupported
		// explicit namespace ACL serialization.
		routineExec(t, p, b, c, "CREATE ROLE ra_saved_owner", "CREATE SCHEMA ra_saved AUTHORIZATION ra_saved_owner", "CREATE SCHEMA ra_saved_dest AUTHORIZATION ra_saved_owner", "CREATE EXTENSION pgcrypto SCHEMA ra_saved",
			"CREATE FUNCTION ra_saved_dest.renamed(integer) RETURNS integer RETURN $1+1", "CREATE FUNCTION ra_saved.hard() RETURNS bigint RETURN 1001::bigint", "CREATE TABLE ra_saved.data(v bigint)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ra_saved.data VALUES(1001)", 1))
		// Swap same-signature installed members. Their executable association
		// must survive both relabeling and replay's occupied-key cycle.
		routineExec(t, p, b, c, "ALTER FUNCTION ra_saved.encrypt(bytea,bytea,text) RENAME TO ra_cycle_tmp", "ALTER FUNCTION ra_saved.decrypt(bytea,bytea,text) RENAME TO encrypt", "ALTER FUNCTION ra_saved.ra_cycle_tmp(bytea,bytea,text) RENAME TO decrypt",
			"ALTER FUNCTION ra_saved.digest(text,text) RENAME TO ra_swap", "ALTER FUNCTION ra_saved.hmac(text,text,text) OWNER TO ra_saved_owner",
			"ALTER FUNCTION ra_saved.ra_swap(text,text) SET SCHEMA ra_saved_dest", "ALTER FUNCTION ra_saved_dest.ra_swap(text,text) OWNER TO ra_saved_owner",
			"CREATE VIEW ra_saved.provider_view AS SELECT encode(ra_saved_dest.ra_swap('abc'::text,'sha256'::text),'hex') AS v",
			"ALTER FUNCTION pg_catalog.abs(integer) RENAME TO ra_saved_abs")
		routineExec(t, p, b, c, "ALTER FUNCTION pg_catalog.ra_saved_abs(integer) OWNER TO ra_saved_owner")
		check := func(c *sql.Conn, b fixture.Backend) {
			t.Helper()
			routineValue(t, p, b, c, "SELECT pg_catalog.ra_saved_abs(-4::integer) AS v", "v", "INT4", int64(4))
			routineAlterRows(t, p, b, c, "SELECT v FROM ra_saved.provider_view", []string{"v"}, []string{"TEXT"}, [][]any{{"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"}})
			routineAlterRows(t, p, b, c, "SELECT proowner='ra_saved_owner'::regrole AS owned FROM pg_proc WHERE proname='ra_swap'", []string{"owned"}, []string{"BOOL"}, [][]any{{true}})
			routineAlterRows(t, p, b, c, "SELECT encode(ra_saved.encrypt(ra_saved.decrypt('abc'::bytea,'1234567890123456'::bytea,'aes'),'1234567890123456'::bytea,'aes'),'hex') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"616263"}})
		}
		check(c, b)
		if b.PG {
			return
		}
		routineAlterReplay(t, p, b, c, check, "DROP EXTENSION IF EXISTS pgcrypto CASCADE; DROP SCHEMA IF EXISTS ra_saved CASCADE; DROP SCHEMA IF EXISTS ra_saved_dest CASCADE")
	})
}
