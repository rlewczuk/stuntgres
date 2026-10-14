package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceOrderedSetAggregateDirectArguments(t *testing.T) {
	orderedSetScenario(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend) {
		orderedSetSetup(t,p,c,b)
		q:="SELECT p,percentile_cont(p) WITHIN GROUP(ORDER BY x) AS c,percentile_disc(p) WITHIN GROUP(ORDER BY x) AS d FROM os_input WHERE p IS NOT NULL GROUP BY p ORDER BY p"
		orderedSetBoth(t,p,c,b,fixture.Select(q,[]string{"p","c","d"},[]string{"FLOAT8","FLOAT8","INT4"},[][]any{{float64(.25),float64(12.5),int64(10)},{float64(.75),float64(37.5),int64(40)}}))
		orderedSetBoth(t,p,c,b,orderedSetValue("SELECT percentile_cont(p+0) WITHIN GROUP(ORDER BY x) AS v FROM os_input WHERE p=.25 GROUP BY p+0","FLOAT8",float64(12.5)))
		orderedSetBoth(t,p,c,b,orderedSetValue("SELECT percentile_cont(p) WITHIN GROUP(ORDER BY x) AS v FROM os_input WHERE p IS NULL GROUP BY p","FLOAT8",nil))
		for _, tail := range []string{""," WHERE FALSE"," HAVING FALSE"} {
			fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE SEQUENCE os_seq",0))
			s:=orderedSetValue("SELECT percentile_cont(nextval('os_seq')::float8/10) WITHIN GROUP(ORDER BY x) AS v FROM os_input"+tail,"FLOAT8",float64(13))
			if tail==" WHERE FALSE" {s.Rows=[][]any{{nil}}};if tail==" HAVING FALSE" {s.Rows=[][]any{}}
			fixture.RunStep(t,p.Ctx,c,b.Recorder,s)
			fixture.RunStep(t,p.Ctx,c,b.Recorder,orderedSetValue("SELECT nextval('os_seq') AS v","INT8",int64(2)))
			fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("DROP SEQUENCE os_seq",0))
		}
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE SEQUENCE os_empty_seq",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,orderedSetValue("SELECT percentile_cont(nextval('os_empty_seq')::float8/10) WITHIN GROUP(ORDER BY x) FILTER(WHERE FALSE) AS v FROM os_input","FLOAT8",nil))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,orderedSetValue("SELECT nextval('os_empty_seq') AS v","INT8",int64(2)))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT percentile_cont(nextval('os_empty_seq')::float8/10) WITHIN GROUP(ORDER BY x) AS v FROM os_input WHERE FALSE GROUP BY p",[]string{"v"},[]string{"FLOAT8"},[][]any{}))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,orderedSetValue("SELECT nextval('os_empty_seq') AS v","INT8",int64(3)))
		// Actual admitted input errors precede terminal direct decisions. The
		// row-dependent denominator prevents a constant-folding substitute.
		for _,family:=range []string{"percentile_cont","percentile_disc"} {
			for _,array:=range []bool{false,true} {
				typ:="FLOAT8";if family=="percentile_disc" {typ="INT4"}
				fractions:=[]string{"NULL::float8","1.1::float8"}
				if array {fractions=[]string{"NULL::float8[]","'{1.1}'::float8[]"};typ="_"+typ}
				for i,fraction:=range fractions {
					call:=family+"("+fraction+") WITHIN GROUP(ORDER BY 1/(x-x))"
					orderedSetRuntimeError(t,p,c,b,"SELECT "+call+" AS v FROM os_input","22012","division by zero")
					for _,filter:=range []string{"FALSE","NULL"} {
						q:="SELECT "+call+" FILTER(WHERE "+filter+") AS v FROM os_input"
						if i==1&&!array {orderedSetRuntimeError(t,p,c,b,q,"22003","percentile value 1.1 is not between 0 and 1")} else {orderedSetBoth(t,p,c,b,orderedSetValue(q,typ,nil))}
					}
				}
			}
		}
		orderedSetRuntimeError(t,p,c,b,"SELECT percentile_cont(1/(x-x)) WITHIN GROUP(ORDER BY x) FROM os_input GROUP BY x","22012","division by zero")
		q="SELECT percentile_cont(1/$1::float8) WITHIN GROUP(ORDER BY x) FROM os_input WHERE FALSE"
		stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer stmt.Close()
		s:=orderedSetDiagnostic(q,"22012","division by zero","","");s.Args=[]any{float64(0)};s.ErrorStage="execute"
		fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q},b.Recorder,s)
	})
}

func TestReferenceOrderedSetAggregateParameters(t *testing.T) {
	orderedSetScenario(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend) {
		orderedSetSetup(t,p,c,b)
		for _, family:=range []string{"cont","disc"} {
			typ:="FLOAT8";if family=="disc" {typ="INT4"}
			q:="SELECT percentile_"+family+"(NULL) WITHIN GROUP(ORDER BY x) AS v FROM os_input"
			orderedSetBoth(t,p,c,b,orderedSetValue(q,typ,nil))
			q="SELECT percentile_"+family+"($1) WITHIN GROUP(ORDER BY x) AS v FROM os_input"
			stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)}
			held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
			for i,arg:=range []any{float64(.25),nil,float64(.75)} {
				wants:=[]any{float64(17.5),nil,float64(32.5)}
				if family=="disc" {wants=[]any{int64(10),nil,int64(30)}}
				s:=orderedSetValue(q,typ,wants[i]);s.Args=[]any{arg}
				fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
			}
			s:=orderedSetDiagnostic(q,"22003","percentile value 1.1 is not between 0 and 1","","")
			s.Args=[]any{float64(1.1)};s.ErrorStage="execute"
			fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
			s=orderedSetValue(q,typ,nil);s.Args=[]any{nil}
			fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
			stmt.Close()
		}
		for _, tc:= range []struct{cast,typ string;args,wants []any} {
			{"float8","FLOAT8",[]any{float64(.25),float64(.75),nil,float64(.5)},[]any{float64(17.5),float64(32.5),nil,float64(25)}},
			{"float8[]","_FLOAT8",[]any{"{.25,.75}","[-1:0]={1,0}",nil,"{}"},[]any{"{17.5,32.5}","[-1:0]={40,10}",nil,"{}"}},
		} {
			q:="SELECT percentile_cont($1::"+tc.cast+") WITHIN GROUP(ORDER BY x) AS v FROM os_input"
			stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)}
			held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
			for i,arg:=range tc.args {
				s:=orderedSetValue(q,tc.typ,tc.wants[i]);s.Args=[]any{arg}
				fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
			}
			s:=orderedSetDiagnostic(q,"22003","percentile value 1.1 is not between 0 and 1","","")
			arg:=any(float64(1.1));if tc.cast=="float8[]" {arg="{1.1}"};s.Args=[]any{arg};s.ErrorStage="execute"
			fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
			s=orderedSetValue(q,tc.typ,tc.wants[0]);s.Args=[]any{tc.args[0]};fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
			stmt.Close()
		}
	})
}

func TestReferenceOrderedSetAggregateLifecycle(t *testing.T) {
	orderedSetScenario(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend) {
		orderedSetSetup(t,p,c,b)
		q:="SELECT percentile_cont($1::float8) WITHIN GROUP(ORDER BY x) AS v FROM os_input"
		stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer stmt.Close()
		rows,err:=stmt.QueryContext(p.Ctx,.5);if err!=nil {t.Fatal(err)};defer rows.Close()
		// PostgreSQL rows stream on their physical connection. Keep its old
		// snapshot while mutating through a separate session; native rows are
		// eager and explicitly allow another command on the same owner.
		writer:=c
		if b.PG {
			peer,err:=b.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)}
			defer peer.Close()
			writer=peer
		}
		fixture.RunStep(t,p.Ctx,writer,b.Recorder,fixture.Exec("INSERT INTO os_input VALUES(50,.5)",1))
		checkOld:=func(){
			if !rows.Next() {t.Fatal("lost held rows",rows.Err())};var old float64;if err:=rows.Scan(&old);err!=nil||old!=25 {t.Fatalf("held snapshot %v %v",old,err)}
			if rows.Next()||rows.Err()!=nil {t.Fatal("unexpected held suffix",rows.Err())}
			if err:=rows.Close();err!=nil {t.Fatal(err)}
		}
		if b.PG {checkOld()}
		s:=orderedSetValue(q,"FLOAT8",float64(30));s.Args=[]any{float64(.5)}
		fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q},b.Recorder,s)
		if !b.PG {checkOld()}
	})
}

func TestReferenceOrderedSetAggregateCancellation(t *testing.T) {
	orderedSetScenario(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend) {
		orderedSetSetup(t,p,c,b)
		q:="SELECT percentile_cont(.5) WITHIN GROUP(ORDER BY x) AS v FROM os_input"
		stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer stmt.Close()
		for _, prepared:=range []bool{false,true} {
			if b.PG {orderedSetCancelAdmitted(t,p,b,c,stmt,q,prepared)} else {
				ctx,cancel:=context.WithCancel(p.Ctx);cancel()
				var rows *sql.Rows
				if prepared {rows,err=stmt.QueryContext(ctx)} else {rows,err=c.QueryContext(ctx,q)}
				if rows!=nil {rows.Close();t.Fatal("canceled query published rows")};fixture.LexicalOrdinary(t,err,context.Canceled)
			}
			fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q},b.Recorder,orderedSetValue(q,"FLOAT8",float64(25)))
		}
	})
}
