package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

type statisticsViewsScenario struct {
	ndistinctScenario
	backend fixture.Backend
}

func withStatisticsViews(t *testing.T, body func(*testing.T, statisticsViewsScenario), roles ...string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		route := "direct"
		if prepared {
			route = "prepared"
		}
		t.Run(route, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, roles)
			fixture.SchemaReferenceCleanup(t, "sv_defs")
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					conn, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					defer conn.ExecContext(context.Background(), "RESET ROLE")
					run := func(step fixture.Step) {
						if prepared {
							step = fixture.Prepared(step)
						}
						fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
					}
					body(t, statisticsViewsScenario{ndistinctScenario{run, backend.PG, conn, p, prepared}, backend})
				})
			}
		})
	}
}

func (s statisticsViewsScenario) exec(queries ...string) {
	for _, query := range queries {
		s.run(fixture.Exec(query, 0))
	}
}

func (s statisticsViewsScenario) query(t *testing.T, query string, args ...any) (*sql.Rows, func()) {
	t.Helper()
	var rows *sql.Rows
	var stmt *sql.Stmt
	var err error
	if s.prepared {
		stmt, err = s.conn.PrepareContext(s.pair.Ctx, query)
		if err != nil {
			t.Fatal("prepare", query, err)
		}
		rows, err = stmt.QueryContext(s.pair.Ctx, args...)
	} else {
		rows, err = s.conn.QueryContext(s.pair.Ctx, query, args...)
	}
	if err != nil {
		if stmt != nil {
			stmt.Close()
		}
		t.Fatal("query", query, err)
	}
	return rows, func() {
		rows.Close()
		if stmt != nil {
			stmt.Close()
		}
	}
}

func statisticsViewsCount(view, predicate string, count int64) fixture.Step {
	return fixture.Select("SELECT count(*) FROM "+view+" WHERE "+predicate, []string{"count"}, []string{"INT8"}, [][]any{{count}})
}

func statisticsViewsPopulation(s statisticsViewsScenario) {
	expressionPopulation(s.ndistinctScenario)
	s.exec("CREATE STATISTICS sv_expr ON (a+1) FROM expr_t", "CREATE STATISTICS sv_joint ON b,(a+1) FROM expr_t")
}

func statisticsViewsSkew(view, predicate, values string) fixture.Step {
	return fixture.Select("SELECT abs(null_frac-0.2)<0.000001 AS null_fraction,avg_width,n_distinct,most_common_vals::text AS vals,most_common_freqs::text AS freqs,histogram_bounds::text AS histogram FROM "+view+" WHERE "+predicate,
		[]string{"null_fraction", "avg_width", "n_distinct", "vals", "freqs", "histogram"}, []string{"BOOL", "INT4", "FLOAT4", "TEXT", "TEXT", "TEXT"}, [][]any{{true, int64(4), float64(2), values, "{0.7,0.1}", nil}})
}

func statisticsViewsUniform(name, values string) fixture.Step {
	return fixture.Select("SELECT null_frac,avg_width,n_distinct,most_common_vals::text AS vals,most_common_freqs::text AS freqs,histogram_bounds::text AS histogram FROM pg_stats_ext_exprs WHERE statistics_name=$1 AND NOT inherited",
		[]string{"null_frac", "avg_width", "n_distinct", "vals", "freqs", "histogram"}, []string{"FLOAT4", "INT4", "FLOAT4", "TEXT", "TEXT", "TEXT"}, [][]any{{float64(0), int64(4), float64(1), values, "{1}", nil}}, name)
}

func statisticsViewsMissing(s statisticsViewsScenario, table string, expressions [][]any) {
	s.run(statisticsViewsCount("pg_stats", "schemaname='public' AND tablename='"+table+"'", 0))
	s.run(statisticsViewsCount("pg_stats_ext", "schemaname='public' AND tablename='"+table+"'", 0))
	s.run(fixture.Select("SELECT statistics_name,expr,inherited,null_frac,avg_width,n_distinct,most_common_vals,most_common_freqs,histogram_bounds,correlation,most_common_elems,most_common_elem_freqs,elem_count_histogram FROM pg_stats_ext_exprs WHERE schemaname='public' AND tablename=$1 ORDER BY statistics_name,expr",
		[]string{"statistics_name", "expr", "inherited", "null_frac", "avg_width", "n_distinct", "most_common_vals", "most_common_freqs", "histogram_bounds", "correlation", "most_common_elems", "most_common_elem_freqs", "elem_count_histogram"},
		[]string{"NAME", "TEXT", "BOOL", "FLOAT4", "INT4", "FLOAT4", statisticsViewsAnyarrayType(s.pg), "_FLOAT4", statisticsViewsAnyarrayType(s.pg), "FLOAT4", statisticsViewsAnyarrayType(s.pg), "_FLOAT4", "_FLOAT4"}, expressions, table))
}

func statisticsViewsUnbuilt(name, expr string) []any {
	return []any{name, expr, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil}
}

func statisticsViewsAnyarrayType(pg bool) string {
	// The existing attribute-statistics oracle pins lib/pq's known ANYARRAY.
	return "ANYARRAY"
}

func statisticsViewsMetadata(t *testing.T, pg bool, rows *sql.Rows, names, types []string) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != len(names) {
		t.Fatalf("view column count %d, want %d", len(columns), len(names))
	}
	for i, column := range columns {
		nullable, known := column.Nullable()
		if column.Name() != names[i] || column.DatabaseTypeName() != types[i] || known == pg || !pg && !nullable {
			t.Fatalf("column %d: name=%q type=%q nullable=(%t,%t), want %q %q nullable", i+1, column.Name(), column.DatabaseTypeName(), nullable, known, names[i], types[i])
		}
	}
}

// This reader consumes the public PostgreSQL array text grammar, not engine
// representations or pg_mcv_list's physical codec. Quoted NULL is a value;
// unquoted NULL is SQL NULL. Nested braces preserve rank and item boundaries.
type statisticsViewsArray struct {
	shape  []int
	values []*string
}

func statisticsViewsReadArray(t *testing.T, text string) statisticsViewsArray {
	t.Helper()
	position := 0
	var parse func() statisticsViewsArray
	parse = func() statisticsViewsArray {
		if position >= len(text) || text[position] != '{' {
			t.Fatalf("array opener in %q at %d", text, position)
		}
		position++
		out := statisticsViewsArray{}
		count := 0
		var childShape []int
		for position < len(text) && text[position] != '}' {
			if count > 0 {
				if text[position] != ',' {
					t.Fatalf("array separator in %q", text)
				}
				position++
			}
			if position < len(text) && text[position] == '{' {
				child := parse()
				if count > 0 && !reflect.DeepEqual(childShape, child.shape) {
					t.Fatalf("ragged array %q", text)
				}
				childShape = child.shape
				out.values = append(out.values, child.values...)
			} else {
				quoted := position < len(text) && text[position] == '"'
				var value strings.Builder
				if quoted {
					position++
					closed := false
					for position < len(text) {
						c := text[position]
						position++
						if c == '"' {
							closed = true
							break
						}
						if c == '\\' {
							if position == len(text) {
								t.Fatalf("array escape %q", text)
							}
							c = text[position]
							position++
						}
						value.WriteByte(c)
					}
					if !closed {
						t.Fatalf("unterminated array value %q", text)
					}
				} else {
					for position < len(text) && text[position] != ',' && text[position] != '}' {
						value.WriteByte(text[position])
						position++
					}
				}
				v := value.String()
				if !quoted && v == "NULL" {
					out.values = append(out.values, nil)
				} else {
					out.values = append(out.values, &v)
				}
			}
			count++
		}
		if position == len(text) {
			t.Fatalf("unclosed array %q", text)
		}
		position++
		out.shape = append([]int{count}, childShape...)
		return out
	}
	array := parse()
	if position != len(text) {
		t.Fatalf("trailing array text %q", text)
	}
	return array
}

func statisticsViewsRawText(t *testing.T, pg bool, value any) string {
	t.Helper()
	if pg {
		if _, ok := value.([]byte); !ok {
			t.Fatalf("PostgreSQL array carrier %T", value)
		}
	} else if _, ok := value.(string); !ok {
		t.Fatalf("native array carrier %T", value)
	}
	text, ok := fixture.Normalize(value).(string)
	if !ok {
		t.Fatalf("array text carrier %T", value)
	}
	return text
}

type statisticsViewsMCVItem struct {
	values          []any // strings or nil, in definition dimension order
	frequency, base float64
}

func statisticsViewsMCV(t *testing.T, s statisticsViewsScenario, name string, want ...statisticsViewsMCVItem) {
	t.Helper()
	rows, closeRows := s.query(t, "SELECT most_common_vals,most_common_val_nulls,most_common_freqs,most_common_base_freqs FROM pg_stats_ext WHERE statistics_name=$1 AND NOT inherited", name)
	defer closeRows()
	statisticsViewsMetadata(t, s.pg, rows, []string{"most_common_vals", "most_common_val_nulls", "most_common_freqs", "most_common_base_freqs"}, []string{"_TEXT", "_BOOL", "_FLOAT8", "_FLOAT8"})
	if !rows.Next() {
		t.Fatal("missing collected MCV view row", rows.Err())
	}
	var raw [4]any
	if err := rows.Scan(&raw[0], &raw[1], &raw[2], &raw[3]); err != nil {
		t.Fatal(err)
	}
	var arrays [4]statisticsViewsArray
	for i, value := range raw {
		arrays[i] = statisticsViewsReadArray(t, statisticsViewsRawText(t, s.pg, value))
	}
	dimensions := len(want[0].values)
	for i := range 2 {
		if !reflect.DeepEqual(arrays[i].shape, []int{len(want), dimensions}) {
			t.Fatalf("MCV rank/extents %d: %v", i, arrays[i].shape)
		}
	}
	for i := 2; i < 4; i++ {
		if !reflect.DeepEqual(arrays[i].shape, []int{len(want)}) {
			t.Fatalf("MCV frequency rank %d: %v", i, arrays[i].shape)
		}
	}
	seen := make([]bool, len(want))
	for item := range want {
		values := make([]any, dimensions)
		for dimension := range dimensions {
			offset := item*dimensions + dimension
			value, flag := arrays[0].values[offset], arrays[1].values[offset]
			if flag == nil || *flag != "t" && *flag != "f" || (*flag == "t") != (value == nil) {
				t.Fatalf("MCV NULL flag item%d dim%d: %v %v", item, dimension, value, flag)
			}
			if value != nil {
				values[dimension] = *value
			}
		}
		matched := -1
		for i, expected := range want {
			if !seen[i] && reflect.DeepEqual(values, expected.values) {
				matched = i
				break
			}
		}
		if matched < 0 {
			t.Fatalf("unexpected/duplicate MCV item %#v", values)
		}
		seen[matched] = true
		for i, expected := range []float64{want[matched].frequency, want[matched].base} {
			text := arrays[i+2].values[item]
			if text == nil {
				t.Fatal("NULL MCV frequency")
			}
			value, err := strconv.ParseFloat(*text, 64)
			if err != nil || math.Abs(value-expected) > 1e-12 {
				t.Fatalf("MCV %#v frequency%d = %q, want %.15g: %v", values, i, *text, expected, err)
			}
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("extra MCV rows", rows.Err())
	}
}

func statisticsViewsSkewMCV(t *testing.T, s statisticsViewsScenario) {
	statisticsViewsMCV(t, s, "sv_joint", statisticsViewsMCVItem{[]any{"10", "2"}, .7, .49}, statisticsViewsMCVItem{[]any{"20", "3"}, .1, .01}, statisticsViewsMCVItem{[]any{nil, nil}, .2, .04})
}

func statisticsViewsHeld(t *testing.T, s statisticsViewsScenario, query string) *sql.Stmt {
	t.Helper()
	stmt, err := s.conn.PrepareContext(s.pair.Ctx, query)
	if err != nil {
		t.Fatal("held prepare", err)
	}
	return stmt
}

func statisticsViewsHeldCount(t *testing.T, s statisticsViewsScenario, stmt *sql.Stmt, want int64) {
	t.Helper()
	var got int64
	if err := stmt.QueryRowContext(s.pair.Ctx).Scan(&got); err != nil || got != want {
		t.Fatalf("held count %d, want %d: %v", got, want, err)
	}
}

func statisticsViewsReadHeldSkew(t *testing.T, s statisticsViewsScenario, rows *sql.Rows) {
	t.Helper()
	statisticsViewsMetadata(t, s.pg, rows, []string{"most_common_vals", "most_common_freqs"}, []string{statisticsViewsAnyarrayType(s.pg), "_FLOAT4"})
	if !rows.Next() {
		t.Fatal("old Rows lost", rows.Err())
	}
	var values, frequencies any
	if err := rows.Scan(&values, &frequencies); err != nil {
		t.Fatal(err)
	}
	if statisticsViewsRawText(t, s.pg, values) != "{2,3}" || statisticsViewsRawText(t, s.pg, frequencies) != "{0.7,0.1}" {
		t.Fatalf("old Rows replaced: %v %v", values, frequencies)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("old Rows cardinality", rows.Err())
	}
	rows.Close()
}

func statisticsViewsFailure(query, code, message string, prepared bool) fixture.Step {
	want := fixture.ControlsReferenceDiagnostic(code, message)
	want.Position = "8"
	step := fixture.ErrorDiagnostic(query, want)
	step.SelectRows, step.Prepared, step.ErrorStage = true, prepared, "execute"
	if prepared {
		step.ErrorStage = "prepare"
	}
	return step
}

func statisticsViewsIntegerHistogram(t *testing.T, text string) {
	t.Helper()
	array := statisticsViewsReadArray(t, text)
	if !reflect.DeepEqual(array.shape, []int{101}) {
		t.Fatal("histogram shape", array.shape)
	}
	last := 1
	for i, word := range array.values {
		if word == nil {
			t.Fatal("NULL histogram bound")
		}
		n, err := strconv.Atoi(*word)
		if err != nil || n <= last || i == 0 && n != 2 || i == 100 && n != 201 {
			t.Fatalf("histogram bound%d=%q after%d: %v", i, *word, last, err)
		}
		last = n
	}
}

func statisticsViewsSQLValues(count int, format string) string {
	values := make([]string, count)
	for i := range values {
		values[i] = fmt.Sprintf(format, i+1)
	}
	return strings.Join(values, ",")
}
