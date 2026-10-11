package reference_test

import (
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQuerySubqueryMergedViewPresentation(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE presentation_source(id INT,v INT)", 0))
		run(fixture.Exec("INSERT INTO presentation_source VALUES(1,10),(2,20),(2,21)", 3))
		run(fixture.Exec("CREATE VIEW presentation_view(renamed,n,first) AS SELECT o.id,(SELECT count(*) FROM presentation_source i WHERE i.id=o.id),(SELECT d.x FROM (SELECT i.v AS x FROM presentation_source i WHERE i.id=o.id ORDER BY i.v LIMIT 1) d) FROM presentation_source o GROUP BY o.id ORDER BY o.id", 0))
		fixture.ViewCopy(t, p, b, "presentation_view", "presentation_catalog")
		for _, pretty := range []bool{false, true} {
			name := "presentation_compact"
			if pretty {
				name = "presentation_pretty"
			}
			fixture.ViewCopySQL(t, p, b, name, "SELECT pg_get_viewdef('presentation_view'::regclass,CAST($1 AS BOOL)) AS definition", pretty)
		}
		check := func() {
			for _, name := range []string{"presentation_view", "presentation_catalog", "presentation_compact", "presentation_pretty"} {
				subqueryBoth(run, fixture.Select("SELECT * FROM "+name+" ORDER BY renamed", []string{"renamed", "n", "first"}, []string{"INT4", "INT8", "INT4"}, [][]any{{int64(1), int64(1), int64(10)}, {int64(2), int64(2), int64(20)}}))
			}
		}
		check()
		run(fixture.Exec("ALTER TABLE presentation_source RENAME TO presentation_renamed", 0))
		run(fixture.Exec("ALTER TABLE presentation_renamed RENAME COLUMN v TO changed_v", 0))
		check()
		if !b.PG {
			dump := fixture.ViewDump(t, p)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			fixture.ViewRestore(t, p, dump)
			check()
		}
	})
}
