package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 installation scripts and pg_operator.dat, not native observations.
type networkCase struct {
	typ, lower, boundary, upper, absent, comparator string
	input, search, comparatorOID                    int64
	operators                                       []int64
}

var networkCases = []networkCase{
	{"inet", "0.0.0.0/0", "10.0.0.0/24", "::/0", "10.0.1.0/24", "network_cmp", 869, 869, 926, []int64{1203, 1204, 1201, 1206, 1205}},
	{"cidr", "0.0.0.0/0", "10.0.0.0/24", "::/0", "10.0.1.0/24", "network_cmp", 650, 869, 926, []int64{1203, 1204, 1201, 1206, 1205}},
	{"macaddr", "00:00:00:00:00:00", "80:00:00:00:00:00", "ff:ff:ff:ff:ff:ff", "80:00:00:00:00:01", "macaddr_cmp", 829, 829, 836, []int64{1222, 1223, 1220, 1225, 1224}},
	{"macaddr8", "00:00:00:00:00:00:00:00", "80:00:00:00:00:00:00:00", "ff:ff:ff:ff:ff:ff:ff:ff", "80:00:00:00:00:00:00:01", "macaddr8_cmp", 774, 774, 4119, []int64{3364, 3365, 3362, 3367, 3366}},
	{"uuid", "00000000-0000-0000-0000-000000000000", "80000000-0000-0000-0000-000000000000", "ffffffff-ffff-ffff-ffff-ffffffffffff", "80000000-0000-0000-0000-000000000001", "uuid_cmp", 2950, 2950, 2960, []int64{2974, 2976, 2972, 2977, 2975}},
	{"oid", "0", "2147483648", "4294967295", "2147483649", "btoidcmp", 26, 26, 356, []int64{609, 611, 607, 612, 610}},
}

const networkClasses = "('inet_ops','cidr_ops','macaddr_ops','macaddr8_ops','uuid_ops','oid_ops')"

var networkTruth = [][]int64{{1}, {1, 2, 3}, {2, 3}, {2, 3, 4}, {4}, {1, 4}}

func networkLiteral(text, typ string) string { return "'" + text + "'::" + typ }
func networkSeed(run func(fixture.Step), table string, tc networkCase) {
	run(fixture.Exec("INSERT INTO "+table+" VALUES(1,"+networkLiteral(tc.lower, tc.typ)+"),(2,"+networkLiteral(tc.boundary, tc.typ)+"),(3,"+networkLiteral(tc.boundary, tc.typ)+"),(4,"+networkLiteral(tc.upper, tc.typ)+"),(5,NULL)", 5))
}
func networkValues(tc networkCase) [][]any {
	values := []any{tc.lower, tc.boundary, tc.boundary, tc.upper, nil}
	if tc.typ == "oid" {
		values = []any{int64(0), int64(2147483648), int64(2147483648), int64(4294967295), nil}
	}
	var rows [][]any
	for i, v := range values {
		rows = append(rows, []any{int64(i + 1), v})
	}
	return rows
}

func networkValueStep(b fixture.Backend, query string, tc networkCase, values [][]any) fixture.Step {
	typ := strings.ToUpper(tc.typ)
	if b.PG && tc.typ == "macaddr8" {
		typ = ""
	}
	expected := values
	if b.PG && tc.typ == "oid" {
		expected = nil
		for _, row := range values {
			v := row[1]
			if v != nil {
				v = fmt.Sprint(v)
			}
			expected = append(expected, []any{row[0], v})
		}
	}
	s := fixture.Select(query, []string{"id", "v"}, []string{"INT4", typ}, expected)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if column == 0 {
			return false
		}
		want := values[row][1]
		if want == nil {
			if value != nil {
				t.Fatalf("NULL carrier %T", value)
			}
			return true
		}
		if b.PG {
			raw, ok := value.([]byte)
			text := tc.lower
			switch row {
			case 1, 2:
				text = tc.boundary
			case 3:
				text = tc.upper
			}
			if !ok || raw == nil || string(raw) != text {
				t.Fatalf("%s carrier %T %v want %q", tc.typ, value, value, text)
			}
		} else if tc.typ == "oid" {
			if raw, ok := value.(int64); !ok || raw != want {
				t.Fatalf("OID carrier %T %v", value, value)
			}
		} else if raw, ok := value.(string); !ok || raw != want {
			t.Fatalf("%s carrier %T %v", tc.typ, value, value)
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
			scan := reflect.TypeFor[string]()
			if i == 0 || tc.typ == "oid" {
				scan = reflect.TypeFor[int64]()
			}
			if b.PG {
				scan = reflect.TypeFor[any]()
				if i == 0 {
					scan = reflect.TypeFor[int32]()
				}
			}
			if c.ScanType() != scan {
				t.Fatalf("%s column %d scan=%v want %v", tc.typ, i, c.ScanType(), scan)
			}
			nullable, known := c.Nullable()
			// id is declared NOT NULL; v is nullable. lib/pq exposes neither.
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

func networkControlSeed(run func(fixture.Step)) {
	compoundControlSeed(run)
	var keys []string
	for _, tc := range networkCases {
		keys = append(keys, tc.typ+" "+tc.typ+"_ops")
	}
	run(fixture.Exec("ALTER TABLE compound_saved ADD COLUMN inet inet", 0))
	for _, tc := range networkCases[1:] {
		run(fixture.Exec("ALTER TABLE compound_saved ADD COLUMN "+tc.typ+" "+tc.typ, 0))
	}
	run(fixture.Exec("CREATE INDEX network_saved_i ON compound_saved USING gin("+strings.Join(keys, ",")+",b bit_ops,v varbit_ops,e enum_ops,m money_ops,a,t)", 0))
	run(fixture.Exec("ALTER INDEX network_saved_i SET(fastupdate=off,gin_pending_list_limit=64)", 0))
	for _, tc := range networkCases {
		run(fixture.Exec("CREATE INDEX network_saved_"+tc.typ+" ON compound_saved USING gin("+tc.typ+")", 0))
		run(fixture.Exec("UPDATE compound_saved SET "+tc.typ+"="+networkLiteral(tc.boundary, tc.typ)+" WHERE id IN(1,4)", 2))
		run(fixture.Exec("UPDATE compound_saved SET "+tc.typ+"="+networkLiteral(tc.lower, tc.typ)+" WHERE id=2", 1))
	}
}
func networkControlCheck(b fixture.Backend, run func(fixture.Step)) {
	// Five incumbent compound indexes plus the mixed network and six defaults.
	compoundControlCheck(b, run, 12)
	for _, tc := range networkCases {
		run(ginIDs("SELECT id FROM compound_saved WHERE "+tc.typ+"="+networkLiteral(tc.boundary, tc.typ)+" ORDER BY id", []int64{1, 4}))
		run(ginIDs("SELECT id FROM compound_saved WHERE "+tc.typ+"<"+networkLiteral(tc.boundary, tc.typ)+" ORDER BY id", []int64{2}))
		run(ginIDs("SELECT id FROM compound_saved WHERE "+tc.typ+" IS NULL ORDER BY id", []int64{3}))
	}
	run(fixture.Select("SELECT reloptions::text AS options FROM pg_class WHERE oid='network_saved_i'::regclass", []string{"options"}, []string{"TEXT"}, [][]any{{"{fastupdate=off,gin_pending_list_limit=64}"}}))
}
