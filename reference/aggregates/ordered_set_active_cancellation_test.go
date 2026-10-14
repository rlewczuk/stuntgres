package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceOrderedSetAggregateActiveCancellation(t *testing.T) {
	orderedSetScenario(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend) {
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE os_active(x INT)",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO os_active SELECT generate_series(1,500000)",500000))
		q:="SELECT percentile_cont(.5) WITHIN GROUP(ORDER BY x DESC) AS v FROM os_active"
		stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer stmt.Close()
		for _,prepared:=range []bool{false,true} {
			// Size alone is not evidence: the common fixture observes this real
			// query on PG, or the actual native sort and worker stack, before cancel.
			fixture.BinaryConversionActiveCancel(t,p,b,c,q,"sortOrderedAggregate",func(ctx context.Context) fixture.RelationOutcome {
				var rows *sql.Rows;var err error
				if prepared {rows,err=stmt.QueryContext(ctx)} else {rows,err=c.QueryContext(ctx,q)}
				return fixture.RelationOutcome{Rows:rows,Err:err}
			})
			fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q},b.Recorder,orderedSetValue(q,"FLOAT8",float64(250000.5)))
		}
	})
}
