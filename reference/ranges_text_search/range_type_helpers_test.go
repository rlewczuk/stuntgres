package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// The shared callback owns all SQL in both modes.
func rangeTypeScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, func(fixture.Step))) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			body(t, p, b, conn, run)
		})
	}
}

func rangeTypeSetup(run func(fixture.Step)) {
	run(fixture.Exec(`CREATE TYPE public.custom_span AS RANGE (SUBTYPE=integer,SUBTYPE_OPCLASS=pg_catalog.int4_ops,MULTIRANGE_TYPE_NAME=public.custom_spans)`, 0))
	run(fixture.Exec(`CREATE TABLE range_data(k integer NOT NULL,r public.custom_span,m public.custom_spans)`, 0))
}

func rangeTypeSeed(run func(fixture.Step)) {
	run(fixture.Exec(`INSERT INTO range_data VALUES (1,'[1,3]','{[1,3]}'),(2,'[1,4)','{[1,3),[2,4),empty,[8,9)}'),(3,'[4,4]','{}'),(4,'[4,4)',NULL),(5,'[2147483647,2147483647]','{[2147483647,2147483647]}')`, 5))
}

func rangeTypeRows() [][]any {
	return [][]any{{int64(1), "[1,3]", "{[1,3]}"}, {int64(2), "[1,4)", "{[1,4),[8,9)}"}, {int64(3), "[4,4]", "{}"}, {int64(4), "empty", nil}, {int64(5), "[2147483647,2147483647]", "{[2147483647,2147483647]}"}}
}

// Custom OIDs are unknown to pq regardless of the spelling of the type name.
// Do not route them through the builtin-only rangeSelect family classifier.
func rangeTypeSelect(b fixture.Backend, q string, columns, names []string, rows [][]any, nullable []bool, args ...any) fixture.Step {
	types := append([]string(nil), names...)
	if b.PG {
		for i, name := range names {
			if name != "INT4" {
				types[i] = ""
			}
		}
	}
	s := fixture.Select(q, columns, types, rows, args...)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if names[column] == "INT4" {
			return false
		}
		if value != nil {
			want := reflect.TypeFor[string]()
			if b.PG {
				want = reflect.TypeFor[[]byte]()
			}
			if reflect.TypeOf(value) != want {
				t.Fatalf("column %d row %d raw %T, want %v", column, row, value, want)
			}
			if v, ok := value.([]byte); ok && v == nil {
				t.Fatal("typed nil is not SQL NULL")
			}
		}
		return true
	}
	s.CheckMetadata = func(t *testing.T, result *sql.Rows) {
		metadata, err := result.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, typ := range metadata {
			want := reflect.TypeFor[string]()
			if names[i] == "INT4" {
				want = reflect.TypeFor[int64]()
			}
			if b.PG {
				want = reflect.TypeFor[any]()
				if names[i] == "INT4" {
					want = reflect.TypeFor[int32]()
				}
			}
			if typ.ScanType() != want {
				t.Fatalf("%s ScanType %v, want %v", columns[i], typ.ScanType(), want)
			}
			if n, known := typ.Length(); n != 0 || known {
				t.Fatalf("length %d/%v", n, known)
			}
			if p, scale, known := typ.DecimalSize(); p != 0 || scale != 0 || known {
				t.Fatalf("precision %d/%d/%v", p, scale, known)
			}
			n, known := typ.Nullable()
			if b.PG || nullable == nil {
				if n || known {
					t.Fatalf("nullable %v/%v, want unknown", n, known)
				}
			} else if !known || n != nullable[i] {
				t.Fatalf("nullable %v/%v, want %v/true", n, known, nullable[i])
			}
		}
	}
	return s
}

func rangeTypeData(b fixture.Backend, rows [][]any) fixture.Step {
	return rangeTypeSelect(b, `SELECT k,r,m FROM range_data ORDER BY k`, []string{"k", "r", "m"}, []string{"INT4", "CUSTOM_SPAN", "CUSTOM_SPANS"}, rows, []bool{false, true, true})
}

func rangeTypeClass(name, comparator string) string {
	return `CREATE OPERATOR CLASS public.` + name + ` FOR TYPE integer USING btree AS OPERATOR 1 pg_catalog.<, OPERATOR 2 pg_catalog.<=, OPERATOR 3 pg_catalog.=, OPERATOR 4 pg_catalog.>=, OPERATOR 5 pg_catalog.>, FUNCTION 1 pg_catalog.` + comparator + `(integer,integer)`
}

func rangeTypeAbsent(run func(fixture.Step), names ...string) {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = rangeLiteral(name)
	}
	run(fixture.Select(`SELECT typname FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN (`+strings.Join(quoted, ",")+`) ORDER BY typname`, []string{"typname"}, []string{"NAME"}, nil))
}
