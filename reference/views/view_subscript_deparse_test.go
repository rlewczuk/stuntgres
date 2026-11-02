package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// pg_get_viewdef parenthesizes a subscripted base unless it is a plain column.
func TestReferenceViewSubscriptDeparse(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE view_subscript_t(a integer[], c integer)", 0))
			run(fixture.Exec("INSERT INTO view_subscript_t VALUES ('{1,2,3}',3)", 1))
			run(fixture.Exec("CREATE VIEW view_subscript_v AS SELECT a[1] AS x,a[1:2] AS s,(a || c)[c] AS e FROM view_subscript_t", 0))
			def := fixture.Select(`SELECT pg_get_viewdef('view_subscript_v'::regclass) AS d`, []string{"d"}, []string{"TEXT"}, [][]any{{" SELECT a[1] AS x,\n    a[1:2] AS s,\n    ((a || c))[c] AS e\n   FROM view_subscript_t;"}})
			run(def)
			run(fixture.Prepared(def))
			run(fixture.Select(`SELECT x,s::text AS s,e FROM view_subscript_v`, []string{"x", "s", "e"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), "{1,2}", int64(3)}}))
			run(fixture.Exec("DROP VIEW view_subscript_v", 0))
			run(fixture.Exec("DROP TABLE view_subscript_t", 0))
		})
	}
}
