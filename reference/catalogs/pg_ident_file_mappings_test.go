package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const identSelect = "SELECT map_number,file_name,line_number,map_name,sys_name,pg_username,error FROM pg_catalog.pg_ident_file_mappings"

var identColumns = []string{"map_number", "file_name", "line_number", "map_name", "sys_name", "pg_username", "error"}
var identTypes = []string{"INT4", "TEXT", "INT4", "TEXT", "TEXT", "TEXT", "TEXT"}

const identMissing = "missing entry at end of line"
const identMultiple = "multiple values in ident field"
const identRegex = "invalid regular expression \"[\": brackets [] not balanced"

func identMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, identColumns) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 7 {
		t.Fatal(types, err)
	}
	fixture.ScalarAssertMetadata(t, pg, types)
	for i, typ := range types {
		if typ.Name() != identColumns[i] || typ.DatabaseTypeName() != identTypes[i] {
			t.Fatal(i, typ)
		}
		nullable, known := typ.Nullable()
		if pg && (nullable || known) || !pg && (!nullable || !known) {
			t.Fatal(i, nullable, known)
		}
		length, lengthKnown := typ.Length()
		precision, scale, decimalKnown := typ.DecimalSize()
		if precision != 0 || scale != 0 || decimalKnown {
			t.Fatal(i, precision, scale, decimalKnown)
		}
		if identTypes[i] == "TEXT" {
			if length != math.MaxInt64 || !lengthKnown || typ.ScanType() != reflect.TypeOf("") {
				t.Fatal(i, length, lengthKnown, typ.ScanType())
			}
		} else if lengthKnown {
			t.Fatal(i, length)
		}
	}
}
func identRead(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query string, want [][]any, prepared bool) {
	t.Helper()
	step := fixture.Select(query, identColumns, identTypes, want)
	step.Prepared = prepared
	step.Bag = !strings.Contains(strings.ToUpper(query), "ORDER BY")
	step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { identMetadata(t, rows, b.PG) }
	fixture.RunStep(t, p.Ctx, e, b.Recorder, step)
}
func identValid(number int64, file string, line int64, name, system, user string) []any {
	return []any{number, file, line, name, system, user, nil}
}
func identInvalid(file string, line int64, diagnostic string) []any {
	return []any{nil, file, line, nil, nil, nil, diagnostic}
}

func TestReferencePGIdentFileMappingsRows(t *testing.T) {
	p, files := fixture.NewIdentFileScenario(t, "# heading\nmap_a os_a db_a\n\nmissing\nmap_bad x,y db\nrx /[ db\nmap_b os_b db_b trailing ignored\n", nil)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			main := files.Path(b, "")
			want := [][]any{identValid(1, main, 2, "map_a", "os_a", "db_a"), identInvalid(main, 4, identMissing), identInvalid(main, 5, identMultiple), identInvalid(main, 6, identRegex), identValid(2, main, 7, "map_b", "os_b", "db_b")}
			for _, prepared := range []bool{false, true} {
				identRead(t, p, b, b.DB, identSelect+" ORDER BY line_number", want, prepared)
				identRead(t, p, b, b.DB, "SELECT * FROM pg_ident_file_mappings ORDER BY line_number", want, prepared)
				identRead(t, p, b, b.DB, identSelect+" WHERE error IS NULL ORDER BY map_number", [][]any{want[0], want[4]}, prepared)
				if !b.PG {
					s := fixture.Exec(identSelect, 5)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
			identCatalogMetadata(t, p, b)
			// Independently pin builtin identities and all exposed routine declarations.
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT c.oid::int4,c.reltype::int4,t.typarray::int4 FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='pg_catalog.pg_ident_file_mappings'::regclass", []string{"oid", "reltype", "typarray"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(12118), int64(12120), int64(12119)}}))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT pg_get_viewdef('pg_ident_file_mappings'::regclass)", []string{"pg_get_viewdef"}, []string{"TEXT"}, [][]any{{" SELECT map_number,\n    file_name,\n    line_number,\n    map_name,\n    sys_name,\n    pg_username,\n    error\n   FROM pg_ident_file_mappings() a(map_number, file_name, line_number, map_name, sys_name, pg_username, error);"}}))
		})
	}
}

func TestReferencePGIdentFileMappingsFreshness(t *testing.T) {
	p, files := fixture.NewIdentFileScenario(t, "a s p\n", nil)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			query := identSelect + " ORDER BY map_number"
			stmt, err := b.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			files.Write(t, p.Ctx, b, "", "b s p\n")
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
			want := [][]any{identValid(1, files.Path(b, ""), 1, "b", "s", "p")}
			identRead(t, p, b, held, query, want, true)
			old, err := stmt.QueryContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			files.Write(t, p.Ctx, b, "", "c s p\n")
			next := [][]any{identValid(1, files.Path(b, ""), 1, "c", "s", "p")}
			identRead(t, p, b, b.DB, query, next, false)
			identRead(t, p, b, held, query, next, true)
			files.Remove(t, p.Ctx, b, "")
			step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "58P01", Message: "could not open file \"" + files.Path(b, "") + "\": No such file or directory"})
			step.SelectRows = true
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
			var got [][]any
			for old.Next() {
				values := make([]any, 7)
				targets := make([]any, 7)
				for i := range values {
					targets[i] = &values[i]
				}
				if err := old.Scan(targets...); err != nil {
					t.Fatal(err)
				}
				got = append(got, values)
			}
			if err := old.Err(); err != nil {
				t.Fatal(err)
			}
			// pq returns text raw cells as []byte; Scan into concrete nullable values is
			// handled separately below rather than erasing provenance from comparison.
			if len(got) != 1 {
				t.Fatal(got)
			}
			for i, v := range got[0] {
				if bytes, ok := v.([]byte); ok {
					got[0][i] = string(bytes)
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatal(got, want)
			}
			files.Write(t, p.Ctx, b, "", "c s p\n")
			identRead(t, p, b, held, query, next, true)
		})
	}
}
