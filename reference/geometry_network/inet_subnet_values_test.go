package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var inetSubnetOps = []string{"<<", "<<=", ">>", ">>=", "&&"}
var inetSubnetNames = []string{"sub", "subeq", "sup", "supeq", "overlap"}
var inetSubnetTypes = []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}

func inetSubnetQuery(a, b string) string {
	parts := make([]string, 5)
	for i, op := range inetSubnetOps {
		parts[i] = a + " " + op + " " + b + " AS " + inetSubnetNames[i]
	}
	return "SELECT " + strings.Join(parts, ",")
}

func inetSubnetVector(t *testing.T, p *fixture.Pair, b fixture.Backend, a, z string, want []any) {
	t.Helper()
	s := fixture.Select(inetSubnetQuery(a, z), inetSubnetNames, inetSubnetTypes, [][]any{want})
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
}

func TestReferenceINETSubnetValues(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		for _, tc := range []struct {
			a, z string
			want []any
		}{
			{"192.168.1.5", "192.168.1.199/24", []any{true, true, false, false, true}},
			{"192.168.0.5", "192.168.1.199/24", []any{false, false, false, false, false}},
			{"192.168.1.1/24", "192.168.1.254/24", []any{false, true, false, true, true}},
			{"192.168.1.255/25", "192.168.1.129/24", []any{true, true, false, false, true}},
			{"192.168.1.255/25", "192.168.1.1/25", []any{false, false, false, false, false}},
			{"2001:db8::ffff/114", "2001:db8::8001/113", []any{true, true, false, false, true}},
			{"2001:db8::ffff/113", "2001:db8::8001/113", []any{false, true, false, true, true}},
			{"2001:db8::ffff/113", "2001:db8::1/113", []any{false, false, false, false, false}},
			{"255.255.255.255/0", "1.2.3.4/0", []any{false, true, false, true, true}},
			{"ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff/0", "::1/0", []any{false, true, false, true, true}},
			{"192.168.1.1/32", "192.168.1.1/32", []any{false, true, false, true, true}},
			{"192.168.1.1/32", "192.168.1.2/32", []any{false, false, false, false, false}},
			{"::1/128", "::1/128", []any{false, true, false, true, true}},
			{"::1/128", "::2/128", []any{false, false, false, false, false}},
			{"0.0.0.0/0", "::/0", []any{false, false, false, false, false}},
			{"1.2.3.4", "::ffff:1.2.3.4", []any{false, false, false, false, false}},
		} {
			t.Run(b.Name+"/"+tc.a+"/"+tc.z, func(t *testing.T) {
				inetSubnetVector(t, p, b, "INET '"+tc.a+"'", "INET '"+tc.z+"'", tc.want)
				reverse := []any{tc.want[2], tc.want[3], tc.want[0], tc.want[1], tc.want[4]}
				inetSubnetVector(t, p, b, "INET '"+tc.z+"'", "INET '"+tc.a+"'", reverse)
			})
		}
	}
}

func TestReferenceINETSubnetBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		// A zero address is in every zero-prefix network; flipping the first bit
		// produces a disjoint network for every nonzero prefix, including full width.
		for _, family := range []struct {
			zero, other string
			widths      []int
		}{
			{"0.0.0.0", "128.0.0.0", []int{1, 7, 8, 9, 23, 24, 25, 31, 32}},
			{"::", "8000::", []int{1, 63, 64, 65, 111, 112, 113, 114, 127, 128}},
		} {
			for _, width := range family.widths {
				a := fmt.Sprintf("INET '%s/%d'", family.zero, width)
				z := fmt.Sprintf("INET '%s/%d'", family.other, width)
				inetSubnetVector(t, p, b, a, a, []any{false, true, false, true, true})
				inetSubnetVector(t, p, b, a, z, []any{false, false, false, false, false})
				inetSubnetVector(t, p, b, a, fmt.Sprintf("INET '%s/%d'", family.zero, width-1), []any{true, true, false, false, true})
			}
		}
		inetOperatorScalar(t, p, b, "INET '192.168.1.1/24' <> INET '192.168.1.254/24'", "BOOL", true)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT INET '192.168.1.255/25' AS a,INET '2001:db8::ffff/114' AS b", []string{"a", "b"}, []string{"INET", "INET"}, [][]any{{"192.168.1.255/25", "2001:db8::ffff/114"}}))
	}
}

func TestReferenceINETSubnetResolution(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		for _, q := range []string{"CREATE DOMAIN subnet_host AS INET", "CREATE DOMAIN subnet_host_chain AS subnet_host", "CREATE DOMAIN subnet_net AS CIDR", "CREATE DOMAIN subnet_net_chain AS subnet_net"} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
		}
		for _, pair := range [][2]string{
			{"CIDR '192.168.1.128/25'", "CIDR '192.168.1/24'"},
			{"INET '192.168.1.255/25'", "CIDR '192.168.1/24'"},
			{"CIDR '192.168.1.128/25'", "INET '192.168.1.199/24'"},
			{"'192.168.1.255/25'::subnet_host_chain", "'192.168.1/24'::subnet_net_chain"},
			{"'192.168.1.255/25'", "INET '192.168.1.199/24'"},
			{"INET '192.168.1.255/25'", "'192.168.1.199/24'"},
		} {
			inetSubnetVector(t, p, b, pair[0], pair[1], []any{true, true, false, false, true})
		}
		for _, typ := range []string{"inet", "cidr", "subnet_host_chain", "subnet_net_chain"} {
			inetSubnetVector(t, p, b, "NULL::"+typ, "INET '::/0'", []any{nil, nil, nil, nil, nil})
			inetSubnetVector(t, p, b, "INET '::/0'", "NULL::"+typ, []any{nil, nil, nil, nil, nil})
		}
		for _, op := range inetSubnetOps {
			for _, q := range []string{"SELECT $1 " + op + " INET '192.168.1.0/24' AS v", "SELECT INET '192.168.1.0/24' " + op + " $1 AS v"} {
				want := map[string]bool{"<<": false, "<<=": true, ">>": false, ">>=": true, "&&": true}[op]
				s := fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{want}}, "192.168.1.199/24")
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
			}
		}
		for _, op := range []string{"<<=", ">>="} {
			inetOperatorScalar(t, p, b, "'::1' "+op+" '::1'", "BOOL", true)
		}
		inetOperatorScalar(t, p, b, "INET '192.168.1.255/25'::CIDR <<= CIDR '192.168.1/24'", "BOOL", true)
	}
}

func TestReferenceINETSubnetSharedOperators(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		for _, tc := range []struct {
			e, typ string
			want   any
		}{
			{"1::smallint << 3", "INT2", int64(8)}, {"16 >> 2", "INT4", int64(4)},
			{"1::bigint << 4", "INT8", int64(16)}, {"B'1001' >> 1", "BIT", "0100"},
			{"ARRAY[1,2] && ARRAY[2,3]", "BOOL", true}, {"ARRAY[1] && ARRAY[2]", "BOOL", false},
		} {
			inetOperatorScalar(t, p, b, tc.e, tc.typ, tc.want)
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE subnet_shared(a int4range,b int4range,c int4range,m int4multirange,n int4multirange)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO subnet_shared VALUES ('[1,4)','[5,8)','[3,8)','{[1,4)}','{[3,8)}')", 1))
		for _, q := range []string{"SELECT a << b AS v FROM subnet_shared", "SELECT a && c AS v FROM subnet_shared", "SELECT m && n AS v FROM subnet_shared"} {
			s := fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{true}})
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
		}
	}
}

func inetSubnetMetadata(t *testing.T, rows *sql.Rows) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != 5 {
		t.Fatal(columns)
	}
	for i, c := range columns {
		if c.Name() != inetSubnetNames[i] || c.DatabaseTypeName() != "BOOL" || c.ScanType() != reflect.TypeFor[bool]() {
			t.Fatalf("column %d: %s %s %v", i, c.Name(), c.DatabaseTypeName(), c.ScanType())
		}
		if _, ok := c.Length(); ok {
			t.Fatal("BOOL length")
		}
		if _, _, ok := c.DecimalSize(); ok {
			t.Fatal("BOOL precision")
		}
		if nullable, ok := c.Nullable(); nullable || ok {
			t.Fatal("computed nullability", nullable, ok)
		}
	}
}

func TestReferenceINETSubnetBindsMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		q := inetSubnetQuery("$1::INET", "$2::INET")
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			a, z any
			want []any
		}{
			{"192.168.1.255/25", "192.168.1.199/24", []any{true, true, false, false, true}},
			{[]byte("2001:db8::ffff/114"), []byte("2001:db8::8001/113"), []any{true, true, false, false, true}},
			{"1.2.3.4", "::ffff:1.2.3.4", []any{false, false, false, false, false}},
			{[]byte(nil), "::/0", []any{nil, nil, nil, nil, nil}}, {"::1", nil, []any{nil, nil, nil, nil, nil}},
			{inetValuer{"::1"}, []byte("::/0"), []any{true, true, false, false, true}},
		} {
			s := fixture.Select(q, inetSubnetNames, inetSubnetTypes, [][]any{tc.want}, tc.a, tc.z)
			s.CheckMetadata = inetSubnetMetadata
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		for _, input := range []string{"bad", ""} {
			d := inetDiagnostic("inet", input, false)
			d.Context = "unnamed portal parameter $2 = '...'"
			if input == "" {
				d.Context = "unnamed portal parameter $2 = ''"
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, d, "::1", input))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, inetSubnetNames, inetSubnetTypes, [][]any{{true, true, false, false, true}}, "::1", "::/0"))
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE subnet_empty(h INET,n CIDR)", 0))
		for _, q := range []string{inetSubnetQuery("NULL::inet", "NULL::cidr"), inetSubnetQuery("h", "n") + " FROM subnet_empty"} {
			var want [][]any
			if !strings.Contains(q, "FROM") {
				want = [][]any{{nil, nil, nil, nil, nil}}
			}
			s := fixture.Select(q, inetSubnetNames, inetSubnetTypes, want)
			s.CheckMetadata = inetSubnetMetadata
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
		}
	}
}
