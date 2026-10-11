package fixture

// Raw assertions use the cohesive fixture's extension hook rather than
// changing or splitting its existing oversized implementation.
import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
)

func RegtypeScenario(t *testing.T, body func(*testing.T, *Pair, Backend, *sql.Conn, *Recorder, *[]Diagnostic)) {
	t.Helper()
	p := NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			t.Cleanup(func() {
				for _, schema := range []string{"regtype_a", "regtype_b", "regtype_private", "regtype_external", "regtype_renamed", "Odd.Regtype", "regtype_reader"} {
					if _, err := backend.DB.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS "+RegtypeIdentifier(schema)+" CASCADE"); err != nil {
						t.Error(err)
					}
				}
			})
			var notices []Diagnostic
			_, conn, recorder := NoticeReferencePool(t, p, backend.PG, &notices)
			body(t, p, backend, conn, recorder, &notices)
		})
	}
}

func RegtypeIdentifier(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func RegtypeLiteral(s string) string    { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func RegtypeSetup(t *testing.T, p *Pair, conn *sql.Conn, recorder *Recorder) {
	t.Helper()
	for _, q := range []string{
		"CREATE SCHEMA regtype_a", "CREATE SCHEMA regtype_b", `CREATE SCHEMA "Odd.Regtype"`,
		"CREATE DOMAIN regtype_a.same AS integer", "CREATE DOMAIN regtype_b.same AS text",
		"CREATE TYPE regtype_a.mood AS ENUM ('ok','sad')",
		`CREATE DOMAIN "Odd.Regtype"."a""b.dot" AS integer`,
		`CREATE DOMAIN regtype_a."Select" AS integer`, `CREATE DOMAIN regtype_a."23" AS integer`, `CREATE DOMAIN regtype_a."-" AS integer`,
		"SET search_path TO regtype_a,regtype_b,public",
	} {
		RunStep(t, p.Ctx, conn, recorder, Exec(q, 0))
	}
}

func RegtypeBoth(t *testing.T, p *Pair, db Executor, recorder *Recorder, s Step) {
	t.Helper()
	// Keep the same independently expected body for direct and prepared routes.
	for _, prepared := range []bool{false, true} {
		s.Prepared = prepared
		RegtypeRunStep(t, p.Ctx, db, recorder, s)
	}
}

func RegtypeMetadata(t *testing.T, rows *sql.Rows) {
	t.Helper()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range types {
		length, lengthOK := typ.Length()
		precision, scale, decimalOK := typ.DecimalSize()
		if length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK {
			t.Fatalf("regtype metadata %s length=%d/%t decimal=%d,%d/%t", typ.Name(), length, lengthOK, precision, scale, decimalOK)
		}
	}
}

func RegtypeSelect(query string, values ...any) Step {
	columns := make([]string, len(values))
	types := make([]string, len(values))
	for i := range values {
		columns[i] = string(rune('a' + i))
		types[i] = "REGTYPE"
	}
	s := Select(query, columns, types, [][]any{values})
	s.CheckMetadata = RegtypeMetadata
	return s
}

func RegtypeAssertError(t *testing.T, err error, state, primary, query, route, stage string) {
	t.Helper()
	if err == nil {
		t.Fatalf("route=%s query=%q expected %s, got success", route, query, state)
	}
	want, expectedStage := regtypeErrorExpectation(state, primary, query, route)
	if stage != expectedStage {
		t.Fatalf("route=%s error stage=%s want %s", route, stage, expectedStage)
	}
	AssertDiagnostic(t, err, &want)
}

func RegtypeScan(t *testing.T, rows *sql.Rows, index int, postgres bool) {
	t.Helper()
	var raw any
	var text sql.NullString
	var bytes []byte
	if err := rows.Scan(&raw, &text, &bytes); err != nil {
		t.Fatal(err)
	}
	if raw == nil && (text.Valid || text.String != "" || bytes != nil) {
		t.Fatal("SQL NULL specialized scans", raw, text, bytes)
	}
	if raw != nil {
		RegtypeRaw(t, postgres, raw)
		if !text.Valid || text.String != string(bytes) || Normalize(raw) != text.String {
			t.Fatal("specialized regtype scans disagree", raw, text, bytes)
		}
	}
}

func RegtypeAssertRows(t *testing.T, rows *sql.Rows, want [][]any, postgres bool) {
	t.Helper()
	RegtypeAssertRowsWithMetadata(t, rows, want, postgres, RegtypeMetadata)
}

func RegtypeAssertRowsWithMetadata(t *testing.T, rows *sql.Rows, want [][]any, postgres bool, checkMetadata func(*testing.T, *sql.Rows)) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	checkMetadata(t, rows)
	var got [][]any
	for rows.Next() {
		row := make([]any, len(columns))
		dest := make([]any, len(row))
		for i := range row {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i := range row {
			family := types[i].DatabaseTypeName()
			if family == "REGTYPE" || family == "_REGTYPE" || family == "_IDENTIFIER" || family == "" {
				RegtypeRaw(t, postgres, row[i])
			} else {
				AssertRawValue(t, postgres, family, row[i])
			}
			row[i] = Normalize(row[i])
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("regtype rows got %#v want %#v", got, want)
	}
}

func RegtypeRaw(t *testing.T, postgres bool, value any) {
	t.Helper()
	if value == nil {
		return
	}
	want := reflect.TypeFor[string]()
	if postgres {
		want = reflect.TypeFor[[]byte]()
	}
	if reflect.TypeOf(value) != want {
		t.Fatalf("regtype raw postgres=%t got %T/%#v want %v", postgres, value, value, want)
	}
	if bytes, ok := value.([]byte); ok && bytes == nil {
		t.Fatal("regtype typed nil bytes are not SQL NULL")
	}
}

func RegtypeRunStep(t *testing.T, ctx context.Context, db Executor, recorder *Recorder, s Step) Observed {
	t.Helper()
	metadata := s.CheckMetadata
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		RegtypeFamilyMetadata(t, rows, recorder.RawBackend == RawPostgres, s.Types)
		allRegtype := len(s.Types) > 0
		for _, family := range s.Types {
			if family != "REGTYPE" {
				allRegtype = false
			}
		}
		if allRegtype && !strings.Contains(s.Query, " FROM ") {
			RegtypeCursorMetadata(t, rows, recorder.RawBackend == RawPostgres, s.Columns, s.Types, nil)
		}
		if metadata != nil {
			metadata(t, rows)
		}
	}
	previous := s.CheckRaw
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		family := s.Types[column]
		if s.RawTypes != nil {
			family = s.RawTypes[column]
		}
		if family == "REGTYPE" || family == "_REGTYPE" {
			RegtypeRaw(t, recorder.RawBackend == RawPostgres, value)
			return true
		}
		return previous != nil && previous(t, column, row, value)
	}
	return RunStep(t, ctx, db, recorder, s)
}

func RegtypeAssertNotices(t *testing.T, got, want []Diagnostic) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("notice count got %#v want %#v", got, want)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("notice %d got %#v want %#v", i, got[i], want[i])
		}
	}
}

func RegtypeFamilyMetadata(t *testing.T, rows *sql.Rows, postgres bool, families []string) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, typ := range columns {
		if families[i] != "REGTYPE" && families[i] != "_REGTYPE" {
			continue
		}
		scan := reflect.TypeFor[string]()
		if postgres {
			scan = reflect.TypeFor[any]()
		}
		length, lengthOK := typ.Length()
		precision, scale, decimalOK := typ.DecimalSize()
		nullable, nullableOK := typ.Nullable()
		if typ.DatabaseTypeName() != families[i] || typ.ScanType() != scan || length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK || postgres && (nullable || nullableOK) {
			t.Fatalf("regtype metadata %s type=%s scan=%v length=%d/%t decimal=%d,%d/%t nullable=%t/%t", typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK)
		}
	}
}

func RegtypeCursorMetadata(t *testing.T, rows *sql.Rows, postgres bool, names, families []string, nullable []bool) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != len(names) {
		t.Fatalf("metadata column count %d want %d", len(columns), len(names))
	}
	RegtypeMetadata(t, rows)
	for i, typ := range columns {
		scan := reflect.TypeFor[string]()
		if postgres {
			scan = reflect.TypeFor[any]()
		}
		switch families[i] {
		case "INT4":
			scan = reflect.TypeFor[int64]()
			if postgres {
				scan = reflect.TypeFor[int32]()
			}
		case "INT8":
			scan = reflect.TypeFor[int64]()
		}
		if typ.Name() != names[i] || typ.DatabaseTypeName() != families[i] || typ.ScanType() != scan {
			t.Fatalf("metadata column %d name=%s type=%s scan=%v want %s/%s/%v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), names[i], families[i], scan)
		}
		got, known := typ.Nullable()
		if postgres {
			if got || known {
				t.Fatalf("pq nullable %s = %t/%t", typ.Name(), got, known)
			}
		} else if nullable != nil {
			if !known || got != nullable[i] {
				t.Fatalf("native nullable %s = %t/%t want %t/true", typ.Name(), got, known, nullable[i])
			}
		} else if got || known {
			t.Fatalf("computed native nullable %s = %t/%t want false/false", typ.Name(), got, known)
		}
	}
}
