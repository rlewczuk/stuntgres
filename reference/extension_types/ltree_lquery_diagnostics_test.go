package reference_test

import (
	"database/sql"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLtreeLqueryDiagnostics(t *testing.T) {
	cases:=[]struct{input,code,message,detail string}{
		{"a..b","42601","lquery syntax error at character 3",""},
		{"","42601","lquery syntax error","Unexpected end of input."},
		{"a.","42601","lquery syntax error","Unexpected end of input."},
		{"foo@bar","42601","lquery syntax error at character 5",""},
		{"a{}","42601","lquery syntax error at character 3",""},
		{"!","42601","lquery syntax error at character 2","Empty labels are not allowed."},
		{"a{2,1}","42601","lquery syntax error","Low limit (2) is greater than high limit (1), at character 5."},
		{"a{65536}","54000","lquery syntax error","Low limit (65536) exceeds the maximum allowed (65535), at character 3."},
		{"a b","42601","lquery syntax error at character 2",""},
		{"(a)","42601","lquery syntax error at character 1",""},
		{"a&b","42601","lquery syntax error at character 2",""},
	}
	for i,tc:=range cases {t.Run(strconv.Itoa(i),func(t *testing.T){
		ltreeLqueryScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
			ltreeInstall(t,p,b,c)
			for _,prepared:=range []bool{false,true}{
				lit:=queryIntLiteral(tc.input)
				for _,q:=range []string{"/*é*/ SELECT NULL::ltree ~ "+lit+"::lquery","/*é*/ SELECT "+lit+"::lquery ~ NULL::ltree"}{
					want:=fixture.ControlsReferenceDiagnostic(tc.code,tc.message);want.Detail=tc.detail;want.Position=lquerySQLPosition(q,lit)
					ltreeError(t,p,b,c,q,prepared,true,true,want)
				}
				for _,route:=range []struct{q string;arg int;args []any}{
					{"SELECT NULL::ltree ~ $1::lquery",1,[]any{tc.input}},
					{"SELECT $1::lquery ~ NULL::ltree",1,[]any{[]byte(tc.input)}},
					{"SELECT $1::ltree ~ $2::lquery",2,[]any{nil,tc.input}},
				}{want:=queryIntParameterDiagnostic(tc.code,tc.message,tc.input,route.arg);want.Detail=tc.detail;ltreeError(t,p,b,c,route.q,prepared,true,false,want,route.args...)}
			}
		})
	})}
	t.Run("ltree_and_mismatch",func(t *testing.T){ltreeLqueryScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		ltreeInstall(t,p,b,c)
		for _,prepared:=range []bool{false,true}{
			for _,q:=range []string{"/*é*/ SELECT 'a..b'::ltree ~ NULL::lquery","/*é*/ SELECT NULL::lquery ~ 'a..b'::ltree"}{
				want:=fixture.ControlsReferenceDiagnostic("42601","ltree syntax error at character 3");want.Position=lquerySQLPosition(q,"'a..b'");ltreeError(t,p,b,c,q,prepared,true,true,want)
			}
			for _,q:=range []string{"SELECT $1::ltree ~ NULL::lquery","SELECT NULL::lquery ~ $1::ltree"}{ltreeError(t,p,b,c,q,prepared,true,false,queryIntParameterDiagnostic("42601","ltree syntax error at character 3","a..b",1),"a..b")}
			for _,tc:=range []struct{q,signature string}{
				{"SELECT 'a'::ltree ~ 'a'::text","ltree ~ text"}, {"SELECT 'a'::text ~ 'a'::ltree","text ~ ltree"},
			}{want:=fixture.ControlsReferenceDiagnostic("42883","operator does not exist: "+tc.signature);want.Hint="No operator matches the given name and argument types. You might need to add explicit type casts.";want.Position=lquerySQLPosition(tc.q,"~");ltreeError(t,p,b,c,tc.q,prepared,true,true,want)}
		}
	})})
	t.Run("input_rejection_and_runtime_atomicity",func(t *testing.T){ltreeLqueryScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		ltreeInstall(t,p,b,c)
		run:=func(s fixture.Step){ltreeLqueryRun(t,p,b,c,s)}
		run(fixture.Exec("CREATE TABLE lq_atomic(id integer NOT NULL,v boolean)",0));run(fixture.Exec("INSERT INTO lq_atomic VALUES (0,false)",1))
		seed:=func(){run(fixture.Select("SELECT id,v FROM lq_atomic ORDER BY id",[]string{"id","v"},[]string{"INT4","BOOL"},[][]any{{int64(0),false}}))}
		// Typed parameters reject malformed input before any INSERT row executes.
		q:="INSERT INTO lq_atomic VALUES (1,'a'::ltree ~ $1::lquery),(2,$2::lquery ~ 'b'::ltree)"
		stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close();held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
		s:=fixture.ErrorDiagnostic(q,queryIntParameterDiagnostic("42601","lquery syntax error at character 3","a..b",2),"a","a..b");s.ErrorStage="execute";ltreeLqueryRun(t,p,b,held,s);seed()
		ltreeLqueryRun(t,p,b,held,fixture.Exec(q,2,"a","a"))
		run(fixture.Select("SELECT id,v FROM lq_atomic ORDER BY id",[]string{"id","v"},[]string{"INT4","BOOL"},[][]any{{int64(0),false},{int64(1),true},{int64(2),false}}))
		run(fixture.Exec("DELETE FROM lq_atomic WHERE id=1",1));run(fixture.Exec("DELETE FROM lq_atomic WHERE id=2",1))
		for _,prepared:=range []bool{false,true}{
			literal:="/*é*/ INSERT INTO lq_atomic VALUES (1,'a'::ltree ~ 'a'::lquery),(2,'a..b'::lquery ~ 'a'::ltree)"
			want:=fixture.ControlsReferenceDiagnostic("42601","lquery syntax error at character 3");want.Position=lquerySQLPosition(literal,"'a..b'");ltreeError(t,p,b,c,literal,prepared,false,true,want);seed()
		}
		ltreeLqueryUpdateAtomicity(t,p,b,c,"lq_update_atomic")
		stmt2,err:=c.PrepareContext(p.Ctx,ltreeLqueryHeldSQL);if err!=nil{t.Fatal(err)};defer stmt2.Close();held2:=fixture.HeldStatement{Executor:c,Statement:stmt2,SQL:ltreeLqueryHeldSQL}
		s=fixture.ErrorDiagnostic(ltreeLqueryHeldSQL,fixture.ControlsReferenceDiagnostic("42601","lquery syntax error at character 3"),"a","a..b");s.SelectRows=true;s.ErrorStage="execute";ltreeLqueryRun(t,p,b,held2,s)
		ltreeLqueryHeldChecks(t,p,b,held2)
	})})
}
