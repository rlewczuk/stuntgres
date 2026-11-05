package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func sslInfoCertificateMetadata(s fixture.Step, known bool, knownColumns ...int) fixture.Step {
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		meta, err := rows.ColumnTypes()
		if err != nil || len(meta) != len(s.Types) {
			t.Fatal("certificate metadata", meta, err)
		}
		for i, col := range meta {
			if col.Name() != s.Columns[i] || col.DatabaseTypeName() != s.Types[i] {
				t.Fatalf("column %d: %s/%s", i, col.Name(), col.DatabaseTypeName())
			}
			columnKnown := known
			for _, index := range knownColumns {
				columnKnown = columnKnown || i == index
			}
			if nullable, ok := col.Nullable(); ok != columnKnown || nullable != columnKnown {
				t.Fatalf("column %s nullability=(%v,%v), want=(%v,%v)", col.Name(), nullable, ok, columnKnown, columnKnown)
			}
		}
	}
	return s
}

func sslInfoCertificateSource(query string) fixture.Step {
	return sslInfoCertificateMetadata(fixture.Select(query, []string{"name", "value", "critical"}, []string{"TEXT", "TEXT", "BOOL"}, nil), false)
}

func TestReferenceSSLInfoCertificateSources(t *testing.T) {
	sslInfoCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sslInfoInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		var nullExtended []int
		if !b.PG {
			// The native outer-join namespace proves these outputs nullable;
			// lib/pq exposes unknown nullability for every computed column.
			nullExtended = []int{1, 2, 3}
		}
		steps := []fixture.Step{
			sslInfoCertificateSource("SELECT * FROM ssl_part.ssl_extension_info()"),
			sslInfoCertificateMetadata(fixture.Select("SELECT * FROM ssl_part.ssl_extension_info() AS e(n,v,c)", []string{"n", "v", "c"}, []string{"TEXT", "TEXT", "BOOL"}, nil), false),
			sslInfoCertificateMetadata(fixture.Select("SELECT * FROM ssl_part.ssl_extension_info() WITH ORDINALITY", []string{"name", "value", "critical", "ordinality"}, []string{"TEXT", "TEXT", "BOOL", "INT8"}, nil), false),
			sslInfoCertificateMetadata(fixture.Select("SELECT i.id,e.name,e.value,e.critical FROM (VALUES(1)) AS i(id) LEFT JOIN ssl_part.ssl_extension_info() AS e ON true", []string{"id", "name", "value", "critical"}, []string{"INT4", "TEXT", "TEXT", "BOOL"}, [][]any{{int64(1), nil, nil, nil}}), false, nullExtended...),
			sslInfoCertificateMetadata(fixture.Select("SELECT ssl_part.ssl_extension_info()", []string{"ssl_extension_info"}, []string{"RECORD"}, nil), false),
			sslInfoCertificateSource("SELECT (ssl_part.ssl_extension_info()).*"),
			sslInfoCertificateMetadata(fixture.Select("SELECT 1 AS id,ssl_part.ssl_extension_info()", []string{"id", "ssl_extension_info"}, []string{"INT4", "RECORD"}, nil), false),
			sslInfoCertificateMetadata(fixture.Select("SELECT generate_series(1,2) AS n,ssl_part.ssl_extension_info() AS cert ORDER BY n", []string{"n", "cert"}, []string{"INT4", "RECORD"}, [][]any{{int64(1), nil}, {int64(2), nil}}), false),
		}
		for _, s := range steps {
			for _, prepared := range []bool{false, true} {
				s.Prepared = prepared
				run(s)
			}
		}
		q := "SELECT * FROM ssl_part.ssl_extension_info()"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		h := fixture.HeldStatement{Executor: c, Statement: held, SQL: q}
		for range 2 {
			fixture.RunStep(t, p.Ctx, h, b.Recorder, sslInfoCertificateSource(q))
		}
	})
}
