package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSSLInfoCertificateFeatureSmoke(t *testing.T) {
	sslInfoCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sslInfoInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, fn := range []string{"ssl_client_dn_field", "ssl_issuer_field"} {
			for _, value := range []any{"CN", "not-a-field", nil} {
				run(sslInfoCertificateScalar("SELECT ssl_part."+fn+"($1) AS value", value))
			}
		}
		run(fixture.Exec("CREATE SEQUENCE ssl_smoke_seq START 1", 0))
		run(sslInfoCertificateScalar("SELECT ssl_part.ssl_client_dn_field(nextval('ssl_smoke_seq')::text) AS value"))
		run(fixture.Select("SELECT last_value,is_called FROM ssl_smoke_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), true}}))
		run(sslInfoCertificateSource("SELECT * FROM ssl_part.ssl_extension_info()"))
		run(fixture.Select("SELECT ssl_part.ssl_extension_info()", []string{"ssl_extension_info"}, []string{"RECORD"}, nil))
		run(fixture.Select("SELECT generate_series(1,2) AS n,ssl_part.ssl_extension_info() AS cert ORDER BY n", []string{"n", "cert"}, []string{"INT4", "RECORD"}, [][]any{{int64(1), nil}, {int64(2), nil}}))
		sslInfoFailure(t, p, c, b.Recorder, "SELECT ssl_part.ssl_client_dn_field(1)", sslInfoMissing("ssl_part.ssl_client_dn_field(integer)", 8))
		run(fixture.Exec("CREATE TABLE ssl_smoke_input(id integer,field text)", 0))
		run(fixture.Exec("INSERT INTO ssl_smoke_input VALUES(1,'CN')", 1))
		run(fixture.Exec("CREATE VIEW ssl_smoke_values AS SELECT ssl_part.ssl_issuer_field(field || '') AS value FROM ssl_smoke_input", 0))
		run(fixture.Exec("CREATE VIEW ssl_smoke_extensions AS SELECT * FROM ssl_part.ssl_extension_info()", 0))
		run(fixture.Exec("CREATE TABLE ssl_smoke_saved(id integer,value text DEFAULT ssl_part.ssl_client_dn_field(nextval('ssl_smoke_seq')::text))", 0))
		const parameterSQL = "SELECT ssl_part.ssl_client_dn_field($1) AS value"
		const scalarSQL = "SELECT value FROM ssl_smoke_values"
		const sourceSQL = "SELECT * FROM ssl_smoke_extensions"
		const defaultSQL = "INSERT INTO ssl_smoke_saved(id) VALUES($1) RETURNING value"
		held := make(map[string]fixture.HeldStatement)
		for _, q := range []string{parameterSQL, scalarSQL, sourceSQL, defaultSQL} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held[q] = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		}
		observe := func(field any) {
			t.Helper()
			fixture.RunStep(t, p.Ctx, held[parameterSQL], b.Recorder, sslInfoCertificateScalar(parameterSQL, field))
			fixture.RunStep(t, p.Ctx, held[scalarSQL], b.Recorder, sslInfoCertificateMetadata(sslInfoCertificateScalar(scalarSQL), !b.PG))
			fixture.RunStep(t, p.Ctx, held[sourceSQL], b.Recorder, sslInfoCertificateMetadata(fixture.Select(sourceSQL, []string{"name", "value", "critical"}, []string{"TEXT", "TEXT", "BOOL"}, nil), !b.PG))
		}
		observe("CN")
		fixture.RunStep(t, p.Ctx, held[defaultSQL], b.Recorder, sslInfoCertificateScalar(defaultSQL, int64(1)))
		run(fixture.Select("SELECT last_value,is_called FROM ssl_smoke_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(2), true}}))
		if b.PG {
			return
		}
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		run(fixture.Select("SELECT last_value,is_called FROM ssl_smoke_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(2), true}}))
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		observe("not-a-field")
		fixture.RunStep(t, p.Ctx, held[defaultSQL], b.Recorder, sslInfoCertificateScalar(defaultSQL, int64(2)))
		run(fixture.Select("SELECT last_value,is_called FROM ssl_smoke_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(3), true}}))
		run(fixture.Select("SELECT id,value FROM ssl_smoke_saved ORDER BY id", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}, {int64(2), nil}}))
	})
}
