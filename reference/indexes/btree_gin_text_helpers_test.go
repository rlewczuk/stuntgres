package reference_test

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 extension scripts and builtin catalogs, not engine recipe output.
var textCases = []networkCase{
	{"text", "", "ab", "b", "missing", "bttextcmp", 25, 25, 360, []int64{664, 665, 98, 667, 666}},
	{"varchar", "", "ab", "b", "missing", "bttextcmp", 1043, 25, 360, []int64{664, 665, 98, 667, 666}},
	{"bpchar", "", "ab", "b", "missing", "bpcharcmp", 1042, 1042, 1078, []int64{1058, 1059, 1054, 1061, 1060}},
	{"char", "", "b", `\377`, "z", "btcharcmp", 18, 18, 358, []int64{631, 632, 92, 634, 633}},
	{"name", "", "ab", "b", "missing", "btnamecmp", 19, 19, 359, []int64{660, 661, 93, 663, 662}},
	{"bytea", `\x`, `\x80`, `\xff`, `\x81`, "byteacmp", 17, 17, 1954, []int64{1957, 1958, 1955, 1960, 1959}},
}

const textInputs = "(25,1043,1042,18,19,17)"
const textClasses = "('text_ops','varchar_ops','bpchar_ops','char_ops','name_ops','bytea_ops')"

func textSQLType(tc networkCase) string {
	if tc.typ == "char" {
		return `"char"`
	}
	return tc.typ
}

func textArgument(tc networkCase, value string) any {
	if tc.typ != "bytea" {
		return value
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(value, `\x`))
	if err != nil {
		panic(err)
	}
	return raw
}

func textTexts(tc networkCase) []string {
	switch tc.typ {
	case "bpchar":
		return []string{"", "a", "ab", "ab ", "ab  ", "b"}
	case "char":
		return []string{"", "a", "b", "b", `\177`, `\200`, `\377`}
	case "bytea":
		return []string{`\x`, `\x00`, `\x0000`, `\x7f`, `\x80`, `\xff`, `\xff`}
	default:
		return []string{"", "a", "ab", "ab", "ab ", "b"}
	}
}

func textTruth(tc networkCase) [][]int64 {
	switch tc.typ {
	case "bpchar":
		return [][]int64{{1, 2}, {1, 2, 3, 4, 5}, {3, 4, 5}, {3, 4, 5, 6}, {6}, {1, 2, 6}}
	case "char":
		return [][]int64{{1, 2}, {1, 2, 3, 4}, {3, 4}, {3, 4, 5, 6, 7}, {5, 6, 7}, {1, 2, 5, 6, 7}}
	case "bytea":
		return [][]int64{{1, 2, 3, 4}, {1, 2, 3, 4, 5}, {5}, {5, 6, 7}, {6, 7}, {1, 2, 3, 4, 6, 7}}
	default:
		return [][]int64{{1, 2}, {1, 2, 3, 4}, {3, 4}, {3, 4, 5, 6}, {5, 6}, {1, 2, 5, 6}}
	}
}

func textSeed(run func(fixture.Step), table string, tc networkCase) {
	var rows []string
	for i, value := range textTexts(tc) {
		rows = append(rows, "("+strconv.Itoa(i+1)+","+networkLiteral(value, textSQLType(tc))+")")
	}
	rows = append(rows, "("+strconv.Itoa(len(rows)+1)+",NULL)")
	run(fixture.Exec("INSERT INTO "+table+" VALUES"+strings.Join(rows, ","), int64(len(rows))))
}

func textRows(tc networkCase) [][]any {
	var values []any
	switch tc.typ {
	case "char":
		values = []any{"", "a", "b", "b", string([]byte{127}), `\200`, `\377`}
	case "bytea":
		values = []any{[]byte{}, []byte{0}, []byte{0, 0}, []byte{127}, []byte{128}, []byte{255}, []byte{255}}
	default:
		for _, v := range textTexts(tc) {
			values = append(values, v)
		}
	}
	var rows [][]any
	for i, v := range values {
		rows = append(rows, []any{int64(i + 1), v})
	}
	return append(rows, []any{int64(len(rows) + 1), nil})
}

func textValueStep(b fixture.Backend, query string, tc networkCase, values [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, []string{"id", "v"}, []string{"INT4", strings.ToUpper(tc.typ)}, values, args...)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if column == 0 {
			return false
		}
		want := values[row][1]
		// The bytea branch below owns its complete carrier contract: pq's
		// escape-empty output can be typed-nil []byte, never nil interface.
		if tc.typ != "bytea" {
			fixture.AssertRawValue(t, b.PG, s.Types[1], value)
		}
		if want == nil {
			if value != nil {
				t.Fatalf("NULL carrier=%T %v", value, value)
			}
			return true
		}
		if tc.typ == "bytea" {
			got, ok := value.([]byte)
			if !ok || !bytes.Equal(got, want.([]byte)) || !b.PG && got == nil {
				t.Fatalf("bytea carrier=%T %v want=%v", value, value, want)
			}
		} else if b.PG && tc.typ == "name" {
			got, ok := value.([]byte)
			if !ok || string(got) != want {
				t.Fatalf("name carrier=%T %v want=%v", value, value, want)
			}
		} else if got, ok := value.(string); !ok || got != want {
			t.Fatalf("%s carrier=%T %v want=%v", tc.typ, value, value, want)
		}
		return true
	}
	if tc.typ == "bytea" {
		s.Rows = make([][]any, len(values))
		for i, row := range values {
			s.Rows[i] = []any{row[0], fixture.Normalize(row[1])}
		}
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		cols, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		fixture.ScalarAssertDescription(t, cols, s.Columns, s.Types)
		for i, c := range cols {
			scan := reflect.TypeFor[string]()
			if i == 0 {
				scan = reflect.TypeFor[int64]()
				if b.PG {
					scan = reflect.TypeFor[int32]()
				}
			} else if tc.typ == "bytea" {
				scan = reflect.TypeFor[[]byte]()
			} else if b.PG && tc.typ != "text" && tc.typ != "varchar" {
				scan = reflect.TypeFor[any]()
			}
			if c.ScanType() != scan {
				t.Fatalf("%s scan=%v want=%v", c.Name(), c.ScanType(), scan)
			}
			n, known := c.Nullable()
			if known != !b.PG || n != (!b.PG && i == 1) {
				t.Fatalf("%s nullable=%t/%t", c.Name(), n, known)
			}
			var length int64
			var hasLength bool
			if i == 1 {
				switch tc.typ {
				case "text", "varchar", "bpchar":
					hasLength = true
					length = math.MaxInt64
					if b.PG && tc.typ != "text" {
						length = -5
					}
				case "bytea":
					if b.PG {
						hasLength = true
						length = math.MaxInt64
					}
				}
			}
			if got, ok := c.Length(); got != length || ok != hasLength {
				t.Fatalf("%s length=%d/%t want=%d/%t", c.Name(), got, ok, length, hasLength)
			}
			if p, s, ok := c.DecimalSize(); p != 0 || s != 0 || ok {
				t.Fatalf("decimal=%d,%d/%t", p, s, ok)
			}
		}
	}
	return s
}

func textGraph(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, schema string, presentation ...map[string]string) {
	t.Helper()
	ginInstalledGraph(t, p, b, conn, schema, textCases, textInputs, presentation...)
	classRoutes(t, p, b, conn, amCount("SELECT count(*) FROM pg_proc WHERE pronamespace=$1::regnamespace AND proname IN('gin_extract_value_varchar','gin_extract_query_varchar','gin_compare_prefix_varchar')", 0, schema))
	var wants [][]any
	for _, slot := range []int64{2, 3, 5} {
		wants = append(wants, []any{slot, true})
	}
	classRoutes(t, p, b, conn, fixture.Select("SELECT a.amprocnum,a.amproc=b.amproc AS shared FROM pg_amproc a JOIN pg_opclass t ON t.opcfamily=a.amprocfamily JOIN pg_opclass v ON v.opcmethod=t.opcmethod AND v.opcnamespace=t.opcnamespace AND v.opcintype=1043 JOIN pg_amproc b ON b.amprocfamily=v.opcfamily AND b.amprocnum=a.amprocnum WHERE t.opcmethod=2742 AND t.opcnamespace=$1::regnamespace AND t.opcintype=25 AND a.amprocnum IN(2,3,5) ORDER BY a.amprocnum", []string{"amprocnum", "shared"}, []string{"INT2", "BOOL"}, wants, schema))
}

func textControlSeed(run func(fixture.Step)) {
	run(fixture.Exec("CREATE EXTENSION btree_gin VERSION '1.3'", 0))
	for _, tc := range textCases {
		table := "text_saved_" + tc.typ
		run(fixture.Exec("CREATE TABLE "+table+"(id integer NOT NULL,v "+textSQLType(tc)+")", 0))
		textSeed(run, table, tc)
		run(fixture.Exec("CREATE INDEX "+table+"_i ON "+table+" USING gin(v public."+tc.typ+"_ops)", 0))
	}
	run(fixture.Exec(`CREATE TABLE text_saved_width(id integer NOT NULL,v bpchar(4))`, 0))
	run(fixture.Exec(`INSERT INTO text_saved_width VALUES(1,''),(2,'a'),(3,'ab '),(4,NULL)`, 4))
	run(fixture.Exec(`CREATE INDEX text_saved_width_i ON text_saved_width USING gin(v)`, 0))
	run(fixture.Exec(`ALTER INDEX text_saved_text_i SET(fastupdate=off,gin_pending_list_limit=64)`, 0))
}

func textControlCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, executor fixture.Executor, run func(fixture.Step)) {
	t.Helper()
	for _, tc := range textCases {
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, textValueStep(b, "SELECT id,v FROM text_saved_"+tc.typ+" ORDER BY id", tc, textRows(tc)))
		for i, op := range []string{"<", "<=", "=", ">=", ">", "<>"} {
			run(ginIDs("SELECT id FROM text_saved_"+tc.typ+" WHERE v"+op+"$1::"+textSQLType(tc)+" ORDER BY id", textTruth(tc)[i], textArgument(tc, tc.boundary)))
		}
	}
	run(fixture.Select("SELECT id,v FROM text_saved_width ORDER BY id", []string{"id", "v"}, []string{"INT4", "BPCHAR"}, [][]any{{int64(1), "    "}, {int64(2), "a   "}, {int64(3), "ab  "}, {int64(4), nil}}))
	run(fixture.Select("SELECT reloptions::text AS options FROM pg_class WHERE oid='text_saved_text_i'::regclass", []string{"options"}, []string{"TEXT"}, [][]any{{"{fastupdate=off,gin_pending_list_limit=64}"}}))
}
