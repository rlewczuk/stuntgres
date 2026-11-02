package reference_test

import (
	"context"
	"database/sql"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Released scalar declarations; these are independent of the native recipe.
var scalarCases = []networkCase{
	{"int2", "-32768", "0", "32767", "2", "btint2cmp", 21, 21, 350, []int64{95, 522, 94, 524, 520}},
	{"int4", "-2147483648", "0", "2147483647", "2", "btint4cmp", 23, 23, 351, []int64{97, 523, 96, 525, 521}},
	{"int8", "-9223372036854775808", "0", "9223372036854775807", "2", "btint8cmp", 20, 20, 842, []int64{412, 414, 410, 415, 413}},
	{"float4", "-Infinity", "0", "NaN", "2", "btfloat4cmp", 700, 700, 354, []int64{622, 624, 620, 625, 623}},
	{"float8", "-Infinity", "0", "NaN", "2", "btfloat8cmp", 701, 701, 355, []int64{672, 673, 670, 675, 674}},
	{"bool", "false", "false", "true", "", "btboolcmp", 16, 16, 1693, []int64{58, 1694, 91, 1695, 59}},
}

const scalarClasses = "('int2_ops','int4_ops','int8_ops','float4_ops','float8_ops','bool_ops')"
const scalarInputs = "(21,23,20,700,701,16)"

func scalarTexts(tc networkCase) []string {
	if tc.typ == "bool" {
		return []string{"false", "false", "true", ""}
	}
	if strings.HasPrefix(tc.typ, "float") {
		sub := "5e-324"
		if tc.typ == "float4" {
			sub = "1e-45"
		}
		return []string{"-Infinity", "-1", "-0", "0", sub, "1", "Infinity", "NaN", "NaN", ""}
	}
	return []string{tc.lower, "-1", "0", "0", "1", tc.upper, ""}
}
func scalarTruth(tc networkCase) [][]int64 {
	if tc.typ == "bool" {
		return [][]int64{nil, {1, 2}, {1, 2}, {1, 2, 3}, {3}, {3}}
	}
	if strings.HasPrefix(tc.typ, "float") {
		return [][]int64{{1, 2}, {1, 2, 3, 4}, {3, 4}, {3, 4, 5, 6, 7, 8, 9}, {5, 6, 7, 8, 9}, {1, 2, 5, 6, 7, 8, 9}}
	}
	return [][]int64{{1, 2}, {1, 2, 3, 4}, {3, 4}, {3, 4, 5, 6}, {5, 6}, {1, 2, 5, 6}}
}
func scalarSeed(run func(fixture.Step), table string, tc networkCase) {
	var rows []string
	for i, text := range scalarTexts(tc) {
		value := "NULL"
		if text != "" {
			value = networkLiteral(text, tc.typ)
		}
		rows = append(rows, "("+strconv.Itoa(i+1)+","+value+")")
	}
	run(fixture.Exec("INSERT INTO "+table+" VALUES"+strings.Join(rows, ","), int64(len(rows))))
}
func scalarRows(b fixture.Backend, tc networkCase) [][]any {
	var rows [][]any
	for i, text := range scalarTexts(tc) {
		var value any
		if text != "" {
			switch {
			case tc.typ == "bool":
				value = text == "true"
			case strings.HasPrefix(tc.typ, "float"):
				f, err := strconv.ParseFloat(text, 64)
				if err != nil {
					panic(err)
				}
				if tc.typ == "float4" && !b.PG {
					f = float64(float32(f))
				}
				value = f
			default:
				n, err := strconv.ParseInt(text, 10, 64)
				if err != nil {
					panic(err)
				}
				value = n
			}
		}
		rows = append(rows, []any{int64(i + 1), value})
	}
	return rows
}
func scalarValueStep(b fixture.Backend, query string, tc networkCase, values [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, []string{"id", "v"}, []string{"INT4", strings.ToUpper(tc.typ)}, values, args...)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if column == 0 {
			return false
		}
		want := values[row][1]
		if f, ok := want.(float64); ok {
			got, ok := value.(float64)
			if !ok || !(math.IsNaN(f) && math.IsNaN(got)) && math.Float64bits(got) != math.Float64bits(f) {
				t.Fatalf("%s row %d raw=%T %v want bits %x", tc.typ, row, value, value, math.Float64bits(f))
			}
		} else if !reflect.DeepEqual(value, want) {
			t.Fatalf("%s row %d raw=%T %v want %T %v", tc.typ, row, value, value, want, want)
		}
		return true
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		cols, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		fixture.ScalarAssertDescription(t, cols, s.Columns, s.Types)
		fixture.ScalarAssertMetadata(t, b.PG, cols)
		for i, c := range cols {
			scan := reflect.TypeFor[int64]()
			if i == 1 {
				switch tc.typ {
				case "bool":
					scan = reflect.TypeFor[bool]()
				case "float4", "float8":
					scan = reflect.TypeFor[float64]()
				}
			}
			if b.PG {
				if i == 0 || tc.typ == "int4" {
					scan = reflect.TypeFor[int32]()
				}
				if i == 1 && tc.typ == "int2" {
					scan = reflect.TypeFor[int16]()
				}
				if i == 1 && tc.typ == "float4" {
					scan = reflect.TypeFor[float32]()
				}
			}
			if c.ScanType() != scan {
				t.Fatalf("scan %d=%v want %v", i, c.ScanType(), scan)
			}
			nullable, known := c.Nullable()
			if known != !b.PG || nullable != (!b.PG && i == 1) {
				t.Fatalf("nullable %d=%t/%t", i, nullable, known)
			}
			if n, ok := c.Length(); n != 0 || ok {
				t.Fatalf("length %d/%t", n, ok)
			}
			if p, s, ok := c.DecimalSize(); p != 0 || s != 0 || ok {
				t.Fatalf("decimal %d,%d/%t", p, s, ok)
			}
		}
	}
	return s
}

// Float observations use the numeric fixtures' raw-value lane: NaN is checked
// with IsNaN, while finite values and signed zeros retain exact carrier bits.
// The common fixture's row equality deliberately remains unchanged.
func scalarConsumeValues(t *testing.T, b fixture.Backend, rows *sql.Rows, s fixture.Step) {
	t.Helper()
	defer rows.Close()
	s.CheckMetadata(t, rows)
	count := 0
	for rows.Next() {
		if count >= len(s.Rows) {
			t.Fatal("unexpected scalar value row", count)
		}
		var id, value any
		if err := rows.Scan(&id, &value); err != nil {
			t.Fatal(err)
		}
		fixture.AssertRawValue(t, b.PG, "INT4", id)
		if !reflect.DeepEqual(fixture.Normalize(id), s.Rows[count][0]) {
			t.Fatalf("scalar row %d id=%v want %v", count, id, s.Rows[count][0])
		}
		s.CheckRaw(t, 1, count, value)
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != len(s.Rows) {
		t.Fatalf("scalar value rows=%d want %d", count, len(s.Rows))
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func scalarValueRun(t *testing.T, p *fixture.Pair, b fixture.Backend, executor fixture.Executor, s fixture.Step) {
	t.Helper()
	if s.Types[1] != "FLOAT4" && s.Types[1] != "FLOAT8" {
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
		return
	}
	rows, err := executor.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), s.Query, s.Args...)
	if err != nil {
		t.Fatal(err)
	}
	scalarConsumeValues(t, b, rows, s)
	b.Recorder.Mu.Lock()
	var captured *fixture.Operation
	for _, op := range b.Recorder.Operations {
		if op.SQL == s.Query && op.Columns != nil {
			captured = op
		}
	}
	b.Recorder.Mu.Unlock()
	if captured == nil || !captured.Closed || captured.CloseErr != nil || len(captured.Rows) != len(s.Rows) {
		t.Fatal("missing closed scalar float observation")
	}
	for row, values := range captured.Rows {
		fixture.AssertRawValue(t, b.PG, "INT4", values[0])
		if !reflect.DeepEqual(fixture.Normalize(values[0]), s.Rows[row][0]) {
			t.Fatal("recorded scalar row identity", row, values)
		}
		s.CheckRaw(t, 1, row, values[1])
	}
}

func scalarValueRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, s fixture.Step) {
	t.Helper()
	scalarValueRun(t, p, b, conn, s)
	stmt, err := conn.PrepareContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), s.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	scalarValueRun(t, p, b, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: s.Query}, s)
}
func scalarGraph(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, schema string, presentation ...map[string]string) {
	ginInstalledGraph(t, p, b, conn, schema, scalarCases, scalarInputs, presentation...)
}
func scalarControlSeed(run func(fixture.Step)) {
	run(fixture.Exec("CREATE EXTENSION btree_gin VERSION '1.3'", 0))
	var columns, keys []string
	columns = append(columns, "id integer NOT NULL")
	for _, tc := range scalarCases {
		columns = append(columns, tc.typ+" "+tc.typ)
		keys = append(keys, tc.typ+" public."+tc.typ+"_ops")
		run(fixture.Exec("CREATE TABLE scalar_saved_"+tc.typ+"(id integer NOT NULL,v "+tc.typ+")", 0))
		scalarSeed(run, "scalar_saved_"+tc.typ, tc)
		run(fixture.Exec("CREATE INDEX scalar_saved_"+tc.typ+"_i ON scalar_saved_"+tc.typ+" USING gin(v public."+tc.typ+"_ops)", 0))
	}
	run(fixture.Exec("CREATE TABLE scalar_saved("+strings.Join(columns, ",")+")", 0))
	run(fixture.Exec("CREATE INDEX scalar_saved_i ON scalar_saved USING gin("+strings.Join(keys, ",")+")", 0))
	run(fixture.Exec("INSERT INTO scalar_saved VALUES(1,0,0,9007199254740993,'-0','-0',false),(2,0,0,9007199254740994,'Infinity','Infinity',true),(3,1,1,9223372036854775807,'NaN','NaN',true),(4,1,1,-9223372036854775808,'NaN','NaN',false),(5,NULL,NULL,NULL,NULL,NULL,NULL)", 5))
	run(fixture.Exec("CREATE TABLE scalar_saved_edges(id integer NOT NULL,r float4,d float8)", 0))
	run(fixture.Exec("CREATE INDEX scalar_saved_edges_i ON scalar_saved_edges USING gin(r public.float4_ops,d public.float8_ops)", 0))
	run(fixture.Exec("INSERT INTO scalar_saved_edges VALUES(1,'0.1','1'),(2,'7.038531e-26','1.0000000000000002'),(3,'16777217','2.2250738585072014e-308'),(4,'16777218','1.7976931348623157e308'),(5,'1.17549435e-38','-5e-324'),(6,'3.4028235e38','5e-324'),(7,'-1e-45','-0')", 7))
}
func scalarControlCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, executor fixture.Executor, run func(fixture.Step)) {
	t.Helper()
	for _, tc := range scalarCases {
		scalarValueRun(t, p, b, executor, scalarValueStep(b, "SELECT id,v FROM scalar_saved_"+tc.typ+" ORDER BY id", tc, scalarRows(b, tc)))
		for i, op := range []string{"<", "<=", "=", ">=", ">", "<>"} {
			run(ginIDs("SELECT id FROM scalar_saved_"+tc.typ+" WHERE v"+op+"$1::"+tc.typ+" ORDER BY id", scalarTruth(tc)[i], tc.boundary))
		}
	}
	run(ginIDs("SELECT id FROM scalar_saved WHERE int8=9007199254740993 ORDER BY id", []int64{1}))
	run(ginIDs("SELECT id FROM scalar_saved WHERE float8='NaN'::float8 ORDER BY id", []int64{3, 4}))
	run(ginIDs("SELECT id FROM scalar_saved WHERE bool IS NULL ORDER BY id", []int64{5}))
	r := []float64{float64(float32(.1)), float64(math.Float32frombits(0x15ae43fd)), 16777216, 16777218, float64(math.Float32frombits(0x00800000)), float64(math.MaxFloat32), -float64(math.SmallestNonzeroFloat32)}
	if b.PG {
		r = []float64{.1, 7.038531e-26, 16777216, 16777218, 1.1754944e-38, 3.4028235e38, -1e-45}
	}
	d := []float64{1, math.Float64frombits(0x3ff0000000000001), math.Float64frombits(0x0010000000000000), math.MaxFloat64, -math.SmallestNonzeroFloat64, math.SmallestNonzeroFloat64, math.Copysign(0, -1)}
	var rows [][]any
	for i := range r {
		rows = append(rows, []any{r[i], d[i]})
	}
	run(fixture.Select("SELECT r,d FROM scalar_saved_edges ORDER BY id", []string{"r", "d"}, []string{"FLOAT4", "FLOAT8"}, rows))
	run(ginIDs("SELECT id FROM scalar_saved_edges WHERE r=$1::float4 ORDER BY id", []int64{3}, "16777217"))
	run(ginIDs("SELECT id FROM scalar_saved_edges WHERE d>$1::float8 AND d<$2::float8 ORDER BY id", []int64{2, 4}, "1", "Infinity"))
}
