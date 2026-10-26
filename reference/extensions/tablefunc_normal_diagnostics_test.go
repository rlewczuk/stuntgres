package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTablefuncNormalDiagnostics(t *testing.T) {
	normalCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
		run(fixture.Exec("SET search_path TO normal_part,public",0))
		q:="SELECT normal_rand($1,$2,$3)"
		st,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer st.Close()
		held:=fixture.HeldStatement{Executor:c,Statement:st,SQL:q}
		for _,n:=range []int64{-1,-2147483648}{
			s:=normalNegative(fmt.Sprintf("SELECT * FROM normal_part.normal_rand(%d,0,1)",n));run(s);run(fixture.Prepared(s))
			s=normalNegative(q,n,0,1);run(s);run(fixture.Prepared(s));fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
		}
		for _,v:=range []struct{value,code,message string}{
			{"oops","22P02",`invalid input syntax for type integer: "oops"`},
			{"2147483648","22003",`value "2147483648" is out of range for type integer`},
		}{
			for _,prefix:=range []string{"","/*é*/ "}{
				// The opening quote is character 20; /*é*/ plus space adds six
				// characters (seven UTF-8 bytes), making the prefixed position 26.
				pos:="20";if prefix!=""{pos="26"}
				s:=fixture.ImplicitError(prefix+"SELECT normal_rand('"+v.value+"',0,1)",fixture.Diagnostic{Code:v.code,Message:v.message,Position:pos});s.ErrorStage="execute";run(s);s=fixture.Prepared(s);s.ErrorStage="prepare";run(s)
			}
			s:=fixture.ImplicitError(q,fixture.Diagnostic{Code:v.code,Message:v.message,Context:"unnamed portal parameter $1 = '...'"},v.value,0,1)
			run(s);run(fixture.Prepared(s));fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
		}
		for _,value:=range []string{"'NaN'","'Infinity'","'-Infinity'","'2147483648'"}{
			s:=fixture.ImplicitError("SELECT normal_rand(("+value+"::float8)::integer,0,1)",fixture.Diagnostic{Code:"22003",Message:"integer out of range"});run(s)
		}
		for _,tc:=range []struct{sql,signature string}{
			{"SELECT normal_rand(1::bigint,0,1)","bigint, integer, integer"},
			{"SELECT normal_rand(1::numeric,0,1)","numeric, integer, integer"},
			{"SELECT normal_rand(1::float8,0,1)","double precision, integer, integer"},
			{"SELECT normal_rand(1,0)","integer, integer"},
			{"SELECT normal_rand(numvals => 1,mean => 0,stddev => 1)","numvals => integer, mean => integer, stddev => integer"},
		}{
			s:=fixture.ImplicitError(tc.sql,fixture.Diagnostic{Code:"42883",Message:"function normal_rand("+tc.signature+") does not exist",Hint:"No function matches the given name and argument types. You might need to add explicit type casts.",Position:"8"});run(s);s=fixture.Prepared(s);s.ErrorStage="prepare";run(s)
		}
		run(fixture.Exec("CREATE TABLE normal_atomic(v float8)",0));run(fixture.Exec("INSERT INTO normal_atomic VALUES(9)",1))
		write:=normalNegative("INSERT INTO normal_atomic SELECT * FROM normal_rand(-1,0,1)")
		check:=fixture.Select("SELECT v FROM normal_atomic",[]string{"v"},[]string{"FLOAT8"},[][]any{{float64(9)}})
		run(write);run(check);run(fixture.Exec("BEGIN",0));run(write)
		run(fixture.ImplicitError("SELECT 1",fixture.Diagnostic{Code:"25P02",Message:"current transaction is aborted, commands ignored until end of transaction block"}))
		run(fixture.Exec("ROLLBACK",0));run(check)
	})
}
