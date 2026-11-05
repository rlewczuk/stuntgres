package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSSLInfoCertificateDiagnostics(t *testing.T) {
	sslInfoCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		sslInfoFailure(t, p, c, b.Recorder, "SELECT ssl_client_dn_field('CN')", sslInfoMissing("ssl_client_dn_field(unknown)", 8))
		missingSchema := fixture.ControlsReferenceDiagnostic("3F000", `schema "missing_ssl_schema" does not exist`)
		missingSchema.Position = "8"
		sslInfoFailure(t, p, c, b.Recorder, "SELECT missing_ssl_schema.ssl_issuer_field('CN')", missingSchema)
		sslInfoInstall(t, p, b, c)
		run(fixture.Exec("CREATE TABLE ssl_certificate_sentinel(id integer)", 0))
		run(fixture.Exec("INSERT INTO ssl_certificate_sentinel VALUES(7)", 1))
		before := sslInfoCertificateIdentities(t, p, c)
		sslInfoFailure(t, p, c, b.Recorder, "SELECT ssl_issuer_field('CN')", sslInfoMissing("ssl_issuer_field(unknown)", 8))
		run(fixture.Exec("SET search_path TO ssl_part,public", 0))
		for _, fn := range []string{"ssl_client_dn_field", "ssl_issuer_field"} {
			for _, arg := range []struct{ sql, signature string }{
				{"", ""}, {"'CN','O'", "unknown, unknown"}, {"1", "integer"}, {"true", "boolean"}, {"ARRAY['x']::text[]", "text[]"}, {"fieldname => 'CN'", "fieldname => unknown"}, {"$1::integer", "integer"},
			} {
				var args []any
				if arg.sql == "$1::integer" {
					args = []any{int64(1)}
				}
				sslInfoFailure(t, p, c, b.Recorder, "SELECT "+fn+"("+arg.sql+")", sslInfoMissing(fn+"("+arg.signature+")", 8), args...)
			}
			for _, value := range []string{"not-a-field", "", "cn", "é证书"} {
				run(sslInfoCertificateScalar("SELECT "+fn+"($1::text) AS value", value))
			}
		}
		sslInfoFailure(t, p, c, b.Recorder, "SELECT ssl_extension_info(NULL)", sslInfoMissing("ssl_extension_info(unknown)", 8))
		sslInfoFailure(t, p, c, b.Recorder, "/*é*/ SELECT ssl_client_dn_field(1)", sslInfoMissing("ssl_client_dn_field(integer)", 14))
		for _, tc := range []struct{ query, code, message, hint, marker string }{
			{"SELECT ssl_client_dn_field('CN') FILTER(WHERE TRUE)", "42809", "FILTER specified, but ssl_client_dn_field is not an aggregate function", "", "ssl_client"},
			{"SELECT ssl_issuer_field('CN') OVER()", "42809", "OVER specified, but ssl_issuer_field is not a window function nor an aggregate function", "", "ssl_issuer"},
			{"SELECT ssl_client_dn_field(DISTINCT 'CN')", "42809", "DISTINCT specified, but ssl_client_dn_field is not an aggregate function", "", "ssl_client"},
			{"SELECT ssl_client_dn_field(" + strings.Repeat("NULL,", 100) + "NULL)", "54023", "cannot pass more than 100 arguments to a function", "", "ssl_client"},
			{"SELECT * FROM ssl_part.ssl_extension_info() AS e(name text,value text,critical boolean)", "42601", "a column definition list is redundant for a function with OUT parameters", "", "name text"},
			{"SELECT 1 WHERE ssl_part.ssl_extension_info() IS NULL", "0A000", "set-returning functions are not allowed in WHERE", "", "ssl_part"},
			{"SELECT CASE WHEN true THEN ssl_part.ssl_extension_info() END", "0A000", "set-returning functions are not allowed in CASE", "You might be able to move the set-returning function into a LATERAL FROM item.", "ssl_part"},
			{"SELECT count(ssl_part.ssl_extension_info())", "0A000", "aggregate function calls cannot contain set-returning function calls", "You might be able to move the set-returning function into a LATERAL FROM item.", "ssl_part"},
			{"SELECT ssl_client_dn_field(('bad'::integer)::text)", "22P02", `invalid input syntax for type integer: "bad"`, "", "'bad'"},
		} {
			d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			d.Hint = tc.hint
			d.Position = fixture.Position(strings.Index(tc.query, tc.marker) + 1)
			sslInfoFailure(t, p, c, b.Recorder, tc.query, d)
		}
		// Every failure leaves the genuine declaration set and sentinel untouched.
		if after := sslInfoCertificateIdentities(t, p, c); !reflect.DeepEqual(before, after) {
			t.Fatalf("error changed identities: %v -> %v", before, after)
		}
		run(fixture.Select("SELECT id FROM ssl_certificate_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT ssl_certificate_error", 0))
		s := fixture.ErrorDiagnostic("SELECT ssl_client_dn_field(1)", sslInfoMissing("ssl_client_dn_field(integer)", 8))
		s.SelectRows = true
		s.ErrorStage = "execute"
		run(s)
		run(fixture.ImplicitError("SELECT id FROM ssl_certificate_sentinel", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
		run(fixture.Exec("ROLLBACK TO SAVEPOINT ssl_certificate_error", 0))
		run(sslInfoCertificateScalar("SELECT ssl_client_dn_field('CN') AS value"))
		run(fixture.Exec("UPDATE ssl_certificate_sentinel SET id=8", 1))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT id FROM ssl_certificate_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
	})
}
