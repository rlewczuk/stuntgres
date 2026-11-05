package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func sslInfoCertificateRetained(t *testing.T, rows *sql.Rows, columns, types []string, want [][]any) {
	t.Helper()
	defer rows.Close()
	gotColumns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(gotColumns, columns) {
		t.Fatal("retained columns", gotColumns, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != len(types) {
		t.Fatal("retained metadata", meta, err)
	}
	for i, col := range meta {
		if col.DatabaseTypeName() != types[i] {
			t.Fatalf("retained type %d=%s want=%s", i, col.DatabaseTypeName(), types[i])
		}
	}
	var got [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		for i := range values {
			values[i] = fixture.Normalize(values[i])
		}
		got = append(got, values)
	}
	if rows.Err() != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("retained rows=%#v want=%#v err=%v", got, want, rows.Err())
	}
}

func TestReferenceSSLInfoCertificateControls(t *testing.T) {
	sslInfoCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sslInfoInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE SEQUENCE ssl_control_seq START 1", 0))
		run(fixture.Exec("CREATE TABLE ssl_control_input(id integer,field text)", 0))
		run(fixture.Exec("INSERT INTO ssl_control_input VALUES(1,'CN'),(2,NULL)", 2))
		run(fixture.Exec("CREATE VIEW ssl_control_values AS SELECT id,ssl_part.ssl_client_dn_field(field || '') AS value FROM ssl_control_input", 0))
		run(fixture.Exec("CREATE VIEW ssl_control_extensions AS SELECT * FROM ssl_part.ssl_extension_info()", 0))
		run(fixture.Exec("CREATE TABLE ssl_control_saved(id integer,value text DEFAULT ssl_part.ssl_client_dn_field(nextval('ssl_control_seq')::text))", 0))
		const viewSQL = "SELECT id,value FROM ssl_control_values ORDER BY id"
		const paramSQL = "SELECT ssl_part.ssl_issuer_field($1) AS value"
		const insertSQL = "INSERT INTO ssl_control_saved(id) VALUES($1) RETURNING value"
		const sourceSQL = "SELECT * FROM ssl_control_extensions"
		const errorSQL = "SELECT ssl_part.ssl_issuer_field(($1::integer)::text) AS value"
		held := make(map[string]fixture.HeldStatement)
		for _, q := range []string{viewSQL, paramSQL, insertSQL, sourceSQL, errorSQL} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held[q] = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		}
		view := func() fixture.Step {
			return sslInfoCertificateMetadata(fixture.Select(viewSQL, []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}, {int64(2), nil}}), !b.PG)
		}
		source := func() fixture.Step {
			return sslInfoCertificateMetadata(fixture.Select(sourceSQL, []string{"name", "value", "critical"}, []string{"TEXT", "TEXT", "BOOL"}, nil), !b.PG)
		}
		sequence := func(target *sql.Conn, r *fixture.Recorder, n int64) {
			t.Helper()
			fixture.RunStep(t, p.Ctx, target, r, fixture.Select("SELECT last_value,is_called FROM ssl_control_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{n, true}}))
		}
		observe := func() {
			t.Helper()
			for _, s := range []fixture.Step{view(), source()} {
				for _, prepared := range []bool{false, true} {
					s.Prepared = prepared
					run(s)
				}
				s.Prepared = false
				fixture.RunStep(t, p.Ctx, held[s.Query], b.Recorder, s)
			}
		}
		observe()
		fixture.RunStep(t, p.Ctx, held[paramSQL], b.Recorder, sslInfoCertificateScalar(paramSQL, "CN"))
		fixture.RunStep(t, p.Ctx, held[insertSQL], b.Recorder, sslInfoCertificateScalar(insertSQL, int64(1)))
		run(sslInfoCertificateScalar(insertSQL, int64(2)))
		run(fixture.Prepared(sslInfoCertificateScalar(insertSQL, int64(3))))
		sequence(c, b.Recorder, 3)
		// Stored dependencies, not the old SQL spelling, follow genuine rename/move.
		run(fixture.Exec("ALTER FUNCTION ssl_part.ssl_client_dn_field(text) RENAME TO ssl_certificate_client", 0))
		run(fixture.Exec("ALTER FUNCTION ssl_part.ssl_certificate_client(text) SET SCHEMA public", 0))
		run(fixture.Exec("ALTER FUNCTION ssl_part.ssl_extension_info() RENAME TO ssl_certificate_extensions", 0))
		run(fixture.Exec("ALTER FUNCTION ssl_part.ssl_certificate_extensions() SET SCHEMA public", 0))
		run(sslInfoCertificateScalar("SELECT public.ssl_certificate_client('O') AS value"))
		run(sslInfoCertificateSource("SELECT * FROM public.ssl_certificate_extensions()"))
		observe()
		if b.PG {
			return
		}
		capture := func(query string) (*sql.Conn, *sql.Rows) {
			t.Helper()
			owner, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { owner.Close() })
			rows, err := owner.QueryContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { rows.Close() })
			return owner, rows
		}
		scalarOwner, oldScalar := capture("SELECT value FROM ssl_control_values WHERE id=1")
		sourceOwner, oldSource := capture(sourceSQL)
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		sequence(c, b.Recorder, 3)
		schemaAt, extensionAt := strings.Index(dump, "CREATE SCHEMA"), strings.Index(dump, "CREATE EXTENSION")
		if schemaAt < 0 || extensionAt <= schemaAt || !strings.Contains(dump, "VERSION '1.2'") {
			t.Fatal("symbolic provider ordering", dump)
		}
		for _, consumer := range []string{"CREATE TABLE", "CREATE VIEW"} {
			if at := strings.Index(dump, consumer); at <= extensionAt {
				t.Fatal("consumer precedes provider", consumer, dump)
			}
		}
		for _, recipe := range []string{"ssl_certificate_client", "ssl_certificate_extensions", "nextval", "ssl_control_seq", "field"} {
			if !strings.Contains(dump, recipe) {
				t.Fatal("missing symbolic argument recipe", recipe, dump)
			}
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		sslInfoCertificateRetained(t, oldScalar, []string{"value"}, []string{"TEXT"}, [][]any{{nil}})
		sslInfoCertificateRetained(t, oldSource, []string{"name", "value", "critical"}, []string{"TEXT", "TEXT", "BOOL"}, nil)
		if err := scalarOwner.Close(); err != nil {
			t.Fatal(err)
		}
		if err := sourceOwner.Close(); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='sslinfo'", []string{"extname"}, []string{"NAME"}, nil))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname IN ('ssl_control_input','ssl_control_saved','ssl_control_values','ssl_control_extensions')", []string{"relname"}, []string{"NAME"}, nil))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		sslInfoCertificateIdentities(t, p, c)
		observe()
		fixture.RunStep(t, p.Ctx, held[paramSQL], b.Recorder, sslInfoCertificateScalar(paramSQL, "not-a-field"))
		bad := fixture.ErrorDiagnostic(errorSQL, sslInfoCertificateIntegerError(), "bad")
		bad.SelectRows = true
		bad.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held[errorSQL], b.Recorder, bad)
		fixture.RunStep(t, p.Ctx, held[errorSQL], b.Recorder, sslInfoCertificateScalar(errorSQL, int64(7)))
		fixture.RunStep(t, p.Ctx, held[insertSQL], b.Recorder, sslInfoCertificateScalar(insertSQL, int64(4)))
		sequence(c, b.Recorder, 4)
		// An independently constructed Driver consumes the unchanged source snapshot.
		other, target, recorder := pgcryptoControlDestination(t, p, "sslinfo_certificate_destination")
		if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		sslInfoCertificateIdentities(t, p, target)
		for _, prepared := range []bool{false, true} {
			for _, s := range []fixture.Step{view(), source(), sslInfoCertificateScalar("SELECT public.ssl_certificate_client($1) AS value", "CN")} {
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, target, recorder, s)
			}
			id := int64(4)
			if prepared {
				id = 5
			}
			s := sslInfoCertificateScalar(insertSQL, id)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, target, recorder, s)
		}
		sequence(target, recorder, 5)
		sequence(c, b.Recorder, 4)
		run(fixture.Select("SELECT id,value FROM ssl_control_saved ORDER BY id", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}, {int64(2), nil}, {int64(3), nil}, {int64(4), nil}}))
		before := fixture.ExtensionDump(t, p.Ctx, p.Native)
		beforeIDs := sslInfoCertificateIdentities(t, p, c)
		failedScalarOwner, failedScalar := capture("SELECT value FROM ssl_control_values WHERE id=1")
		failedSourceOwner, failedSource := capture(sourceSQL)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump+"\nINVALID SQL;")); err == nil {
			t.Fatal("invalid replacement accepted")
		}
		if after := fixture.ExtensionDump(t, p.Ctx, p.Native); after != before {
			t.Fatal("failed restore changed committed snapshot")
		}
		if after := sslInfoCertificateIdentities(t, p, c); !reflect.DeepEqual(beforeIDs, after) {
			t.Fatalf("failed restore changed identities: %v -> %v", beforeIDs, after)
		}
		sequence(c, b.Recorder, 4)
		sslInfoCertificateRetained(t, failedScalar, []string{"value"}, []string{"TEXT"}, [][]any{{nil}})
		sslInfoCertificateRetained(t, failedSource, []string{"name", "value", "critical"}, []string{"TEXT", "TEXT", "BOOL"}, nil)
		if err := failedScalarOwner.Close(); err != nil {
			t.Fatal(err)
		}
		if err := failedSourceOwner.Close(); err != nil {
			t.Fatal(err)
		}
		observe()
		fixture.RunStep(t, p.Ctx, held[paramSQL], b.Recorder, sslInfoCertificateScalar(paramSQL, nil))
		fixture.RunStep(t, p.Ctx, held[insertSQL], b.Recorder, sslInfoCertificateScalar(insertSQL, int64(5)))
		sequence(c, b.Recorder, 5)
	})
}
