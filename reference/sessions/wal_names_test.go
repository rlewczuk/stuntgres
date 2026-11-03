package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func walNamesScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}
func walNamesQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func walNamesRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
}
func walNamesMetadata(pg bool, names, types []string) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		cols, err := rows.ColumnTypes()
		if err != nil || len(cols) != len(names) {
			t.Fatal(cols, err)
		}
		for i, c := range cols {
			var scan reflect.Type
			switch types[i] {
			case "TEXT":
				scan = reflect.TypeFor[string]()
			case "NUMERIC", "RECORD":
				scan = reflect.TypeFor[string]()
				if pg {
					scan = reflect.TypeFor[any]()
				}
			case "INT4":
				scan = reflect.TypeFor[int64]()
				if pg {
					scan = reflect.TypeFor[int32]()
				}
			case "INT8":
				scan = reflect.TypeFor[int64]()
			default:
				t.Fatal("unspecified WAL output", types[i])
			}
			if c.Name() != names[i] || c.DatabaseTypeName() != types[i] || c.ScanType() != scan {
				t.Fatal(i, c.Name(), c.DatabaseTypeName(), c.ScanType(), names[i], types[i], scan)
			}
			if n, ok := c.Nullable(); n || ok {
				t.Fatal("computed nullability", n, ok)
			}
			length, ok := c.Length()
			wantLength := int64(0)
			wantOK := types[i] == "TEXT"
			if wantOK {
				wantLength = 1<<63 - 1
			}
			if length != wantLength || ok != wantOK {
				t.Fatal("length", length, ok, wantLength, wantOK)
			}
			wantPrecision, wantScale, wantDecimal := int64(0), int64(0), false
			if pg && types[i] == "NUMERIC" {
				// Pinned PG18.6/pq exposes its unconstrained numeric typmod
				// sentinel; native retains unknown precision and scale.
				wantPrecision, wantScale, wantDecimal = 65535, 65531, true
			}
			if precision, scale, ok := c.DecimalSize(); precision != wantPrecision || scale != wantScale || ok != wantDecimal {
				t.Fatal("unconstrained decimal", precision, scale, ok, wantPrecision, wantScale, wantDecimal)
			}
		}
	}
}
func walNamesSelect(pg bool, q string, names, types []string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, names, types, want, args...)
	s.CheckMetadata = walNamesMetadata(pg, names, types)
	s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
		values := make([]any, len(types))
		dest := make([]any, len(types))
		for i := range dest {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, v := range values {
			fixture.AssertRawValue(t, pg, types[i], v)
		}
	}
	return s
}

// Fixed expected vectors are derived from REL_18_6 xlog_internal.h and the
// released misc_functions regression, independently of either backend.
var walNamesVectors = []struct {
	lsn, name, segment string
	offset             int64
}{
	{"0/0", "000000010000000000000000", "0", 0},
	{"0/1", "000000010000000000000000", "0", 1},
	{"0/FFFFFF", "000000010000000000000000", "0", 16777215},
	{"0/1000000", "000000010000000000000001", "1", 0},
	{"0/1000001", "000000010000000000000001", "1", 1},
	{"0/FFFFFFFF", "0000000100000000000000FF", "255", 16777215},
	{"1/0", "000000010000000100000000", "256", 0},
	{"FFFFFFFF/FFFFFFFF", "00000001FFFFFFFF000000FF", "1099511627775", 16777215},
}
var walNamesSplitVectors = []struct {
	name, segment string
	timeline      int64
}{
	{"000000000000000000000000", "0", 0},
	{"000000010000000100000000", "256", 1},
	{"ffffffFF00000001000000af", "431", 4294967295},
	{"000000010000000000000100", "256", 1},
	{"FFFFFFFFFFFFFFFFFFFFFFFF", "1103806594815", 4294967295},
}

func TestReferenceWALNamesValues(t *testing.T) {
	walNamesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, v := range walNamesVectors {
			literal := walNamesQuote(v.lsn) + "::pg_lsn"
			q := "SELECT pg_walfile_name(" + literal + ") AS name,(pg_walfile_name_offset(" + literal + ")).file_name AS same,(pg_walfile_name_offset(" + literal + ")).file_offset AS off,(pg_split_walfile_name(pg_walfile_name(" + literal + "))).segment_number AS seg"
			walNamesRoutes(t, p, b, walNamesSelect(b.PG, q, []string{"name", "same", "off", "seg"}, []string{"TEXT", "TEXT", "INT4", "NUMERIC"}, [][]any{{v.name, v.name, v.offset, v.segment}}))
		}
		for _, v := range walNamesSplitVectors {
			q := "SELECT * FROM pg_catalog.pg_split_walfile_name(file_name => " + walNamesQuote(v.name) + ")"
			walNamesRoutes(t, p, b, walNamesSelect(b.PG, q, []string{"segment_number", "timeline_id"}, []string{"NUMERIC", "INT8"}, [][]any{{v.segment, v.timeline}}))
		}
		walNamesRoutes(t, p, b, walNamesSelect(b.PG, "SELECT * FROM pg_catalog.pg_walfile_name_offset(lsn => '0/1000000')", []string{"file_name", "file_offset"}, []string{"TEXT", "INT4"}, [][]any{{"000000010000000000000001", int64(0)}}))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE wal_names_inputs(id integer,l pg_lsn,n text)", 0))
		for i, v := range walNamesVectors {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO wal_names_inputs VALUES($1,$2,$3)", 1, i, v.lsn, v.name))
		}
		var want [][]any
		for _, v := range walNamesVectors {
			want = append(want, []any{v.name, v.offset, v.segment, int64(1)})
		}
		walNamesRoutes(t, p, b, fixture.Select("SELECT pg_walfile_name(l) AS name,(pg_walfile_name_offset(l)).file_offset AS off,(pg_split_walfile_name(n)).segment_number AS seg,(pg_split_walfile_name(n)).timeline_id AS t FROM wal_names_inputs ORDER BY id", []string{"name", "off", "seg", "t"}, []string{"TEXT", "INT4", "NUMERIC", "INT8"}, want))
	})
}

func TestReferenceWALNamesRecords(t *testing.T) {
	walNamesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct {
			call         string
			names, types []string
			row          []any
			scalar       string
		}{
			{"pg_walfile_name_offset('0/1000000'::pg_lsn)", []string{"file_name", "file_offset"}, []string{"TEXT", "INT4"}, []any{"000000010000000000000001", int64(0)}, "(000000010000000000000001,0)"},
			{"pg_split_walfile_name('000000010000000100000000')", []string{"segment_number", "timeline_id"}, []string{"NUMERIC", "INT8"}, []any{"256", int64(1)}, "(256,1)"},
		} {
			for _, suffix := range []string{"", " LIMIT 0"} {
				want := [][]any{tc.row}
				if suffix != "" {
					want = nil
				}
				for _, q := range []string{"SELECT (" + tc.call + ").*", "SELECT * FROM " + tc.call} {
					walNamesRoutes(t, p, b, walNamesSelect(b.PG, q+suffix, tc.names, tc.types, want))
				}
				walNamesRoutes(t, p, b, walNamesSelect(b.PG, "SELECT * FROM "+tc.call+" AS w(a,b)"+suffix, []string{"a", "b"}, tc.types, want))
				scalar := [][]any{{tc.scalar}}
				if suffix != "" {
					scalar = nil
				}
				walNamesRoutes(t, p, b, walNamesSelect(b.PG, "SELECT "+tc.call+" AS r"+suffix, []string{"r"}, []string{"RECORD"}, scalar))
				for i, label := range tc.names {
					var row [][]any
					if suffix == "" {
						row = [][]any{{tc.row[i]}}
					}
					walNamesRoutes(t, p, b, walNamesSelect(b.PG, "SELECT ("+tc.call+")."+label+" AS v"+suffix, []string{"v"}, tc.types[i:i+1], row))
				}
			}
		}
		for _, tc := range []struct {
			call         string
			names, types []string
		}{
			{"pg_walfile_name_offset(NULL::pg_lsn)", []string{"file_name", "file_offset"}, []string{"TEXT", "INT4"}},
			{"pg_split_walfile_name(NULL::text)", []string{"segment_number", "timeline_id"}, []string{"NUMERIC", "INT8"}},
		} {
			walNamesRoutes(t, p, b, walNamesSelect(b.PG, "SELECT "+tc.call+" AS r", []string{"r"}, []string{"RECORD"}, [][]any{{nil}}))
			for _, q := range []string{"SELECT (" + tc.call + ").*", "SELECT * FROM " + tc.call} {
				walNamesRoutes(t, p, b, walNamesSelect(b.PG, q, tc.names, tc.types, [][]any{{nil, nil}}))
			}
		}
		walNamesRoutes(t, p, b, walNamesSelect(b.PG, "SELECT pg_walfile_name(NULL::pg_lsn) AS n", []string{"n"}, []string{"TEXT"}, [][]any{{nil}}))
	})
}

func TestReferenceWALNamesParameters(t *testing.T) {
	walNamesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct {
			q            string
			names, types []string
			input        string
			row          []any
		}{
			{"SELECT pg_walfile_name($1) AS n", []string{"n"}, []string{"TEXT"}, "FFFFFFFF/FFFFFFFF", []any{"00000001FFFFFFFF000000FF"}},
			{"SELECT pg_walfile_name_offset($1) AS r", []string{"r"}, []string{"RECORD"}, "0/1000000", []any{"(000000010000000000000001,0)"}},
			{"SELECT * FROM pg_walfile_name_offset($1)", []string{"file_name", "file_offset"}, []string{"TEXT", "INT4"}, "0/1000000", []any{"000000010000000000000001", int64(0)}},
			{"SELECT pg_split_walfile_name($1) AS r", []string{"r"}, []string{"RECORD"}, "ffffffFF00000001000000af", []any{"(431,4294967295)"}},
			{"SELECT * FROM pg_split_walfile_name($1)", []string{"segment_number", "timeline_id"}, []string{"NUMERIC", "INT8"}, "ffffffFF00000001000000af", []any{"431", int64(4294967295)}},
		} {
			stmt, err := b.DB.PrepareContext(p.Ctx, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: tc.q}
			for _, arg := range []any{tc.input, []byte(tc.input), nil, []byte(nil)} {
				row := append([]any(nil), tc.row...)
				if arg == nil || reflect.DeepEqual(arg, []byte(nil)) {
					for i := range row {
						row[i] = nil
					}
				}
				s := walNamesSelect(b.PG, tc.q, tc.names, tc.types, [][]any{row}, arg)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
			for _, invalid := range []struct {
				input string
				arg   any
			}{
				{"bad", "bad"}, {"bad", []byte("bad")}, {"", ""}, {"", []byte{}},
			} {
				d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type pg_lsn: "`+invalid.input+`"`)
				d.Context = "unnamed portal parameter $1 = '...'"
				if invalid.input == "" {
					d.Context = "unnamed portal parameter $1 = ''"
				}
				if strings.Contains(tc.q, "split") {
					d = walNamesInvalid(invalid.input)
				}
				s := fixture.ErrorDiagnostic(tc.q, d, invalid.arg)
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, walNamesSelect(b.PG, tc.q, tc.names, tc.types, [][]any{tc.row}, tc.input))
			}
			stmt.Close()
		}
		q := "SELECT pg_walfile_name($1::pg_lsn) AS n"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		input := []byte("0/1000000")
		rows, err := stmt.QueryContext(p.Ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		for i := range input {
			input[i] = 'X'
		}
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		var raw any
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		fixture.AssertRawValue(t, b.PG, "TEXT", raw)
		if raw != "000000010000000000000001" {
			t.Fatal("borrowed parameter escaped", raw)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		rows.Close()
		walNamesRoutes(t, p, b, walNamesSelect(b.PG, "SELECT * FROM pg_split_walfile_name($1::text) LIMIT 0", []string{"segment_number", "timeline_id"}, []string{"NUMERIC", "INT8"}, nil, "000000010000000000000001"))
	})
}
