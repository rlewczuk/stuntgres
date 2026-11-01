package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

var sslInfoFunctions = []struct {
	name, typ string
	value     any
}{
	{"ssl_is_used", "BOOL", false},
	{"ssl_version", "TEXT", nil},
	{"ssl_cipher", "TEXT", nil},
	{"ssl_client_cert_present", "BOOL", false},
	{"ssl_client_serial", "NUMERIC", nil},
	{"ssl_client_dn", "TEXT", nil},
	{"ssl_issuer_dn", "TEXT", nil},
}

const sslInfoProjection = "ssl_part.ssl_is_used() AS used,ssl_part.ssl_version() AS version,ssl_part.ssl_cipher() AS cipher,ssl_part.ssl_client_cert_present() AS cert,ssl_part.ssl_client_serial() AS serial,ssl_part.ssl_client_dn() AS client,ssl_part.ssl_issuer_dn() AS issuer"
const sslInfoNames = "'ssl_is_used','ssl_version','ssl_cipher','ssl_client_cert_present','ssl_client_serial','ssl_client_dn','ssl_issuer_dn'"

func sslInfoSelect(query string) fixture.Step {
	return fixture.Select(query,
		[]string{"used", "version", "cipher", "cert", "serial", "client", "issuer"},
		[]string{"BOOL", "TEXT", "TEXT", "BOOL", "NUMERIC", "TEXT", "TEXT"},
		[][]any{{false, nil, nil, false, nil, nil, nil}})
}

// sslinfo lives outside public, so its database-global installation needs explicit
// teardown as well as the shared schema/role cleanup. Every body uses a physical owner.
func sslInfoCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "ssl_part")
	fixture.RoleReferenceCleanup(t, []string{"ssl_owner"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if backend.PG {
				clean := func() {
					ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
					defer cancel()
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "RESET ROLE", "DROP EXTENSION IF EXISTS sslinfo CASCADE", "DROP SCHEMA IF EXISTS ssl_part CASCADE"} {
						if _, err := c.ExecContext(ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				}
				clean()
				defer clean()
			}
			body(t, p, backend, c)
		})
	}
}

func sslInfoInstall(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE SCHEMA ssl_part", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION sslinfo SCHEMA ssl_part VERSION '1.2'", 0))
}

func TestReferenceSSLInfoConnection(t *testing.T) {
	sslInfoCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sslInfoInstall(t, p, b, c)
		q := "SELECT " + sslInfoProjection
		for _, prepared := range []bool{false, true} {
			s := sslInfoSelect(q)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		h := fixture.HeldStatement{Executor: c, Statement: held, SQL: q}
		for range 2 {
			fixture.RunStep(t, p.Ctx, h, b.Recorder, sslInfoSelect(q))
		}
		// c remains borrowed while peer is acquired: these are distinct sessions.
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, sslInfoSelect(q))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Prepared(sslInfoSelect(q)))
		truth := fixture.Select("SELECT ssl_part.ssl_is_used() IS FALSE AS used,ssl_part.ssl_version() IS NULL AS version,ssl_part.ssl_cipher() IS NULL AS cipher,ssl_part.ssl_client_cert_present() IS FALSE AS cert,ssl_part.ssl_client_serial() IS NULL AS serial,ssl_part.ssl_client_dn() IS NULL AS client,ssl_part.ssl_issuer_dn() IS NULL AS issuer",
			[]string{"used", "version", "cipher", "cert", "serial", "client", "issuer"},
			[]string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"},
			[][]any{{true, true, true, true, true, true, true}})
		for _, prepared := range []bool{false, true} {
			truth.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, truth)
			empty := sslInfoSelect(q + " WHERE false")
			empty.Rows = nil
			empty.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, empty)
		}
	})
}

func sslInfoConsumers(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE ssl_saved(id integer,used boolean DEFAULT ssl_part.ssl_is_used(),serial numeric DEFAULT ssl_part.ssl_client_serial(),version text DEFAULT ssl_part.ssl_version())", 0))
	run(fixture.Exec("INSERT INTO ssl_saved(id) VALUES(1),(2)", 2))
	run(fixture.Exec("INSERT INTO ssl_saved VALUES(3,NULL,NULL,NULL)", 1))
	run(fixture.Exec("CREATE VIEW ssl_view AS SELECT " + sslInfoProjection, 0))
	for _, prepared := range []bool{false, true} {
		view := sslInfoSelect("SELECT * FROM ssl_view")
		view.Prepared = prepared
		run(view)
		saved := fixture.Select("SELECT id,used,serial,version FROM ssl_saved ORDER BY id", []string{"id", "used", "serial", "version"}, []string{"INT4", "BOOL", "NUMERIC", "TEXT"}, [][]any{{int64(1), false, nil, nil}, {int64(2), false, nil, nil}, {int64(3), nil, nil, nil}})
		saved.Prepared = prepared
		run(saved)
	}
}

func TestReferenceSSLInfoSourcesAndConsumers(t *testing.T) {
	sslInfoCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sslInfoInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, fn := range sslInfoFunctions {
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT * FROM ssl_part."+fn.name+"()", []string{fn.name}, []string{fn.typ}, [][]any{{fn.value}})
				s.Prepared = prepared
				run(s)
			}
		}
		sslInfoConsumers(t, p, b, c)
		for _, prepared := range []bool{false, true} {
			projection := sslInfoSelect("SELECT " + sslInfoProjection + " FROM ssl_saved ORDER BY id")
			projection.Rows = [][]any{{false, nil, nil, false, nil, nil, nil}, {false, nil, nil, false, nil, nil, nil}, {false, nil, nil, false, nil, nil, nil}}
			projection.Prepared = prepared
			run(projection)
			group := fixture.Select("SELECT ssl_part.ssl_is_used() AS used,count(*) AS n FROM ssl_saved GROUP BY ssl_part.ssl_is_used() HAVING NOT ssl_part.ssl_is_used()", []string{"used", "n"}, []string{"BOOL", "INT8"}, [][]any{{false, int64(3)}})
			group.Prepared = prepared
			run(group)
			conditional := fixture.Select("SELECT CASE WHEN ssl_part.ssl_is_used() THEN 'TLS' ELSE ssl_part.ssl_version() END AS version,COALESCE(ssl_part.ssl_client_serial(),7::numeric)::text AS serial,NOT ssl_part.ssl_client_cert_present() AS no_cert", []string{"version", "serial", "no_cert"}, []string{"TEXT", "TEXT", "BOOL"}, [][]any{{nil, "7", true}})
			conditional.Prepared = prepared
			run(conditional)
		}
		run(fixture.ImplicitError("CREATE TABLE ssl_generated(used boolean GENERATED ALWAYS AS (ssl_part.ssl_is_used()) STORED)", fixture.Diagnostic{Code: "42P17", Message: "generation expression is not immutable"}))
		run(fixture.ImplicitError("CREATE INDEX ssl_volatile_idx ON ssl_saved ((ssl_part.ssl_is_used()))", fixture.Diagnostic{Code: "42P17", Message: "functions in index expression must be marked IMMUTABLE"}))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname IN ('ssl_generated','ssl_volatile_idx')", []string{"relname"}, []string{"NAME"}, nil))
	})
}
