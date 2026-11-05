package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIntarraySliceFeatureSmoke(t *testing.T) {
	intarrayScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		intarraySliceSeed(t,p,b,c)
		// Select every actual declaration against stored input, rather than
		// accepting a catalog-only or constant-only smoke result.
		for _,tc := range intarraySliceCases {
			args := tc.literals(tc.args)
			if tc.name != "intset" { args[0] = "a" }
			q := "SELECT "+tc.expression(args)+" AS v FROM ia_slice_rows WHERE id=1"
			intarrayBoth(t,p,b,c,intarraySliceResult(b,q,tc.family,tc.want))
		}
		for _,s := range []fixture.Step{
			intarraySliceResult(b,"SELECT ia_provider.idx(a,99) AS v FROM ia_slice_rows WHERE id=1","INT4",int64(0)),
			intarraySliceResult(b,"SELECT ia_provider.icount('{NULL,1,NULL}'::integer[]) AS v","INT4",int64(3)),
			intarraySliceResult(b,"SELECT OPERATOR(ia_provider.#) '{NULL,1,NULL}'::integer[] AS v","INT4",int64(3)),
			intarraySliceResult(b,"SELECT ia_provider.subarray(a,-5,2) AS v FROM ia_slice_rows WHERE id=1","_INT4","{11}"),
			intarraySliceResult(b,"SELECT ia_provider.subarray(a,2,0) AS v FROM ia_slice_rows WHERE id=1","_INT4","{22,33,22}"),
		} { intarrayBoth(t,p,b,c,s) }
		q := "SELECT ia_provider.subarray($1::integer[],$2::integer,$3::integer) AS v"
		stmt,err := c.PrepareContext(p.Ctx,q); if err != nil { t.Fatal(err) }; defer stmt.Close()
		held := fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
		fixture.RunStep(t,p.Ctx,held,b.Recorder,intarraySliceResult(b,q,"_INT4","{22,33}",intarraySliceA,int64(2),int64(2)))
		bad := fixture.ErrorDiagnostic(q,intarraySetNullError(),"{22,NULL}",int64(1),int64(2)); bad.SelectRows,bad.ErrorStage = true,"execute"
		fixture.RunStep(t,p.Ctx,held,b.Recorder,bad)
		fixture.RunStep(t,p.Ctx,held,b.Recorder,intarraySliceResult(b,q,"_INT4",nil,"{NULL}",nil,int64(2)))
		fixture.RunStep(t,p.Ctx,held,b.Recorder,intarraySliceResult(b,q,"_INT4","{8,9}",[]byte("{7,8,9}"),int64(2),int64(0)))
		if err := stmt.Close(); err != nil { t.Fatal(err) }
		intarrayError(t,p,b,c,"SELECT ia_provider.idx('{22,NULL}'::integer[],22)",false,"execute",intarraySetNullError())
		intarraySlicePresentation(t,p,b,c)
		intarraySliceDefaults(t,p,b,c)
		if !b.PG { intarraySliceNativeRoundtrip(t,p,b,c) } else { intarraySliceControlVerify(t,p,b,c,"stuntgres") }
	})
}
