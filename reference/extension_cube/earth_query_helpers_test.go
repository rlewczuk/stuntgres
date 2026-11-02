package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// A sequence shares one SQL spelling but changes bound values. Every step is
// independently asserted direct/fresh, then twice through the same held handle.
func earthQueryRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, steps ...fixture.Step) {
	t.Helper()
	if len(steps) == 0 {
		t.Fatal("empty EarthQuery route sequence")
	}
	for _, s := range steps {
		if s.Query != steps[0].Query {
			t.Fatal("EarthQuery held sequence changes SQL spelling")
		}
	}
	t.Run("query="+steps[0].Query, func(t *testing.T) {
		for i, s := range steps {
			for _, route := range []string{"direct", "fresh"} {
				t.Run(fmt.Sprintf("%s/step-%d/args=%#v", route, i+1, s.Args), func(t *testing.T) {
					step := s
					if route == "fresh" {
						step = fixture.Prepared(step)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				})
			}
		}
		t.Run("held", func(t *testing.T) {
			h := earthHeld(t, p, c, steps[0].Query)
			defer h.Statement.Close()
			for repeat := range 2 {
				for i, s := range steps {
					t.Run(fmt.Sprintf("repeat-%d/step-%d/args=%#v", repeat+1, i+1, s.Args), func(t *testing.T) {
						fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
					})
				}
			}
		})
	})
}

func earthQueryCandidateSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE earth_query_candidates(id integer,p earth)", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO earth_query_candidates VALUES (1,ll_to_earth(0,0)),(2,ll_to_earth(0,0.5)),(3,ll_to_earth(0.8,0.8)),(4,ll_to_earth(0,2)),(5,NULL)", 5))
}

func earthQueryCandidateVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, predicate := range []string{
		"earth_box(ll_to_earth(0,0),$1::float8) @> p",
		"p <@ earth_box(ll_to_earth(0,0),$1::float8)",
	} {
		for _, exact := range []bool{false, true} {
			where := predicate
			want := [][]any{{int64(1)}, {int64(2)}, {int64(3)}}
			if exact {
				where += " AND earth_distance(ll_to_earth(0,0),p)<=$1::float8"
				want = [][]any{{int64(1)}, {int64(2)}}
			}
			q := "SELECT id FROM earth_query_candidates WHERE " + where + " ORDER BY id"
			earthQueryRoutes(t, p, b, c,
				fixture.Select(q, []string{"id"}, []string{"INT4"}, want, float64(100000)),
				fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}, float64(0)),
				fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{}, nil),
			)
		}
	}
	q := "SELECT earth_box(ll_to_earth(0,0),100000) @> p AS candidate,earth_distance(ll_to_earth(0,0),p)>100000 AS outside FROM earth_query_candidates WHERE id=3"
	earthQueryRoutes(t, p, b, c, fixture.Select(q, []string{"candidate", "outside"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
}
