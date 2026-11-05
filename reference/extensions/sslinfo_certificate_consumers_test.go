package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSSLInfoCertificateConsumers(t *testing.T) {
	sslInfoCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sslInfoInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE SEQUENCE ssl_consumer_seq START 1", 0))
		run(fixture.Exec("CREATE SEQUENCE ssl_consumer_issuer_seq START 1", 0))
		run(fixture.Exec("CREATE TABLE ssl_certificate_input(id integer,field text)", 0))
		run(fixture.Exec("INSERT INTO ssl_certificate_input VALUES(1,'CN'),(2,'not-a-field'),(3,NULL)", 3))
		run(fixture.Exec("CREATE VIEW ssl_certificate_values AS SELECT id,ssl_part.ssl_client_dn_field(field) AS client,ssl_part.ssl_issuer_field(CASE WHEN field IS NULL THEN NULL ELSE field || '' END) AS issuer FROM ssl_certificate_input", 0))
		run(fixture.Exec("CREATE VIEW ssl_certificate_extensions AS SELECT * FROM ssl_part.ssl_extension_info()", 0))
		run(fixture.Exec("CREATE TABLE ssl_certificate_saved(id integer,client text DEFAULT ssl_part.ssl_client_dn_field(nextval('ssl_consumer_seq')::text),issuer text DEFAULT ssl_part.ssl_issuer_field(nextval('ssl_consumer_issuer_seq')::text))", 0))
		for i, prepared := range []bool{false, true} {
			s := fixture.Select("INSERT INTO ssl_certificate_saved(id) VALUES($1) RETURNING client,issuer", []string{"client", "issuer"}, []string{"TEXT", "TEXT"}, [][]any{{nil, nil}}, int64(i+1))
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Select("SELECT last_value,is_called FROM ssl_consumer_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(2), true}}))
		run(fixture.Select("SELECT last_value,is_called FROM ssl_consumer_issuer_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(2), true}}))
		q := "INSERT INTO ssl_certificate_saved(id) VALUES($1) RETURNING client,issuer"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		h := fixture.HeldStatement{Executor: c, Statement: held, SQL: q}
		fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"client", "issuer"}, []string{"TEXT", "TEXT"}, [][]any{{nil, nil}}, int64(3)))
		run(fixture.Select("SELECT last_value,is_called FROM ssl_consumer_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(3), true}}))
		run(fixture.Select("SELECT last_value,is_called FROM ssl_consumer_issuer_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(3), true}}))
		for _, prepared := range []bool{false, true} {
			view := fixture.Select("SELECT id,client,issuer FROM ssl_certificate_values ORDER BY id", []string{"id", "client", "issuer"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), nil, nil}, {int64(2), nil, nil}, {int64(3), nil, nil}})
			view.Prepared = prepared
			run(sslInfoCertificateMetadata(view, !b.PG))
			source := fixture.Select("SELECT * FROM ssl_certificate_extensions", []string{"name", "value", "critical"}, []string{"TEXT", "TEXT", "BOOL"}, nil)
			source.Prepared = prepared
			run(sslInfoCertificateMetadata(source, !b.PG))
			group := fixture.Select("SELECT ssl_part.ssl_client_dn_field(field) AS value,count(*) AS n FROM ssl_certificate_input GROUP BY ssl_part.ssl_client_dn_field(field)", []string{"value", "n"}, []string{"TEXT", "INT8"}, [][]any{{nil, int64(3)}})
			group.Prepared = prepared
			run(group)
			aggregate := sslInfoCertificateScalar("SELECT ssl_part.ssl_issuer_field(min(field)) AS value FROM ssl_certificate_input")
			aggregate.Prepared = prepared
			run(aggregate)
			conditional := fixture.Select("SELECT id,COALESCE(CASE WHEN id=1 THEN ssl_part.ssl_client_dn_field(field || '') ELSE ssl_part.ssl_issuer_field(field) END,'plain')::varchar AS value FROM ssl_certificate_input ORDER BY id", []string{"id", "value"}, []string{"INT4", "VARCHAR"}, [][]any{{int64(1), "plain"}, {int64(2), "plain"}, {int64(3), "plain"}})
			conditional.Prepared = prepared
			run(conditional)
		}
		run(fixture.ImplicitError("CREATE TABLE ssl_certificate_generated(field text,value text GENERATED ALWAYS AS (ssl_part.ssl_client_dn_field(field)) STORED)", fixture.Diagnostic{Code: "42P17", Message: "generation expression is not immutable"}))
		run(fixture.ImplicitError("CREATE INDEX ssl_certificate_volatile_idx ON ssl_certificate_input ((ssl_part.ssl_issuer_field(field)))", fixture.Diagnostic{Code: "42P17", Message: "functions in index expression must be marked IMMUTABLE"}))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname IN ('ssl_certificate_generated','ssl_certificate_volatile_idx')", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Select("SELECT id,client,issuer FROM ssl_certificate_saved ORDER BY id", []string{"id", "client", "issuer"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), nil, nil}, {int64(2), nil, nil}, {int64(3), nil, nil}}))
	})
}
