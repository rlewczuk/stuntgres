package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func sslInfoCertificateScalar(query string, args ...any) fixture.Step {
	return fixture.Select(query, []string{"value"}, []string{"TEXT"}, [][]any{{nil}}, args...)
}

func TestReferenceSSLInfoCertificateFields(t *testing.T) {
	sslInfoCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sslInfoInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		fields := []string{"commonName", "CN", "surname", "SN", "name", "givenName", "GN", "countryName", "C", "localityName", "L", "stateOrProvinceName", "ST", "organizationName", "O", "organizationalUnitName", "OU", "title", "description", "initials", "postalCode", "streetAddress", "generationQualifier", "dnQualifier", "x500UniqueIdentifier", "pseudonym", "role", "emailAddress", "", "not-a-field", "cn", " CN ", "é证书", "2.5.4.3", strings.Repeat("x", 4096)}
		run(fixture.Exec("CREATE TABLE ssl_fields(id integer,field text)", 0))
		run(fixture.Exec("INSERT INTO ssl_fields VALUES(1,'CN'),(2,'not-a-field'),(3,NULL)", 3))
		for _, fn := range []string{"ssl_client_dn_field", "ssl_issuer_field"} {
			t.Run(fn, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, field := range fields {
					literal := "'" + strings.ReplaceAll(field, "'", "''") + "'"
					for _, prepared := range []bool{false, true} {
						s := sslInfoCertificateScalar("SELECT ssl_part." + fn + "(" + literal + ") AS value")
						s.Prepared = prepared
						run(s)
					}
				}
				for _, arg := range []string{"NULL", "NULL::text", "'CN'::varchar", "'CN'::character(4)"} {
					for _, prepared := range []bool{false, true} {
						s := sslInfoCertificateScalar("SELECT ssl_part." + fn + "(" + arg + ") AS value")
						s.Prepared = prepared
						run(s)
					}
				}
				q := "SELECT ssl_part." + fn + "($1) AS value"
				held, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				h := fixture.HeldStatement{Executor: c, Statement: held, SQL: q}
				for _, value := range []any{"CN", "not-a-field", nil, int64(7)} {
					run(sslInfoCertificateScalar(q, value))
					run(fixture.Prepared(sslInfoCertificateScalar(q, value)))
					fixture.RunStep(t, p.Ctx, h, b.Recorder, sslInfoCertificateScalar(q, value))
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.Select("SELECT id,ssl_part."+fn+"(field) AS value FROM ssl_fields ORDER BY id", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}, {int64(2), nil}, {int64(3), nil}})
					s.Prepared = prepared
					run(s)
					source := fixture.Select("SELECT * FROM ssl_part."+fn+"('CN')", []string{fn}, []string{"TEXT"}, [][]any{{nil}})
					source.Prepared = prepared
					run(source)
					empty := sslInfoCertificateScalar("SELECT ssl_part." + fn + "(field) AS value FROM ssl_fields WHERE false")
					empty.Rows = nil
					empty.Prepared = prepared
					run(empty)
				}
			})
		}
	})
}
