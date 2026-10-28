package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func intaggCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "ia_addon")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if b.PG {
				clean := func() {
					for _, q := range []string{"ROLLBACK", "RESET search_path", "DROP EXTENSION IF EXISTS intagg CASCADE", "DROP SCHEMA IF EXISTS ia_addon CASCADE"} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				}
				clean()
				defer clean()
			}
			body(t, p, b, c)
		})
	}
}

func intaggInstall(run func(fixture.Step)) {
	run(fixture.Exec("CREATE SCHEMA ia_addon", 0))
	run(fixture.Exec("CREATE EXTENSION intagg SCHEMA ia_addon VERSION '1.1'", 0))
}

func intaggInput(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE ia_input(id int,k text,v integer)", 0))
	run(fixture.Exec("INSERT INTO ia_input VALUES(1,'a',2),(2,'a',NULL),(3,'a',2),(4,'b',-2147483648),(5,'b',2147483647)", 5))
}

func intaggArray(b fixture.Backend, q string, columns []string, types []string, rows [][]any, args ...any) fixture.Step {
	s := fixture.ArrayReferenceSelect(q, columns, types, types, rows, b.PG, args...)
	if len(types) == 1 && types[0] == "_INT4" {
		s.CheckMetadata = fixture.ArrayAggMetadata(b.PG, types)
	}
	return s
}

func TestReferenceIntaggAggregate(t *testing.T) {
	intaggCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		intaggInstall(run)
		intaggInput(run)
		for i, tc := range []struct {
			q    string
			rows [][]any
		}{
			{"SELECT ia_addon.int_array_aggregate(v ORDER BY id) FROM ia_input", [][]any{{"{2,NULL,2,-2147483648,2147483647}"}}},
			{"SELECT ia_addon.int_array_aggregate(v ORDER BY v DESC NULLS FIRST,id) FROM ia_input", [][]any{{"{NULL,2147483647,2,2,-2147483648}"}}},
			{"SELECT ia_addon.int_array_aggregate(DISTINCT v ORDER BY v NULLS LAST) FROM ia_input WHERE k='a'", [][]any{{"{2,NULL}"}}},
			{"SELECT ia_addon.int_array_aggregate(v ORDER BY id) FILTER(WHERE k='a') FROM ia_input", [][]any{{"{2,NULL,2}"}}},
			{"SELECT ia_addon.int_array_aggregate(v) FROM ia_input WHERE FALSE", [][]any{{nil}}},
			{"SELECT ia_addon.int_array_aggregate(v) FILTER(WHERE FALSE) FROM ia_input", [][]any{{nil}}},
			{"SELECT ia_addon.int_array_aggregate(v) FILTER(WHERE NULL) FROM ia_input", [][]any{{nil}}},
			{"SELECT ia_addon.int_array_aggregate(v) FROM ia_input WHERE FALSE GROUP BY k", nil},
			{"SELECT ia_addon.int_array_aggregate(NULL)", [][]any{{"{NULL}"}}},
			{"SELECT ia_addon.int_array_aggregate(7::smallint)", [][]any{{"{7}"}}},
			{"SELECT ia_addon.int_array_aggregate($1)", [][]any{{"{9}"}}},
			{"SELECT ia_addon.int_array_aggregate(v ORDER BY id) FROM ia_input HAVING count(*)=5", [][]any{{"{2,NULL,2,-2147483648,2147483647}"}}},
		} {
			t.Run(fixture.Position(i), func(t *testing.T) {
				var args []any
				if i == 10 {
					args = []any{int64(9)}
				}
				fixture.ArrayAggBoth(func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }, intaggArray(b, tc.q, []string{"int_array_aggregate"}, []string{"_INT4"}, tc.rows, args...))
			})
		}
		fixture.ArrayAggBoth(run, intaggArray(b, "SELECT k,ia_addon.int_array_aggregate(v ORDER BY id) FROM ia_input GROUP BY k ORDER BY k", []string{"k", "int_array_aggregate"}, []string{"TEXT", "_INT4"}, [][]any{{"a", "{2,NULL,2}"}, {"b", "{-2147483648,2147483647}"}}))
		fixture.ArrayAggBoth(run, intaggArray(b, "SELECT array_agg(v ORDER BY id),ia_addon.int_array_aggregate(v ORDER BY id),ia_addon.int_array_aggregate(v ORDER BY id) FROM ia_input", []string{"array_agg", "int_array_aggregate", "int_array_aggregate"}, []string{"_INT4", "_INT4", "_INT4"}, [][]any{{"{2,NULL,2,-2147483648,2147483647}", "{2,NULL,2,-2147483648,2147483647}", "{2,NULL,2,-2147483648,2147483647}"}}))
		// Observe the unordered aggregate only through its element bag.
		s := fixture.Select("SELECT ia_addon.int_array_enum(a) AS v FROM (SELECT ia_addon.int_array_aggregate(v) AS a FROM ia_input) AS s", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {nil}, {int64(2)}, {int64(-2147483648)}, {int64(2147483647)}})
		s.Bag = true
		fixture.ArrayAggBoth(run, s)
		run(fixture.Exec("CREATE TABLE ia_out(v integer[])", 0))
		run(fixture.Exec("INSERT INTO ia_out SELECT ia_addon.int_array_aggregate(v ORDER BY id) FROM ia_input", 1))
		fixture.ArrayAggBoth(run, intaggArray(b, "SELECT v FROM ia_out", []string{"v"}, []string{"_INT4"}, [][]any{{"{2,NULL,2,-2147483648,2147483647}"}}))
		run(fixture.Exec("CREATE DOMAIN ia_int AS integer", 0))
		fixture.ArrayAggBoth(run, intaggArray(b, "SELECT ia_addon.int_array_aggregate(4::ia_int)", []string{"int_array_aggregate"}, []string{"_INT4"}, [][]any{{"{4}"}}))
	})
}

func TestReferenceIntaggEnumeration(t *testing.T) {
	intaggCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		intaggInstall(run)
		run(fixture.Exec("CREATE TABLE ia_arrays(id int,a integer[])", 0))
		run(fixture.Exec("INSERT INTO ia_arrays VALUES(1,'{2,2,NULL}'),(2,'{}'),(3,NULL)", 3))
		for _, q := range []string{"SELECT ia_addon.int_array_enum(a) FROM ia_arrays", "SELECT * FROM ia_addon.int_array_enum('{2,2,NULL}'::integer[])", "SELECT ia_addon.int_array_enum('{2,2,NULL}')"} {
			s := fixture.Select(q, []string{"int_array_enum"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}, {nil}})
			s.Bag = true
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { fixture.JSONTypeMetadataCheck(t, rows, b.PG, s.Columns, s.Types) }
			fixture.ArrayAggBoth(run, s)
		}
		for _, arg := range []string{"'{}'::integer[]", "NULL::integer[]", "NULL"} {
			for _, q := range []string{"SELECT ia_addon.int_array_enum(" + arg + ")", "SELECT * FROM ia_addon.int_array_enum(" + arg + ")"} {
				s := fixture.Select(q, []string{"int_array_enum"}, []string{"INT4"}, nil)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { fixture.JSONTypeMetadataCheck(t, rows, b.PG, s.Columns, s.Types) }
				fixture.ArrayAggBoth(run, s)
			}
		}
		fixture.ArrayAggBoth(run, fixture.Select("SELECT v,n FROM ia_addon.int_array_enum('[0:1][3:4]={{1,2},{3,NULL}}'::integer[]) WITH ORDINALITY AS u(v,n) ORDER BY n", []string{"v", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}, {nil, int64(4)}}))
		s := fixture.Select("SELECT ia_addon.int_array_enum(a)+1 AS v FROM ia_arrays", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(3)}, {nil}})
		s.Bag = true
		fixture.ArrayAggBoth(run, s)
		fixture.ArrayAggBoth(run, fixture.Select("SELECT id,v,n FROM ia_arrays LEFT JOIN LATERAL ia_addon.int_array_enum(a) WITH ORDINALITY AS u(v,n) ON TRUE ORDER BY id,n", []string{"id", "v", "n"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(2), int64(1)}, {int64(1), int64(2), int64(2)}, {int64(1), nil, int64(3)}, {int64(2), nil, nil}, {int64(3), nil, nil}}))
		fixture.ArrayAggBoth(run, fixture.Select("SELECT x,y,n FROM ROWS FROM (ia_addon.int_array_enum('{2,NULL}'::integer[]),unnest(ARRAY[7,8,9])) WITH ORDINALITY AS u(x,y,n) ORDER BY n", []string{"x", "y", "n"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(2), int64(7), int64(1)}, {nil, int64(8), int64(2)}, {nil, int64(9), int64(3)}}))
		q := "SELECT v,n FROM ia_addon.int_array_enum($1) WITH ORDINALITY AS u(v,n) ORDER BY n"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			arg  any
			rows [][]any
		}{
			{"{2,2,NULL}", [][]any{{int64(2), int64(1)}, {int64(2), int64(2)}, {nil, int64(3)}}},
			{"{}", nil}, {nil, nil},
			{"{-2147483648,2147483647}", [][]any{{int64(-2147483648), int64(1)}, {int64(2147483647), int64(2)}}},
		} {
			s := fixture.Select(q, []string{"v", "n"}, []string{"INT4", "INT8"}, tc.rows, tc.arg)
			fixture.ArrayAggBoth(run, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
	})
}
