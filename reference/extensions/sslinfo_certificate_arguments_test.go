package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func sslInfoCertificateIntegerError() fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
	// Pinned PostgreSQL 18.6 redacts Bind values in the portal context.
	d.Context = "unnamed portal parameter $1 = '...'"
	return d
}

func TestReferenceSSLInfoCertificateArguments(t *testing.T) {
	sslInfoCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sslInfoInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE SEQUENCE ssl_arg_seq START 1", 0))
		run(sslInfoCertificateScalar("SELECT ssl_part.ssl_client_dn_field(nextval('ssl_arg_seq')::text) AS value"))
		run(fixture.Prepared(sslInfoCertificateScalar("SELECT ssl_part.ssl_issuer_field(nextval('ssl_arg_seq')::text) AS value")))
		run(fixture.Select("SELECT currval('ssl_arg_seq') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
		run(fixture.Exec("CREATE TABLE ssl_arg_rows(id integer,field text)", 0))
		run(fixture.Exec("CREATE SEQUENCE ssl_arg_row_seq START 1", 0))
		run(fixture.Exec("INSERT INTO ssl_arg_rows VALUES(1,'C'),(2,'O'),(3,NULL)", 3))
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT id,ssl_part.ssl_client_dn_field(CASE WHEN id=1 THEN field || 'N' ELSE field END) AS value,ssl_part.ssl_issuer_field(field || $1::text) AS issuer FROM ssl_arg_rows ORDER BY id", []string{"id", "value", "issuer"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), nil, nil}, {int64(2), nil, nil}, {int64(3), nil, nil}}, "U")
			s.Prepared = prepared
			run(s)
			rowEffect := fixture.Select("SELECT id,ssl_part.ssl_client_dn_field(CASE WHEN field IS NULL THEN NULL ELSE field || nextval('ssl_arg_row_seq')::text END) AS value FROM ssl_arg_rows ORDER BY id", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}, {int64(2), nil}, {int64(3), nil}})
			rowEffect.Prepared = prepared
			run(rowEffect)
		}
		run(fixture.Select("SELECT last_value,is_called FROM ssl_arg_row_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(4), true}}))
		for _, tc := range []struct {
			query      string
			bad, good  any
			diagnostic fixture.Diagnostic
		}{
			{"SELECT ssl_part.ssl_client_dn_field(($1::integer)::text) AS value", "bad", int64(7), sslInfoCertificateIntegerError()},
			{"SELECT ssl_part.ssl_issuer_field(($1::integer / 0)::text) AS value", int64(1), nil, fixture.ControlsReferenceDiagnostic("22012", "division by zero")},
		} {
			held, err := c.PrepareContext(p.Ctx, tc.query)
			if err != nil {
				t.Fatal(err)
			}
			h := fixture.HeldStatement{Executor: c, Statement: held, SQL: tc.query}
			for _, route := range []struct {
				executor fixture.Executor
				prepared bool
			}{{c, false}, {c, true}, {h, false}} {
				s := fixture.ErrorDiagnostic(tc.query, tc.diagnostic, tc.bad)
				s.SelectRows = true
				s.Prepared = route.prepared
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, route.executor, b.Recorder, s)
				good := sslInfoCertificateScalar(tc.query, tc.good)
				good.Prepared = route.prepared
				fixture.RunStep(t, p.Ctx, route.executor, b.Recorder, good)
			}
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
		}
		// Explicit INTEGER is rejected at analysis even though evaluation would fail.
		sslInfoFailure(t, p, c, b.Recorder, "SELECT ssl_part.ssl_client_dn_field(1 / 0)", sslInfoMissing("ssl_part.ssl_client_dn_field(integer)", 8))
		run(fixture.Select("SELECT currval('ssl_arg_seq') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
	})
}
