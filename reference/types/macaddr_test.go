package reference_test

import (
	"database/sql/driver"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const macaddrSample = "08:00:2b:01:02:03"

var macaddrForms = []string{
	"08:00:2b:01:02:03", "08-00-2b-01-02-03", "08002b:010203",
	"08002b-010203", "0800.2b01.0203", "0800-2b01-0203", "08002b010203",
}

func macaddrScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func macaddrQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

type macaddrValuer struct{ value driver.Value }

func (v macaddrValuer) Value() (driver.Value, error) { return v.value, nil }

func macaddrValue(q, value string, args ...any) fixture.Step {
	return fixture.Select(q, []string{"v"}, []string{"MACADDR"}, [][]any{{value}}, args...)
}

func TestReferenceMACADDRValues(t *testing.T) {
	macaddrScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE mac_values(id integer,v macaddr)", 0))
		q := "SELECT $1::macaddr AS v"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		vectors := [][2]string{}
		for _, input := range macaddrForms {
			vectors = append(vectors, [2]string{input, macaddrSample}, [2]string{strings.ToUpper(input), macaddrSample})
		}
		vectors = append(vectors, [2]string{"08:00:2B:01:02:03", macaddrSample}, [2]string{"00:00:00:00:00:00", "00:00:00:00:00:00"}, [2]string{"ff:ff:ff:ff:ff:ff", "ff:ff:ff:ff:ff:ff"}, [2]string{"80:01:02:03:04:05", "80:01:02:03:04:05"})
		for id, v := range vectors {
			for _, prepared := range []bool{false, true} {
				for _, q := range []string{
					"SELECT " + macaddrQuote(v[0]) + "::macaddr AS v",
					"SELECT MACADDR " + macaddrQuote(v[0]) + " AS v",
					"SELECT pg_catalog.macaddr " + macaddrQuote(v[0]) + " AS v",
					"SELECT CAST(" + macaddrQuote(v[0]) + " AS pg_catalog.macaddr) AS v",
					"SELECT " + macaddrQuote(v[0]) + "::text::macaddr AS v",
				} {
					s := macaddrValue(q, v[1])
					s.Prepared = prepared
					run(s)
				}
				for _, arg := range []any{v[0], []byte(v[0]), macaddrValuer{v[0]}} {
					s := macaddrValue("SELECT $1::macaddr AS v", v[1], arg)
					s.Prepared = prepared
					run(s)
				}
			}
			for _, arg := range []any{v[0], []byte(v[0]), macaddrValuer{v[0]}} {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, macaddrValue(q, v[1], arg))
			}
			run(fixture.Exec("INSERT INTO mac_values VALUES($1,$2)", 1, id, v[0]))
			run(macaddrValue("SELECT v FROM mac_values WHERE id=$1", v[1], id))
		}
		s := fixture.Select("SELECT '08002b010203'::macaddr::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{macaddrSample}})
		s.NonContext = true
		run(s)
	})
}

func TestReferenceMACADDRComparison(t *testing.T) {
	macaddrScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		ordered := []string{"00:00:00:00:00:ff", "00:00:00:00:01:00", "00:00:ff:ff:ff:ff", "00:01:00:00:00:00", "7f:ff:ff:ff:ff:ff", "80:00:00:00:00:00", "ff:ff:ff:ff:ff:ff"}
		run(fixture.Exec("CREATE TABLE mac_order(v macaddr)", 0))
		for i := len(ordered) - 1; i >= 0; i-- {
			run(fixture.Exec("INSERT INTO mac_order VALUES($1)", 1, ordered[i]))
		}
		run(fixture.Exec("INSERT INTO mac_order VALUES(NULL)", 1))
		var ascending, descending [][]any
		for _, v := range ordered {
			ascending = append(ascending, []any{v})
		}
		ascending = append(ascending, []any{nil})
		descending = append(descending, []any{nil})
		for i := len(ordered) - 1; i >= 0; i-- {
			descending = append(descending, []any{ordered[i]})
		}
		for _, prepared := range []bool{false, true} {
			for _, s := range []fixture.Step{
				fixture.Select("SELECT v FROM mac_order ORDER BY v NULLS LAST", []string{"v"}, []string{"MACADDR"}, ascending),
				fixture.Select("SELECT v FROM mac_order ORDER BY v DESC NULLS FIRST", []string{"v"}, []string{"MACADDR"}, descending),
				fixture.Select("SELECT v FROM mac_order WHERE v=NULL", []string{"v"}, []string{"MACADDR"}, nil),
				fixture.Select("SELECT v FROM mac_order WHERE v IS NULL", []string{"v"}, []string{"MACADDR"}, [][]any{{nil}}),
			} {
				s.Prepared = prepared
				run(s)
			}
			for _, pair := range []struct {
				left, right string
				want        []any
			}{
				{ordered[0], ordered[1], []any{false, true, true, true, false, false}},
				{ordered[1], ordered[0], []any{false, true, false, false, true, true}},
				{macaddrSample, "0800.2b01.0203", []any{true, false, false, true, false, true}},
			} {
				for i, op := range []string{"=", "<>", "<", "<=", ">", ">="} {
					s := fixture.Select("SELECT $1::macaddr "+op+" $2::macaddr AS v", []string{"v"}, []string{"BOOL"}, [][]any{{pair.want[i]}}, pair.left, pair.right)
					s.Prepared = prepared
					run(s)
					for _, args := range [][]any{{nil, pair.right}, {pair.left, nil}} {
						s.Rows = [][]any{{nil}}
						s.Args = args
						run(s)
					}
				}
			}
		}
		q := "SELECT v FROM mac_order WHERE v=$1"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		for _, frame := range []struct {
			arg  any
			rows [][]any
		}{{"0000.0000.00ff", [][]any{{ordered[0]}}}, {macaddrSample, nil}, {nil, nil}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"MACADDR"}, frame.rows, frame.arg))
		}
	})
}

func TestReferenceMACADDRIdentity(t *testing.T) {
	macaddrScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		q := "SELECT oid::bigint AS id,typname,typlen,typbyval,typalign,typstorage,typcategory,typispreferred,typcollation::bigint AS coll,typarray::bigint AS arr FROM pg_type WHERE oid=829"
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select(q,
			[]string{"id", "typname", "typlen", "typbyval", "typalign", "typstorage", "typcategory", "typispreferred", "coll", "arr"},
			[]string{"INT8", "NAME", "INT2", "BOOL", "CHAR", "CHAR", "CHAR", "BOOL", "INT8", "INT8"},
			[][]any{{int64(829), "macaddr", int64(6), false, "i", "p", "U", false, int64(0), int64(1040)}}))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT oid::bigint AS id,typname,typelem::bigint AS elem FROM pg_type WHERE oid=1040", []string{"id", "typname", "elem"}, []string{"INT8", "NAME", "INT8"}, [][]any{{int64(1040), "_macaddr", int64(829)}}))
		// Class OIDs are initdb-assigned; assert identity relationships rather
		// than imposing another server's allocator sequence on this SQL result.
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select(
			"SELECT oid>0 AS valid,opcname,opcintype::bigint AS input,opcdefault,opcfamily::bigint AS family,opcmethod::bigint AS method FROM pg_opclass WHERE opcname='macaddr_ops' AND opcmethod=403",
			[]string{"valid", "opcname", "input", "opcdefault", "family", "method"},
			[]string{"BOOL", "NAME", "INT8", "BOOL", "INT8", "INT8"},
			[][]any{{true, "macaddr_ops", int64(829), true, int64(1984), int64(403)}}))
	})
}

func TestReferenceMACADDRBoundaries(t *testing.T) {
	macaddrScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		// PostgreSQL supports these types; native intentionally keeps them outside
		// this scalar feature. Identity lookup is covered independently above.
		if b.PG {
			return
		}
		for _, q := range []string{"CREATE TABLE excluded(v macaddr[])", "CREATE TABLE excluded(v macaddr8)"} {
			d := fixture.Diagnostic{Code: "0A000", Message: "unsupported column type", Position: "25"}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.ImplicitError(q, d))
		}
		for _, tc := range []struct{ q, message, position string }{
			{"SELECT ARRAY[MACADDR '08:00:2b:01:02:03']", "unsupported array element type", "8"},
			{"SELECT '{}'::macaddr[]", "unsupported column type", "14"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "0A000", Message: tc.message, Position: tc.position})
				s.SelectRows = true
				s.Prepared = prepared
				if prepared {
					s.ErrorStage = "prepare"
				} else {
					s.ErrorStage = "execute"
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
	})
}
