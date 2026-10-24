package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLoInstallation(t *testing.T) {
	t.Run("trusted_owner", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"lo_installer"})
		loScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { loRun(t, p, b, c, s) }
			run(fixture.Exec("CREATE USER lo_installer", 0))
			var database string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
				t.Fatal(err)
			}
			target := `"` + strings.ReplaceAll(database, `"`, `""`) + `"`
			run(fixture.Exec("REVOKE CREATE ON DATABASE "+target+" FROM PUBLIC", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION lo_installer", 0))
			defer func() {
				if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
					t.Error(err)
				}
			}()
			run(fixture.ImplicitError("CREATE EXTENSION lo SCHEMA public", fixture.Diagnostic{Code: "42501", Message: `permission denied to create extension "lo"`, Hint: "Must have CREATE privilege on current database to create this extension."}))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			loAbsent(t, p, b, c)
			run(fixture.Exec("GRANT CREATE ON DATABASE "+target+" TO lo_installer", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION lo_installer", 0))
			run(fixture.Exec("CREATE EXTENSION lo SCHEMA public", 0))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Select("SELECT r.rolname FROM pg_extension e JOIN pg_roles r ON r.oid=e.extowner WHERE e.extname='lo'", []string{"rolname"}, []string{"NAME"}, [][]any{{"lo_installer"}}))
			run(fixture.Select("SELECT typname,typowner::bigint AS owner FROM pg_type WHERE oid IN ('lo'::regtype,'lo[]'::regtype) ORDER BY typname", []string{"typname", "owner"}, []string{"NAME", "INT8"}, [][]any{{"_lo", int64(10)}, {"lo", int64(10)}}))
			loIdentity(t, p, b, c)
			run(fixture.Exec("DROP EXTENSION lo", 0))
			run(fixture.Exec("REVOKE CREATE ON DATABASE "+target+" FROM lo_installer", 0))
		})
	})
	t.Run("public", func(t *testing.T) {
		loScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			loAbsent(t, p, b, c)
			loInstall(t, p, b, c)
			loRun(t, p, b, c, fixture.Select("SELECT extname,extversion,extrelocatable,extnamespace='public'::regnamespace AS placed FROM pg_extension WHERE extname='lo'", []string{"extname", "extversion", "extrelocatable", "placed"}, []string{"NAME", "TEXT", "BOOL", "BOOL"}, [][]any{{"lo", "1.2", true, true}}))
			loRun(t, p, b, c, fixture.Select("SELECT t.typowner=e.extowner AS member_owner FROM pg_type t JOIN pg_extension e ON t.typnamespace=e.extnamespace WHERE t.oid IN ('lo'::regtype,'lo[]'::regtype) AND e.extname='lo' ORDER BY t.typname", []string{"member_owner"}, []string{"BOOL"}, [][]any{{true}, {true}}))
			loRun(t, p, b, c, fixture.Exec("CREATE TABLE lo_one(v lo)", 0))
			loRun(t, p, b, c, fixture.Exec("INSERT INTO lo_one VALUES ('4294967295')", 1))
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT v FROM lo_one", []string{"v"}, []string{"OID"}, [][]any{{int64(4294967295)}})
				s.Prepared = prepared
				loRows(t, p, b, c, s)
			}
			loRun(t, p, b, c, fixture.ImplicitError("CREATE EXTENSION lo", fixture.Diagnostic{Code: "42710", Message: `extension "lo" already exists`}))
			var before int64
			if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_extension WHERE extname='lo'").Scan(&before); err != nil {
				t.Fatal(err)
			}
			var notices []fixture.Diagnostic
			_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("CREATE EXTENSION IF NOT EXISTS lo SCHEMA lo_missing VERSION 'missing'", 0))
			want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `extension "lo" already exists, skipping`}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("notices %#v want %#v", notices, want)
			}
			loRun(t, p, b, c, fixture.Select("SELECT oid::bigint=$1 AS unchanged FROM pg_extension WHERE extname='lo'", []string{"unchanged"}, []string{"BOOL"}, [][]any{{true}}, before))
		})
	})
	t.Run("schema", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "lo_addon")
		loScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			loRun(t, p, b, c, fixture.Exec("CREATE SCHEMA lo_addon", 0))
			loRun(t, p, b, c, fixture.Exec("CREATE EXTENSION lo SCHEMA lo_addon VERSION '1.2'", 0))
			loRun(t, p, b, c, fixture.Exec("CREATE TABLE lo_qualified(v lo_addon.lo)", 0))
			loRun(t, p, b, c, fixture.Exec("SET search_path=lo_addon,public", 0))
			loRun(t, p, b, c, fixture.Exec("CREATE TABLE public.lo_visible(v lo)", 0))
			for _, table := range []string{"lo_qualified", "lo_visible"} {
				loRun(t, p, b, c, fixture.Exec("INSERT INTO public."+table+" VALUES ($1)", 1, "2147483648"))
				loRows(t, p, b, c, fixture.Prepared(fixture.Select("SELECT v FROM public."+table, []string{"v"}, []string{"OID"}, [][]any{{int64(2147483648)}})))
			}
			loRun(t, p, b, c, fixture.Select("SELECT extnamespace='lo_addon'::regnamespace AS placed FROM pg_extension WHERE extname='lo'", []string{"placed"}, []string{"BOOL"}, [][]any{{true}}))
		})
	})
	t.Run("version", func(t *testing.T) {
		loScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			loRun(t, p, b, c, fixture.ImplicitError("CREATE EXTENSION lo VERSION 'missing'", fixture.Diagnostic{Code: "22023", Message: `extension "lo" has no installation script nor update path for version "missing"`}))
			loAbsent(t, p, b, c)
		})
	})
	t.Run("collision", func(t *testing.T) {
		loScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			loRun(t, p, b, c, fixture.Exec("CREATE DOMAIN lo AS oid", 0))
			var original int64
			if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_type WHERE typnamespace='public'::regnamespace AND typname='lo'").Scan(&original); err != nil {
				t.Fatal(err)
			}
			s := fixture.ImplicitError("CREATE EXTENSION lo", fixture.Diagnostic{Code: "42710", Message: `type "lo" already exists`})
			s.Diagnostic.Context = "SQL statement \"CREATE DOMAIN lo AS pg_catalog.oid\"\nextension script file \"lo--1.1.sql\", near line 10"
			loRun(t, p, b, c, s)
			loRun(t, p, b, c, fixture.Select("SELECT extname FROM pg_extension WHERE extname='lo'", []string{"extname"}, []string{"NAME"}, [][]any{}))
			loRun(t, p, b, c, fixture.Select("SELECT oid::bigint=$1 AS retained,typbasetype=26 AS base FROM pg_type WHERE typnamespace='public'::regnamespace AND typname='lo'", []string{"retained", "base"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, original))
			loRun(t, p, b, c, fixture.Exec("CREATE TABLE lo_collision(v lo)", 0))
			loRun(t, p, b, c, fixture.Exec("INSERT INTO lo_collision VALUES ('26')", 1))
			loRows(t, p, b, c, fixture.Select("SELECT v FROM lo_collision", []string{"v"}, []string{"OID"}, [][]any{{int64(26)}}))
		})
	})
	t.Run("relation_collision", func(t *testing.T) {
		loScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			loRun(t, p, b, c, fixture.Exec("CREATE TABLE lo(v integer)", 0))
			loRun(t, p, b, c, fixture.Exec("INSERT INTO lo VALUES (77)", 1))
			s := fixture.ImplicitError("CREATE EXTENSION lo", fixture.Diagnostic{Code: "42710", Message: `type "lo" already exists`})
			s.Diagnostic.Context = "SQL statement \"CREATE DOMAIN lo AS pg_catalog.oid\"\nextension script file \"lo--1.1.sql\", near line 10"
			loRun(t, p, b, c, s)
			loRun(t, p, b, c, fixture.Select("SELECT extname FROM pg_extension WHERE extname='lo'", []string{"extname"}, []string{"NAME"}, [][]any{}))
			loRun(t, p, b, c, fixture.Prepared(fixture.Select("SELECT v FROM lo", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}})))
			loRun(t, p, b, c, fixture.Select("SELECT typtype FROM pg_type WHERE oid='lo'::regtype", []string{"typtype"}, []string{"CHAR"}, [][]any{{"c"}}))
		})
	})
}

func TestReferenceLoInstallationTransactions(t *testing.T) {
	for _, commit := range []bool{false, true} {
		name := "rollback"
		if commit {
			name = "commit"
		}
		t.Run(name, func(t *testing.T) {
			loScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				peer, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				loAbsent(t, p, b, c)
				loAbsent(t, p, b, peer)
				tx, err := c.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				loInstall(t, p, b, tx)
				loIdentity(t, p, b, tx)
				loRun(t, p, b, tx, fixture.Select("SELECT extname,extversion FROM pg_extension WHERE extname='lo'", []string{"extname", "extversion"}, []string{"NAME", "TEXT"}, [][]any{{"lo", "1.2"}}))
				// The peer must not see any of the extension/domain/companion
				// installation before its transaction commits.
				loAbsent(t, p, b, peer)
				if !commit {
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					loAbsent(t, p, b, c)
					loAbsent(t, p, b, peer)
					return
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				for _, db := range []fixture.Executor{c, peer} {
					loIdentity(t, p, b, db)
					loRun(t, p, b, db, fixture.Select("SELECT extname,extversion FROM pg_extension WHERE extname='lo'", []string{"extname", "extversion"}, []string{"NAME", "TEXT"}, [][]any{{"lo", "1.2"}}))
				}
				// A peer can use the committed domain immediately, with ordinary
				// direct and prepared values and typed NULL result metadata.
				loRun(t, p, b, peer, fixture.Exec("CREATE TABLE lo_tx_visible(id integer NOT NULL,v lo)", 0))
				loRun(t, p, b, peer, fixture.Exec("INSERT INTO lo_tx_visible VALUES (1,$1)", 1, "4294967295"))
				loRun(t, p, b, peer, fixture.Prepared(fixture.Exec("INSERT INTO lo_tx_visible VALUES (2,$1)", 1, nil)))
				for _, db := range []fixture.Executor{c, peer} {
					for _, prepared := range []bool{false, true} {
						s := fixture.Select("SELECT v FROM lo_tx_visible ORDER BY id", []string{"v"}, []string{"OID"}, [][]any{{int64(4294967295)}, {nil}})
						s.Prepared = prepared
						loMetadataRows(t, p, b, db, s, []bool{true})
					}
				}
			})
		})
	}
}
