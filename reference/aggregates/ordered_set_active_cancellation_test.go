package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceOrderedSetAggregateActiveCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		route := "direct"
		if prepared { route = "prepared" }
		t.Run(route, func(t *testing.T) {
		orderedSetScenario(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend) {
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE os_active(x INT)",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO os_active SELECT generate_series(1,64)",64))
		// Reuse a small stored seed. Active input has 64^3 nonconstant sums,
		// while the SAME held statement recovers with 2^3 deterministically
		// ordered inputs instead of completing another large successful sort.
		q:="SELECT percentile_cont(.5) WITHIN GROUP(ORDER BY a.x+b.x+c.x DESC) AS v FROM (SELECT x FROM os_active ORDER BY x LIMIT $1) AS a CROSS JOIN (SELECT x FROM os_active ORDER BY x LIMIT $1) AS b CROSS JOIN (SELECT x FROM os_active ORDER BY x LIMIT $1) AS c"
		stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer stmt.Close()
			// Each route owns its budget before setup. Native observation matches
			// the executing worker identity and a real sort frame, not its launcher.
			orderedSetActiveCancel(t,p,b,c,q,func(ctx context.Context) fixture.RelationOutcome {
				var rows *sql.Rows;var err error
				if prepared {rows,err=stmt.QueryContext(ctx,int64(64))} else {rows,err=c.QueryContext(ctx,q,int64(64))}
				return fixture.RelationOutcome{Rows:rows,Err:err}
			})
			fixture.RunStep(t,p.Ctx,c,b.Recorder,orderedSetValue("SELECT count(*) AS v FROM os_active","INT8",int64(64)))
			recovered:=orderedSetValue(q,"FLOAT8",float64(4.5));recovered.Args=[]any{int64(2)}
			fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q},b.Recorder,recovered)
	})
		})
	}
}
