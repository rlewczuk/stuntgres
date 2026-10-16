package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

type objectGrantProviderCase struct {
	step         fixture.Step
	needed, held bool
}

func objectGrantProviderLifecycle(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), target, kind, name string, cases []objectGrantProviderCase, controls []fixture.Step) {
	t.Helper()
	defer objectGrantRoutineCleanup(t, p, c, []string{target})
	held := make(map[int]fixture.HeldStatement)
	defer func() {
		for _, h := range held {
			h.Statement.Close()
		}
	}()
	fixture.RoleGrantActor(run, "og_object_reader", func() {
		for i, tc := range cases {
			if !tc.held {
				continue
			}
			stmt, err := c.PrepareContext(p.Ctx, tc.step.Query)
			if err != nil {
				t.Fatal(err)
			}
			held[i] = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.step.Query}
		}
	})
	phase := func(allowed bool) {
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			for i, tc := range cases {
				s := tc.step
				if !allowed && tc.needed {
					s = fixture.ImplicitError(s.Query, fixture.Diagnostic{Code: "42501", Message: "permission denied for " + kind + " " + name}, s.Args...)
					s.SelectRows, s.ErrorStage = true, "execute"
				}
				run(s)
				if h, ok := held[i]; ok {
					fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
				}
			}
			for _, s := range controls {
				run(s)
			}
		})
	}
	phase(true)
	run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC", 0))
	phase(false)
	run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_reader", 0))
	phase(true)
	run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM og_object_reader", 0))
	phase(false)
}

func TestReferenceObjectGrantHypotheticalProviderAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, typ               string
		value, empty, nullValue any
	}{
		{"rank", "INT8", int64(2), int64(1), int64(5)},
		{"dense_rank", "INT8", int64(2), int64(1), int64(4)},
		{"percent_rank", "FLOAT8", float64(.2), float64(0), float64(.8)},
		{"cume_dist", "FLOAT8", float64(4.0 / 6), float64(1), float64(1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_hyp_provider(x INT,y TEXT)", 0))
				run(fixture.Exec("INSERT INTO og_hyp_provider VALUES(1,'a'),(1,'b'),(1,'b'),(2,'a'),(NULL,'z')", 5))
				run(fixture.Exec("GRANT SELECT ON og_hyp_provider TO og_object_reader", 0))
				selectValue := func(expr, tail string, value any, args ...any) fixture.Step {
					return fixture.Select("SELECT "+expr+" AS v FROM og_hyp_provider"+tail, []string{"v"}, []string{tc.typ}, [][]any{{value}}, args...)
				}
				call := tc.name + "(1,'b'::TEXT) WITHIN GROUP(ORDER BY x,y)"
				cases := []objectGrantProviderCase{
					{selectValue(call, "", tc.value), true, true},
					{selectValue(tc.name+"($1::INTEGER,$2::TEXT) WITHIN GROUP(ORDER BY x,y)", "", tc.value, int64(1), "b"), true, true},
					{selectValue(tc.name+"($1::INTEGER,$2::TEXT) WITHIN GROUP(ORDER BY x,y)", "", tc.nullValue, nil, "z"), true, false},
					{selectValue(call, " WHERE x>99", tc.empty), true, true},
					{selectValue(call, " WHERE FALSE", tc.empty), true, true},
					{selectValue(call+" FILTER(WHERE FALSE)", "", tc.empty), true, true},
				}
				other, otherTyp, otherValue := "rank", "INT8", any(int64(2))
				if tc.name == "rank" {
					other, otherTyp, otherValue = "dense_rank", "INT8", int64(2)
				}
				control := fixture.Select("SELECT "+other+"(1,'b'::TEXT) WITHIN GROUP(ORDER BY x,y) AS v FROM og_hyp_provider", []string{"v"}, []string{otherTyp}, [][]any{{otherValue}})
				objectGrantProviderLifecycle(t, p, b, c, run, "pg_catalog."+tc.name+`("any")`, "aggregate", tc.name, cases, []fixture.Step{control})
			})
		})
	}
}

func TestReferenceObjectGrantSQLJSONObjectAggProviderAuthority(t *testing.T) {
	for _, typ := range []string{"JSON", "JSONB"} {
		for _, unique := range []bool{false, true} {
			for _, absent := range []bool{false, true} {
				name := strings.ToLower(typ) + "_object_agg"
				policy := " NULL ON NULL"
				uniqueness := " WITHOUT UNIQUE KEYS"
				if unique {
					name += "_unique"
					uniqueness = " WITH UNIQUE KEYS"
				}
				if absent {
					name += "_strict"
					policy = " ABSENT ON NULL"
				}
				t.Run(name, func(t *testing.T) {
					objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
						run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
						run(fixture.Exec("CREATE TABLE og_sjo_provider(k TEXT,v INT)", 0))
						run(fixture.Exec("INSERT INTO og_sjo_provider VALUES('a',1)", 1))
						run(fixture.Exec("GRANT SELECT ON og_sjo_provider TO og_object_reader", 0))
						value, nullValue := `{ "a" : 1 }`, `{ "a" : null }`
						if typ == "JSONB" {
							value, nullValue = `{"a": 1}`, `{"a": null}`
						}
						if absent {
							nullValue = `{  }`
							if typ == "JSONB" {
								nullValue = `{}`
							}
						}
						call := func(pair string) string {
							return "json_objectagg(" + pair + policy + uniqueness + " RETURNING " + strings.ToLower(typ) + ")"
						}
						selectValue := func(expr, tail string, want any, args ...any) fixture.Step {
							return fixture.Select("SELECT "+expr+" AS v FROM og_sjo_provider"+tail, []string{"v"}, []string{typ}, [][]any{{want}}, args...)
						}
						cases := []objectGrantProviderCase{
							{selectValue(call("k:v"), "", value), true, true},
							{selectValue(call("$1::TEXT:$2::INTEGER"), "", value, "a", int64(1)), true, true},
							{selectValue(call("$1::TEXT:$2::INTEGER"), "", nullValue, "a", nil), true, false},
							{selectValue(call("k:NULL::INTEGER"), "", nullValue), true, true},
							{selectValue(call("k:v"), " WHERE v>99", nil), true, true},
							{selectValue(call("k:v"), " WHERE FALSE", nil), true, true},
							{selectValue(call("k:v")+" FILTER(WHERE FALSE)", "", nil), true, true},
						}
						// Flip uniqueness alone: nearest distinct selected routine,
						// identical valid input and result, no ordinary syntax expansion.
						otherUnique := " WITH UNIQUE KEYS"
						if unique {
							otherUnique = " WITHOUT UNIQUE KEYS"
						}
						control := selectValue("json_objectagg(k:v"+policy+otherUnique+" RETURNING "+strings.ToLower(typ)+")", "", value)
						objectGrantProviderLifecycle(t, p, b, c, run, "pg_catalog."+name+`("any","any")`, "aggregate", name, cases, []fixture.Step{control})
					})
				})
			}
		}
	}
}
