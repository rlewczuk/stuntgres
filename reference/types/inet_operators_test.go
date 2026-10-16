package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func inetOperatorScalar(t *testing.T, p *fixture.Pair, b fixture.Backend, expression, typ string, value any) {
	t.Helper()
	s := fixture.Select("SELECT "+expression+" AS v", []string{"v"}, []string{typ}, [][]any{{value}})
	t.Run("direct", func(t *testing.T) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) })
	t.Run("prepared", func(t *testing.T) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s)) })
}

func TestReferenceINETOperatorValues(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		for _, tc := range []struct {
			expression, typ string
			value           any
		}{
			{"~ inet '192.168.1.6'", "INET", "63.87.254.249"},
			{"inet '192.168.1.6' & inet '0.0.0.255'", "INET", "0.0.0.6"},
			{"inet '192.168.1.6' | inet '0.0.0.255'", "INET", "192.168.1.255"},
			{"inet '192.168.1.6' + 25::bigint", "INET", "192.168.1.31"},
			{"25::bigint + inet '192.168.1.6'", "INET", "192.168.1.31"},
			{"200 + inet '::ffff:fff0:1'", "INET", "::ffff:255.240.0.201"},
			{"inet '192.168.1.43' - 36::bigint", "INET", "192.168.1.7"},
			{"inet '192.168.1.43' - inet '192.168.1.19'", "INT8", int64(24)},
			{"inet '::1' - inet '::ffff:1'", "INT8", int64(-4294901760)},
			{"~ inet '192.168.1.6/24'", "INET", "63.87.254.249/24"},
			{"~ inet '0.0.0.0/0'", "INET", "255.255.255.255/0"},
			{"~ inet '::/64'", "INET", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff/64"},
			{"~ inet 'ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff'", "INET", "::"},
			{"inet '192.168.1.6/24' & inet '0.0.0.255/25'", "INET", "0.0.0.6/25"},
			{"inet '0.0.0.255/25' & inet '192.168.1.6/24'", "INET", "0.0.0.6/25"},
			{"inet '192.168.1.6/24' | inet '0.0.0.255/25'", "INET", "192.168.1.255/25"},
			{"inet '0.0.0.255/25' | inet '192.168.1.6/24'", "INET", "192.168.1.255/25"},
			{"inet '2001:db8:aaaa:5555:ffff::f0/64' & inet 'ffff:ffff:5555:aaaa::ff/96'", "INET", "2001:db8::f0/96"},
			{"inet 'ffff:ffff:5555:aaaa::ff/96' & inet '2001:db8:aaaa:5555:ffff::f0/64'", "INET", "2001:db8::f0/96"},
			{"inet '2001:db8:aaaa:5555:ffff::f0/64' | inet 'ffff:ffff:5555:aaaa::ff/96'", "INET", "ffff:ffff:ffff:ffff:ffff::ff/96"},
			{"inet 'ffff:ffff:5555:aaaa::ff/96' | inet '2001:db8:aaaa:5555:ffff::f0/64'", "INET", "ffff:ffff:ffff:ffff:ffff::ff/96"},
			{"inet '192.168.1.255/24' + 1", "INET", "192.168.2.0/24"},
			{"inet '2001:db8::ffff/64' + 1", "INET", "2001:db8::1:0/64"},
			{"inet '::ffff:ffff:ffff:ffff' + 1", "INET", "0:0:0:1::"},
			{"inet '0:0:0:1::' - 1", "INET", "::ffff:ffff:ffff:ffff"},
			{"inet '192.168.1.0/24' + (-1)::bigint", "INET", "192.168.0.255/24"},
			{"inet '192.168.1.0/24' - (-1)::bigint", "INET", "192.168.1.1/24"},
			{"inet '::1/64' + 0", "INET", "::1/64"},
			{"inet '::1/64' - 0", "INET", "::1/64"},
			{"inet '192.168.1.6/24' - inet '192.168.1.6/32'", "INT8", int64(0)},
			{"inet '255.255.255.255' - inet '0.0.0.0'", "INT8", int64(4294967295)},
			{"inet '0.0.0.0' - inet '255.255.255.255'", "INT8", int64(-4294967295)},
		} {
			t.Run(b.Name+"/"+tc.expression, func(t *testing.T) { inetOperatorScalar(t, p, b, tc.expression, tc.typ, tc.value) })
		}
	}
}

func TestReferenceINETOperatorResolution(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE DOMAIN operator_host AS INET", 0),
			fixture.Exec("CREATE DOMAIN operator_network AS CIDR", 0),
		} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		for _, tc := range []struct {
			expression, typ string
			value           any
		}{
			{"~ cidr '192.168.1/24'", "INET", "63.87.254.255/24"},
			{"cidr '192.168.1/24' | inet '0.0.0.255'", "INET", "192.168.1.255"},
			{"inet '0.0.0.255' & cidr '192.168.1/24'", "INET", "0.0.0.0"},
			{"cidr '192.168.1/24' + 6", "INET", "192.168.1.6/24"},
			{"6 + cidr '192.168.1/24'", "INET", "192.168.1.6/24"},
			{"cidr '192.168.1/24' - 1", "INET", "192.168.0.255/24"},
			{"cidr '192.168.1/24' - inet '192.168.0.255'", "INT8", int64(1)},
			{"~ ('192.168.1.6/24'::operator_host)", "INET", "63.87.254.249/24"},
			{"'192.168.1/24'::operator_network + 6", "INET", "192.168.1.6/24"},
			{"inet '::1' + 2::smallint", "INET", "::3"},
			{"2::integer + inet '::1'", "INET", "::3"},
			{"inet '::1' + '2'", "INET", "::3"},
			{"inet '::3' - '::1'", "INT8", int64(2)},
			{"inet '::3' & '::1'", "INET", "::1"},
			{"'::1' | inet '::2'", "INET", "::3"},
			{"inet '::1' OPERATOR(pg_catalog.+) 2::bigint", "INET", "::3"},
			{"~ NULL::inet", "INET", nil},
			{"NULL::inet & inet '::1'", "INET", nil},
			{"inet '1.2.3.4' | NULL::inet", "INET", nil},
			{"NULL::inet + 1", "INET", nil},
			{"1 + NULL::inet", "INET", nil},
			{"inet '::1' - NULL::bigint", "INET", nil},
			{"NULL::inet - inet '::1'", "INT8", nil},
			{"inet '::1' - NULL", "INT8", nil},
		} {
			t.Run(b.Name+"/"+tc.expression, func(t *testing.T) { inetOperatorScalar(t, p, b, tc.expression, tc.typ, tc.value) })
		}
	}
}

func inetOperatorMetadata(t *testing.T, rows *sql.Rows, pg bool, names, types []string) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != len(types) {
		t.Fatal(columns)
	}
	for i, c := range columns {
		scan := reflect.TypeFor[string]()
		if types[i] == "INT8" {
			scan = reflect.TypeFor[int64]()
		}
		if pg && types[i] == "INET" {
			scan = reflect.TypeFor[any]()
		}
		if c.Name() != names[i] || c.DatabaseTypeName() != types[i] || c.ScanType() != scan {
			t.Fatalf("column %d: %s %s %v", i, c.Name(), c.DatabaseTypeName(), c.ScanType())
		}
		if _, ok := c.Length(); ok {
			t.Fatal("unexpected length", i)
		}
		if _, _, ok := c.DecimalSize(); ok {
			t.Fatal("unexpected precision", i)
		}
		if n, ok := c.Nullable(); n || ok {
			t.Fatal("computed nullability", i, n, ok)
		}
	}
}

func TestReferenceINETOperatorMetadataAndBinds(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		inetOperatorCanceled(t, p, b)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE operator_metadata(h INET)", 0))
		for _, tc := range []struct {
			q    string
			rows [][]any
		}{
			{"SELECT ~ inet '::' AS h,inet '::3' - inet '::1' AS d", [][]any{{"ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", int64(2)}}},
			{"SELECT ~ NULL::inet AS h,NULL::inet - inet '::1' AS d", [][]any{{nil, nil}}},
			{"SELECT ~ h AS h,h - inet '::1' AS d FROM operator_metadata", nil},
		} {
			s := fixture.Select(tc.q, []string{"h", "d"}, []string{"INET", "INT8"}, tc.rows)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
				inetOperatorMetadata(t, r, b.PG, []string{"h", "d"}, []string{"INET", "INT8"})
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
		}
		for _, expression := range []string{"~ $1::inet", "$1::inet & $2::inet", "$1::inet | $2::inet", "$1::inet + $2::bigint", "$2::bigint + $1::inet", "$1::inet - $2::bigint", "$1::inet - $2::inet"} {
			t.Run(b.Name+"/"+expression, func(t *testing.T) {
				q := "SELECT " + expression + " AS v"
				stmt, err := b.DB.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
				for _, input := range []any{"::3", []byte("::3"), nil, inetValuer{"::3"}} {
					args := []any{input}
					typ, value := "INET", any("ffff:ffff:ffff:ffff:ffff:ffff:ffff:fffc")
					switch expression {
					case "$1::inet & $2::inet":
						args = append(args, "::1")
						value = "::1"
					case "$1::inet | $2::inet":
						args = append(args, []byte("::4"))
						value = "::7"
					case "$1::inet + $2::bigint", "$2::bigint + $1::inet":
						args = append(args, int64(2))
						value = "::5"
					case "$1::inet - $2::bigint":
						args = append(args, int64(2))
						value = "::1"
					case "$1::inet - $2::inet":
						args = append(args, "::1")
						typ, value = "INT8", int64(2)
					}
					if input == nil {
						value = nil
					}
					s := fixture.Select(q, []string{"v"}, []string{typ}, [][]any{{value}}, args...)
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
			})
		}
	}
}

// Native cancellation is an ordinary Go adapter boundary, not a synthetic
// PostgreSQL diagnostic or a transport-race assertion against pq.
func inetOperatorCanceled(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	if b.PG {
		return
	}
	q := "SELECT $1::inet + $2::bigint AS h"
	stmt, err := b.DB.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	for _, prepared := range []bool{false, true} {
		var rows *sql.Rows
		if prepared {
			rows, err = stmt.QueryContext(ctx, "::1", int64(1))
		} else {
			rows, err = b.DB.QueryContext(ctx, q, "::1", int64(1))
		}
		if rows != nil {
			rows.Close()
			t.Fatal("canceled query returned rows")
		}
		var state interface{ SQLState() string }
		if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
			t.Fatal("expected ordinary cancellation", err)
		}
	}
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"h"}, []string{"INET"}, [][]any{{"::2"}}, "::1", int64(1)))
}
