package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func versionInformationScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			body(t, p, backend, conn)
		})
	}
}

func versionInformationMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"version", "unicode_version", "icu_unicode_version"}
	if !reflect.DeepEqual(columns, want) {
		t.Fatalf("columns = %#v, want %#v", columns, want)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range types {
		if typ.DatabaseTypeName() != "TEXT" {
			t.Errorf("%s type = %q, want TEXT", typ.Name(), typ.DatabaseTypeName())
		}
		if typ.ScanType() != reflect.TypeOf("") {
			t.Errorf("%s ScanType = %v, want string", typ.Name(), typ.ScanType())
		}
		nullable, nullableOK := typ.Nullable()
		length, lengthOK := typ.Length()
		precision, scale, decimalOK := typ.DecimalSize()
		if nullable || nullableOK || length != 1<<63-1 || !lengthOK || precision != 0 || scale != 0 || decimalOK {
			t.Errorf("%s metadata (PostgreSQL=%v): nullable=%v/%v length=%d/%v decimal=%d,%d/%v", typ.Name(), pg, nullable, nullableOK, length, lengthOK, precision, scale, decimalOK)
		}
	}
}

func TestReferenceVersionInformationValues(t *testing.T) {
	versionInformationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
		const query = "SELECT version(), unicode_version(), icu_unicode_version()"
		read := func(t *testing.T, rows *sql.Rows, err error) {
			t.Helper()
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			versionInformationMetadata(t, rows, b.PG)
			if !rows.Next() {
				t.Fatalf("missing inquiry row: %v", rows.Err())
			}
			var banner, unicode string
			var icu sql.NullString
			if err := rows.Scan(&banner, &unicode, &icu); err != nil {
				t.Fatal(err)
			}
			var rawBanner, rawUnicode, rawICU any
			if err := rows.Scan(&rawBanner, &rawUnicode, &rawICU); err != nil {
				t.Fatal(err)
			}
			if _, ok := rawBanner.(string); !ok {
				t.Errorf("raw banner = %T, want string", rawBanner)
			}
			if _, ok := rawUnicode.(string); !ok {
				t.Errorf("raw Unicode = %T, want string", rawUnicode)
			}
			var wantRawICU any
			if b.PG {
				wantRawICU = "15.0"
			}
			if rawICU != wantRawICU {
				t.Errorf("raw ICU = %#v, want %#v", rawICU, wantRawICU)
			}
			if b.PG {
				if !strings.HasPrefix(banner, "PostgreSQL 18.6 ") || strings.Contains(banner, "embedded stuntgres") {
					t.Errorf("unexpected pinned PostgreSQL banner %q", banner)
				}
			} else if banner != "PostgreSQL 18.0 compatible; embedded stuntgres" {
				t.Errorf("native banner = %q", banner)
			}
			if unicode != "16.0" {
				t.Errorf("Unicode = %q, want 16.0", unicode)
			}
			wantICU := sql.NullString{}
			if b.PG {
				wantICU = sql.NullString{String: "15.0", Valid: true}
			}
			if icu != wantICU {
				t.Errorf("ICU = %#v, want %#v", icu, wantICU)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatalf("unexpected trailing inquiry row/error: %v", rows.Err())
			}
		}
		rows, err := conn.QueryContext(p.Ctx, query)
		read(t, rows, err)
		stmt, err := conn.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for range 2 {
			rows, err := stmt.QueryContext(p.Ctx)
			read(t, rows, err)
		}
		for _, query := range []string{
			`SELECT "version"(), "unicode_version"(), "icu_unicode_version"()`,
			"SELECT pg_catalog.version(), pg_catalog.unicode_version(), pg_catalog.icu_unicode_version()",
			"SELECT VeRsIoN(), UnIcOdE_VeRsIoN(), IcU_UnIcOdE_VeRsIoN()",
		} {
			rows, err := conn.QueryContext(p.Ctx, query)
			read(t, rows, err)
			variant, err := conn.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			rows, err = variant.QueryContext(p.Ctx)
			// Register before assertions so failure also closes the held handle.
			t.Cleanup(func() { variant.Close() })
			read(t, rows, err)
			if err := variant.Close(); err != nil {
				t.Fatal(err)
			}
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT icu_unicode_version() IS NULL AS absent", []string{"absent"}, []string{"BOOL"}, [][]any{{!b.PG}})
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
		}
	})
}

func TestReferenceVersionInformationMetadata(t *testing.T) {
	versionInformationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE version_information_rows(id INT)", 0))
		run(fixture.Exec("INSERT INTO version_information_rows VALUES(1),(2)", 2))
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT version(), unicode_version(), icu_unicode_version() FROM version_information_rows WHERE FALSE",
				[]string{"version", "unicode_version", "icu_unicode_version"}, []string{"TEXT", "TEXT", "TEXT"}, nil)
			s.Prepared = prepared
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { versionInformationMetadata(t, rows, b.PG) }
			run(s)
		}
		const query = "SELECT id, unicode_version(), icu_unicode_version() FROM version_information_rows ORDER BY id"
		stmt, err := conn.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		var icu any
		if b.PG {
			icu = "15.0"
		}
		for range 2 {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}, b.Recorder,
				fixture.Select(query, []string{"id", "unicode_version", "icu_unicode_version"}, []string{"INT4", "TEXT", "TEXT"},
					[][]any{{int64(1), "16.0", icu}, {int64(2), "16.0", icu}}))
		}
	})
}

func TestReferenceVersionInformationUnicodeProvider(t *testing.T) {
	versionInformationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
		// The NFC input is U+0065 U+0301; U+1FAE9 was assigned in Unicode 16.
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT unicode_version() AS uv, unicode_assigned(chr(129769)) AS assigned16, unicode_assigned(chr(888)) AS unassigned, normalize('é') AS nfc, normalize('ﬃ',NFKC) AS nfkc",
				[]string{"uv", "assigned16", "unassigned", "nfc", "nfkc"}, []string{"TEXT", "BOOL", "BOOL", "TEXT", "TEXT"},
				[][]any{{"16.0", true, false, "é", "ffi"}})
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
		}
	})
}
