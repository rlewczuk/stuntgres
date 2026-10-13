package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonArrayAggregateConsumers(t *testing.T,p *fixture.Pair,b fixture.Backend) {
	t.Helper()
	for _, q := range []string{
		"WITH a AS(SELECT json_arrayagg(v ORDER BY id RETURNING jsonb) AS v FROM ja_input WHERE g='a') SELECT v FROM a",
		"SELECT v FROM (SELECT jsonb_agg_strict(v ORDER BY id) AS v FROM ja_input WHERE g='a') AS a",
		"SELECT jsonb_agg_strict(v ORDER BY id) AS v FROM ja_input WHERE g='a' HAVING count(*)=3",
	} { jsonArrayAggregateRoutes(t,p,b,jsonArrayAggregateSelect(q,"JSONB","[2, 1]")) }
	for _, s:=range []fixture.Step{
		fixture.Exec("CREATE TABLE ja_destination(v jsonb)",0),
		fixture.Exec("INSERT INTO ja_destination SELECT json_arrayagg(v ORDER BY id RETURNING jsonb) FROM ja_input WHERE g='a'",1),
		fixture.Exec("INSERT INTO ja_destination SELECT json_arrayagg(v RETURNING jsonb) FROM ja_input WHERE FALSE",1),
		fixture.Exec("INSERT INTO ja_destination SELECT json_arrayagg(v RETURNING jsonb) FROM ja_input WHERE FALSE GROUP BY g",0),
		fixture.Exec("CREATE TABLE ja_ctas AS SELECT json_agg(v ORDER BY id) AS v FROM ja_input WHERE g='a'",1),
		fixture.Exec("CREATE MATERIALIZED VIEW ja_materialized AS SELECT json_arrayagg(v ORDER BY id RETURNING text) AS v FROM ja_input WHERE g='a'",1),
	} { fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s) }
	s:=fixture.Select("SELECT v FROM ja_destination",[]string{"v"},[]string{"JSONB"},[][]any{{"[2, 1]"},{nil}});s.Bag=true
	jsonArrayAggregateRoutes(t,p,b,s)
	jsonArrayAggregateRoutes(t,p,b,jsonArrayAggregateSelect("SELECT v FROM ja_ctas","JSON","[2, null, 1]"))
	jsonArrayAggregateRoutes(t,p,b,jsonArrayAggregateSelect("SELECT v FROM ja_materialized","TEXT","[2, 1]"))
	for _, setup:=range []fixture.Step{
		fixture.Exec("CREATE TABLE ja_bad(id int,g int,v text)",0),
		fixture.Exec(`INSERT INTO ja_bad VALUES(1,1,'1'),(2,2,'no')`,2),
		fixture.Exec("CREATE TABLE ja_atomic(g int,v jsonb)",0),
		fixture.Exec(`INSERT INTO ja_atomic VALUES(9,'[9]')`,1),
	} {fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,setup)}
	q:="INSERT INTO ja_atomic SELECT g,json_arrayagg(v FORMAT JSON ORDER BY id RETURNING jsonb) FROM ja_bad GROUP BY g ORDER BY g"
	d:=fixture.ControlsReferenceDiagnostic("22P02","invalid input syntax for type json");d.Detail,d.Context=`Token "no" is invalid.`,"JSON data, line 1: no"
	for _, prepared:=range []bool{false,true} {
		s:=fixture.ErrorDiagnostic(q,d);s.Prepared,s.ErrorStage=prepared,"execute"
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
		jsonArrayAggregateRoutes(t,p,b,fixture.Select("SELECT g,v FROM ja_atomic",[]string{"g","v"},[]string{"INT4","JSONB"},[][]any{{int64(9),"[9]"}}))
	}
	ctx,cancel:=context.WithCancel(p.Ctx);cancel()
	if rows,err:=b.DB.QueryContext(ctx,"SELECT json_agg(v ORDER BY id) FROM ja_input");rows!=nil||!errors.Is(err,context.Canceled) {if rows!=nil {rows.Close()};t.Fatalf("precanceled Query=%v/%v",rows,err)}
	if result,err:=b.DB.ExecContext(ctx,"INSERT INTO ja_destination SELECT jsonb_agg(v) FROM ja_input");result!=nil||!errors.Is(err,context.Canceled){t.Fatalf("precanceled Exec=%v/%v",result,err)}
	jsonArrayAggregateRoutes(t,p,b,s)
}

func TestReferenceJSONArrayAggregateConsumers(t *testing.T) {
	jsonArrayAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){jsonArrayAggregateSetup(t,p,b);jsonArrayAggregateConsumers(t,p,b)})
	jsonArrayAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		jsonArrayAggregateSetup(t,p,b)
		for _, tc:=range []struct{q,message,token string}{
			{"INSERT INTO ja_input(id,v) VALUES(8,json_agg(1))","aggregate functions are not allowed in VALUES","json_agg"},
			{"INSERT INTO ja_input(id,v) VALUES(8,1) RETURNING json_arrayagg(v)","aggregate functions are not allowed in RETURNING","json_arrayagg"},
			{"CREATE TABLE ja_check(v int CHECK(json_agg(v) IS NOT NULL))","aggregate functions are not allowed in check constraints","json_agg"},
			{"CREATE TABLE ja_generated(v int,j json GENERATED ALWAYS AS(json_arrayagg(v)) STORED)","aggregate functions are not allowed in column generation expressions","json_arrayagg"},
			{"CREATE TABLE ja_default(j json DEFAULT json_agg(1))","aggregate functions are not allowed in DEFAULT expressions","json_agg"},
			{"CREATE INDEX ja_index ON ja_input((json_agg(v)))","aggregate functions are not allowed in index expressions","json_agg"},
			{"SELECT v FROM ja_input LIMIT json_agg(v)","aggregate functions are not allowed in LIMIT","json_agg"},
			{"SELECT a.v FROM ja_input a JOIN ja_input b ON json_agg(a.v) IS NOT NULL","aggregate functions are not allowed in JOIN conditions","json_agg"},
		} {
			t.Run(tc.q,func(t *testing.T){
				d:=fixture.ControlsReferenceDiagnostic("42803",tc.message);d.Position=jsonArrayAggregatePosition(tc.q,tc.token)
				jsonArrayAggregateError(t,p,b,tc.q,d,true)
				jsonArrayAggregateRoutes(t,p,b,fixture.Select("SELECT id FROM ja_input ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)},{int64(3)},{int64(4)}}))
			})
		}
	})
	t.Run("ACL",func(t *testing.T){
		fixture.RoleReferenceCleanup(t,[]string{"ja_reader"})
		jsonArrayAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
			jsonArrayAggregateSetup(t,p,b)
			run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)}
			run(fixture.Exec("CREATE ROLE ja_reader",0))
			if b.PG {run(fixture.Exec("GRANT USAGE ON SCHEMA public TO ja_reader",0))}
			run(fixture.Exec("GRANT SELECT(v) ON ja_input TO ja_reader",0))
			conn,err:=b.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)};defer conn.Close();defer conn.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION")
			fixture.RunStep(t,p.Ctx,conn,b.Recorder,fixture.Exec("SET SESSION AUTHORIZATION ja_reader",0))
			denied:=fixture.ControlsReferenceDiagnostic("42501","permission denied for table ja_input")
			run(fixture.Exec("REVOKE SELECT(v) ON ja_input FROM ja_reader",0))
			for _, prepared:=range []bool{false,true} {
				s:=fixture.ErrorDiagnostic("SELECT json_agg_strict(v) FROM ja_input WHERE FALSE",denied)
				s.SelectRows,s.Prepared,s.ErrorStage=true,prepared,"execute"
				fixture.RunStep(t,p.Ctx,conn,b.Recorder,s)
			}
			run(fixture.Exec("GRANT SELECT(v) ON ja_input TO ja_reader",0))
			for _, q:=range []string{
				"SELECT json_agg_strict(v ORDER BY id) FROM ja_input WHERE FALSE",
				"SELECT json_arrayagg(v) FILTER(WHERE g='b') FROM ja_input WHERE FALSE",
			} {for _, prepared:=range []bool{false,true}{s:=fixture.ErrorDiagnostic(q,denied);s.SelectRows,s.Prepared,s.ErrorStage=true,prepared,"execute";fixture.RunStep(t,p.Ctx,conn,b.Recorder,s)}}
			run(fixture.Exec("GRANT SELECT(id,g) ON ja_input TO ja_reader",0))
			const q="SELECT json_arrayagg(v ORDER BY id) FILTER(WHERE g='a') AS v FROM ja_input"
			stmt,err:=conn.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
			x:=fixture.HeldStatement{conn,stmt,q};s:=jsonArrayAggregateSelect(q,"JSON","[2, 1]")
			fixture.RunStep(t,p.Ctx,x,b.Recorder,s)
			run(fixture.Exec("REVOKE SELECT(v) ON ja_input FROM ja_reader",0))
			failure:=fixture.ErrorDiagnostic(q,denied);failure.SelectRows,failure.ErrorStage=true,"execute";fixture.RunStep(t,p.Ctx,x,b.Recorder,failure)
			run(fixture.Exec("GRANT SELECT(v) ON ja_input TO ja_reader",0));fixture.RunStep(t,p.Ctx,x,b.Recorder,s)
		})
	})
}
