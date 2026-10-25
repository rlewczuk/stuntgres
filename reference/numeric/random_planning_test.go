package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRandomPlanningChildren(t *testing.T) {
	cases := []struct {
		name, query, typ string
	}{
		{"int4_right_error", "SELECT random(NULL::integer,1/0) AS v", ""},
		{"int8_right_error", "SELECT random(NULL::bigint,(1/0)::bigint) AS v", ""},
		{"numeric_right_error", "SELECT random(NULL::numeric,(1/0)::numeric) AS v", ""},
		{"int4_left_error", "SELECT random(1/0,NULL::integer) AS v", ""},
		{"int8_left_error", "SELECT random((1/0)::bigint,NULL::bigint) AS v", ""},
		{"numeric_left_error", "SELECT random((1/0)::numeric,NULL::numeric) AS v", ""},
		{"normal_right_error", "SELECT random_normal(NULL::double precision,1/0) AS v", ""},
		{"normal_left_error", "SELECT random_normal(1/0,NULL::double precision) AS v", ""},
		{"normal_default_error", "SELECT random_normal(1/0) AS v", ""},
		{"int4_null", "SELECT random(NULL::integer,1) AS v", "INT4"},
		{"int8_null", "SELECT random(NULL::bigint,1::bigint) AS v", "INT8"},
		{"numeric_null", "SELECT random(NULL::numeric,1::numeric) AS v", "NUMERIC"},
		{"normal_default_null", "SELECT random_normal(NULL::double precision) AS v", "FLOAT8"},
		{"normal_mean_null", "SELECT random_normal(NULL::double precision,1) AS v", "FLOAT8"},
		{"normal_stddev_null", "SELECT random_normal(0,NULL::double precision) AS v", "FLOAT8"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					want := fixture.Select(tc.query, []string{"v"}, []string{tc.typ}, [][]any{{nil}})
					if tc.typ == "" {
						want = fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
						want.SelectRows = true
						want.ErrorStage = "execute"
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, want)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(want))
					stmt, err := c.PrepareContext(p.Ctx, tc.query)
					if err != nil {
						t.Fatal("held prepare must not compute immutable children", err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.query}
					for range 2 {
						fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
					}
				})
			}
		})
	}
}

func TestReferenceRandomPlanningDiscardedSiblings(t *testing.T) {
	cases := []struct {
		name, typ, volatile, sequence, nonconstant string
	}{
		{"int4", "INT4", "random(NULL::integer,random(1,10))", "random(NULL::integer,nextval('og_rp_seq')::integer)", "random(NULL::integer,1/x)"},
		{"int8", "INT8", "random(NULL::bigint,random(1::bigint,10::bigint))", "random(NULL::bigint,nextval('og_rp_seq'))", "random(NULL::bigint,(1/x)::bigint)"},
		{"numeric", "NUMERIC", "random(NULL::numeric,random(1::numeric,10::numeric))", "random(NULL::numeric,nextval('og_rp_seq')::numeric)", "random(NULL::numeric,(1/x)::numeric)"},
		{"normal", "FLOAT8", "random_normal(NULL::double precision,random())", "random_normal(NULL::double precision,nextval('og_rp_seq'))", "random_normal(NULL::double precision,1/x)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"og_rp_reader"})
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run(fixture.Exec("CREATE SEQUENCE og_rp_seq START 101", 0))
					run(fixture.Exec("CREATE TABLE og_rp_source(x integer)", 0))
					run(fixture.Exec("INSERT INTO og_rp_source VALUES(0)", 1))
					run(fixture.Exec("CREATE ROLE og_rp_reader", 0))
					run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_rp_reader", 0))
					run(fixture.Exec("GRANT USAGE,SELECT ON SEQUENCE og_rp_seq TO og_rp_reader", 0))
					run(fixture.Exec("GRANT SELECT ON og_rp_source TO og_rp_reader", 0))
					randomReseed(t, p, c, b, .25)
					first := randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8")
					for _, witness := range []struct{ name, query string }{
						{"volatile", "SELECT " + tc.volatile + " AS v"},
						{"sequence", "SELECT " + tc.sequence + " AS v"},
						{"nonconstant", "SELECT " + tc.nonconstant + " AS v FROM og_rp_source"},
					} {
						t.Run(witness.name, func(t *testing.T) {
							stmt, err := c.PrepareContext(p.Ctx, witness.query)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: witness.query}
							for _, route := range []string{"direct", "fresh", "held_first", "held_again"} {
								t.Run(route, func(t *testing.T) {
									run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
									run(fixture.Exec("ALTER SEQUENCE og_rp_seq RESTART WITH 101", 0))
									randomReseed(t, p, c, b, .25)
									want := fixture.Select(witness.query, []string{"v"}, []string{tc.typ}, [][]any{{nil}})
									var executor fixture.Executor = c
									if route == "fresh" {
										want = fixture.Prepared(want)
									} else if route == "held_first" || route == "held_again" {
										executor = held
									}
									fixture.RoleGrantActor(run, "og_rp_reader", func() {
										fixture.RunStep(t, p.Ctx, executor, b.Recorder, want)
									})
									if got := randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8"); got != first {
										t.Fatalf("discarded sibling advanced own stream: got %v, want %v", got, first)
									}
									run(fixture.Select("SELECT last_value,is_called FROM og_rp_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(101), false}}))
								})
							}
						})
					}
				})
			}
		})
	}
}
