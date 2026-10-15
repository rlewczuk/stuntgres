package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringSliceMetadata(t *testing.T) {
	stringSliceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE slice_metadata(id int,s text,n int,pattern text,sqlpat text,e text)", 0),
			fixture.Exec(`INSERT INTO slice_metadata VALUES(1,'foobar',2,'o(.)b','%#"o_b#"%','#'),(2,NULL,2,'o(.)b','%#"o_b#"%','#')`, 2),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		expressions := "left(s,n) AS l,right(s,n) AS r,substring(s,n,2) AS s,substr(s,n) AS a,substring(s,pattern) AS p,substring(s,sqlpat,e) AS q"
		computed := "SELECT " + expressions + " FROM slice_metadata WHERE id=$1"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW slice_metadata_view AS SELECT id,"+expressions+" FROM slice_metadata", 0))
		for _, route := range []struct {
			query      string
			storedView bool
		}{
			{computed, false},
			{"SELECT l,r,s,a,p,q FROM slice_metadata_view WHERE id=$1", true},
		} {
			t.Run(route.query, func(t *testing.T) {
				stmt, err := c.PrepareContext(p.Ctx, route.query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: route.query}
				// All cases independently require unconstrained TEXT: string scan,
				// MaxInt64 known length, no decimal capability, and the fixture's
				// computed-versus-stored-view nullability contract on each backend.
				for _, tc := range []struct {
					id   int
					rows [][]any
				}{
					{1, [][]any{{"fo", "ar", "oo", "oobar", "o", "oob"}}},
					{2, [][]any{{nil, nil, nil, nil, nil, nil}}},
					{99, nil},
					{1, [][]any{{"fo", "ar", "oo", "oobar", "o", "oob"}}},
				} {
					s := fixture.Select(route.query, []string{"l", "r", "s", "a", "p", "q"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, tc.rows, tc.id)
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						fixture.FormatReferenceMetadata(t, b.PG, rows, route.storedView)
					}
					stringPadBoth(t, p, b, c, s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
			})
		}
	})
}
