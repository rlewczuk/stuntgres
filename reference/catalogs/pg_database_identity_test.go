package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGDatabaseIdentity(t *testing.T) {
	pgDatabaseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		q := "SELECT datname=current_database() AS same_database,datname=current_catalog AS same_catalog FROM pg_database WHERE datname=current_database()"
		s := fixture.Select(q, []string{"same_database", "same_catalog"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}})
		pgDatabaseCheck(t, p, b, b.DB, s)
		pgDatabaseCheck(t, p, b, fixture.ViewHold(t, p, b, q), s)
		var oid int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_database WHERE datname=current_database()").Scan(&oid); err != nil || oid <= 0 || oid > 4294967295 {
			t.Fatal("current identity", oid, err)
		}
		if !b.PG && oid != 16383 {
			t.Fatal("native database address", oid)
		}
		lookup := fixture.Select("SELECT oid=$1 AS same_identity,datname=current_database() AS same_name FROM pg_database WHERE oid=$1", []string{"same_identity", "same_name"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, oid)
		pgDatabaseCheck(t, p, b, b.DB, lookup)
		pgDatabaseCheck(t, p, b, fixture.ViewHold(t, p, b, lookup.Query), lookup)
		if b.PG {
			inventory := fixture.Select("SELECT datname FROM pg_database", []string{"datname"}, []string{"NAME"}, [][]any{{"postgres"}, {"template0"}, {"template1"}, {"stuntgres_reference"}})
			inventory.Bag = true
			pgDatabaseCheck(t, p, b, b.DB, inventory)
			pgDatabaseCheck(t, p, b, b.DB, fixture.Select("SELECT datname FROM pg_database WHERE datname=current_database()", []string{"datname"}, []string{"NAME"}, [][]any{{"stuntgres_reference"}}))
			return
		}
		// Host attachment cases deliberately have no PostgreSQL CREATE DATABASE analogue.
		pgDatabaseCheck(t, p, b, b.DB, fixture.Select("SELECT oid FROM pg_database", []string{"oid"}, []string{"OID"}, [][]any{{int64(16383)}}))
		open := func(d *stuntgres.Driver, key string) *sql.DB {
			c, err := d.OpenConnector(key)
			if err != nil {
				t.Fatal(err)
			}
			db := sql.OpenDB(c)
			t.Cleanup(func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			})
			return db
		}
		identity := func(db *sql.DB, want string) {
			var name string
			var same bool
			if err := db.QueryRowContext(p.Ctx, "SELECT datname,datname=current_database() FROM pg_database").Scan(&name, &same); err != nil || name != want || !same {
				t.Fatal("opaque identity", name, same, want, err)
			}
		}
		for _, tc := range []struct{ key, want string }{{"", ""}, {"Mixed DB", "Mixed DB"}, {"  identity db  ", "  identity db  "}, {"postgres://host/db?user=x", "postgres://host/db?user=x"}, {strings.Repeat("a", 64), strings.Repeat("a", 63)}, {strings.Repeat("a", 62) + "é", strings.Repeat("a", 62)}} {
			d := stuntgres.NewDriver()
			db := open(d, tc.key)
			identity(db, tc.want)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			identity(open(d, tc.key), tc.want)
		}
		d := stuntgres.NewDriver()
		key := strings.Repeat("a", 63)
		a, other := open(d, key+"x"), open(d, key+"y")
		identity(a, key)
		identity(other, key)
		if _, err := a.ExecContext(p.Ctx, "CREATE TABLE pgdb18_attachment(id integer)"); err != nil {
			t.Fatal(err)
		}
		for _, db := range []*sql.DB{other, open(stuntgres.NewDriver(), key+"x")} {
			rows, err := db.QueryContext(p.Ctx, "SELECT id FROM pgdb18_attachment")
			if rows != nil {
				rows.Close()
				t.Fatal("isolated attachment returned Rows")
			}
			fixture.State(t, err, "42P01")
		}
		for _, tc := range []struct{ key, byteText string }{{strings.Repeat("a", 64) + string([]byte{255}), "0xff"}, {"db\x00name", "0x00"}} {
			err := open(stuntgres.NewDriver(), tc.key).PingContext(p.Ctx)
			want := fixture.ControlsReferenceDiagnostic("22021", "invalid byte sequence for encoding \"UTF8\": "+tc.byteText)
			fixture.AssertDiagnostic(t, err, &want)
		}
		c, err := stuntgres.NewDriver().OpenConnector("notice database")
		if err != nil {
			t.Fatal(err)
		}
		db := sql.OpenDB(c.(*stuntgres.Connector).WithNoticeHandler(func(stuntgres.Error) {}))
		defer db.Close()
		identity(db, "notice database")
	})
}

func TestReferencePGDatabaseVisibility(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgdb18_reader"})
	p := fixture.NewScenario(t)
	pgDatabaseBackends(t, p, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		owner := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		pgClassExec(t, p, b, owner, "CREATE ROLE pgdb18_reader", "CREATE TABLE public.pg_database(marker integer)")
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("INSERT INTO public.pg_database VALUES(18)", 1))
		q := "SELECT datname=current_database() AS same_name,datdba=(SELECT oid FROM pg_roles WHERE rolname='" + pgTableViewsOwner(b) + "') AS same_owner FROM pg_catalog.pg_database WHERE datname=current_database()"
		held := fixture.ViewHold(t, p, b, q)
		s := fixture.Select(q, []string{"same_name", "same_owner"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}})
		defer owner.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		defer owner.ExecContext(p.Ctx, "RESET ROLE")
		pgClassExec(t, p, b, owner, "SET ROLE pgdb18_reader", "SET search_path TO pg_catalog")
		pgDatabaseCheck(t, p, b, owner, s)
		pgDatabaseCheck(t, p, b, held, s)
		pgDatabaseCheck(t, p, b, owner, fixture.Select("SELECT datacl IS NULL AS default_acl FROM pg_database WHERE datname=current_database()", []string{"default_acl"}, []string{"BOOL"}, [][]any{{b.PG}}))
		pgClassExec(t, p, b, owner, "RESET ROLE", "SET SESSION AUTHORIZATION pgdb18_reader")
		pgDatabaseCheck(t, p, b, owner, s)
		pgClassExec(t, p, b, owner, "RESET SESSION AUTHORIZATION", "SET search_path TO public")
		// Implicit pg_catalog wins; explicit ordering permits ordinary user shadowing.
		pgDatabaseCheck(t, p, b, owner, fixture.Select("SELECT datname=current_database() AS same_name FROM pg_database WHERE datname=current_database()", []string{"same_name"}, []string{"BOOL"}, [][]any{{true}}))
		pgClassExec(t, p, b, owner, "SET search_path TO public,pg_catalog")
		pgDatabaseCheck(t, p, b, owner, fixture.Select("SELECT marker FROM pg_database", []string{"marker"}, []string{"INT4"}, [][]any{{int64(18)}}))
		pgDatabaseCheck(t, p, b, owner, s)
	})
}
