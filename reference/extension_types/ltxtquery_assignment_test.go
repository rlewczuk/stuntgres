package reference_test

import (
	"context"
	"errors"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations are specified from the released lexer/printer, not native output.
var ltxtqueryPositiveFixtures = []struct{ input, output string }{
	{" Europe&Russia*@&!Transportation ", "Europe & Russia@* & !Transportation"},
	{"foo*@%@@**", "foo%@*"}, {"((AbC))", "AbC"},
	{"a|b&c", "a | b & c"}, {"(a|b)&c", "( a | b ) & c"},
	{"a|b|c", "( a | b ) | c"}, {"a|(b|c)", "a | ( b | c )"},
	{"a&(b&c)", "a & b & c"}, {"!!a", "!( !a )"},
	{"!(a|b)", "!( a | b )"}, {"!(a&b)", "!( a & b )"},
	{"\ta &\nb", "a & b"}, {"0", "0"}, {"_", "_"}, {"-", "-"},
	{"AbC", "AbC"}, {"a-b_c", "a-b_c"}, {"a%", "a%"}, {"a@", "a@"}, {"a*", "a*"},
	{"a%@*", "a%@*"}, {"a%*@", "a%@*"}, {"a@%*", "a%@*"},
	{"a@*%", "a%@*"}, {"a*%@", "a%@*"}, {"a*@%", "a%@*"},
	{"!tree & aWdf@*", "!tree & aWdf@*"}, {"tree & aw_qw%*", "tree & aw_qw%*"}, {"tree & aw-qw%*", "tree & aw-qw%*"},
}

func TestReferenceLtxtqueryAssignment(t *testing.T) {
	ltreeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ltreeInstall(t,p,b,c)
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
		run(fixture.Exec("CREATE TABLE lx_assign(id integer NOT NULL,v ltxtquery)",0))
		run(fixture.Exec("INSERT INTO lx_assign VALUES (1,' Europe&Russia*@&!Transportation ')",1))
		q := "INSERT INTO lx_assign VALUES ($1,$2)"
		stmt,err := c.PrepareContext(p.Ctx,q); if err != nil { t.Fatal(err) }; defer stmt.Close()
		held := fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
		for _,args := range [][]any{{2,"foo*@%@@**"},{3,[]byte("((AbC))")},{5,nil}} {
			fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Exec(q,1,args...))
		}
		run(fixture.Exec("INSERT INTO lx_assign VALUES (4,NULL)",1))
		run(ltxtquerySelect(b,"SELECT id,v FROM lx_assign ORDER BY id",[]string{"id","v"},[][]any{{int64(1),"Europe & Russia@* & !Transportation"},{int64(2),"foo%@*"},{int64(3),"AbC"},{int64(4),nil},{int64(5),nil}}))
		for _,prepared := range []bool{false,true} {
			for _,q := range []string{"SELECT 'foo*@%'::ltxtquery AS v","SELECT CAST('foo*@%' AS ltxtquery) AS v","SELECT 'foo*@%'::text::ltxtquery AS v","SELECT ltxtquery 'foo*@%' AS v"} {
				s:=ltxtquerySelect(b,q,[]string{"v"},[][]any{{"foo%@*"}}); s.Prepared=prepared; run(s)
			}
			s:=fixture.Select("SELECT 'foo*@%'::ltxtquery::text AS v",[]string{"v"},[]string{"TEXT"},[][]any{{"foo%@*"}}); s.Prepared=prepared; run(s)
		}
		q="SELECT $1::text::ltxtquery AS v"
		cast,err:=c.PrepareContext(p.Ctx,q); if err!=nil { t.Fatal(err) }; defer cast.Close()
		for _,tc:=range []struct{arg any; want any}{{"foo*@%","foo%@*"},{nil,nil},{[]byte("((AbC))"),"AbC"}} {
			fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:cast,SQL:q},b.Recorder,ltxtquerySelect(b,q,[]string{"v"},[][]any{{tc.want}},tc.arg))
		}
		run(fixture.Exec("UPDATE lx_assign SET v=$1 WHERE id=3",1,"a|b&c"))
		var value string
		if err:=c.QueryRowContext(p.Ctx,"SELECT v FROM lx_assign WHERE id=3").Scan(&value); err!=nil || value!="a | b & c" { t.Fatal(value,err) }
		for _,id:=range []int{3,4} {
			var nullable sql.NullString
			if err:=c.QueryRowContext(p.Ctx,"SELECT v FROM lx_assign WHERE id=$1",id).Scan(&nullable); err!=nil { t.Fatal(err) }
			if nullable.Valid!=(id==3) || id==3 && nullable.String!="a | b & c" { t.Fatal(nullable) }
		}
		run(fixture.Exec("CREATE TABLE lx_required(id integer,v ltxtquery NOT NULL)",0))
		if !b.PG {
			ctx,cancel:=context.WithCancel(p.Ctx);cancel()
			_,err:=c.ExecContext(ctx,"INSERT INTO lx_assign VALUES (99,$1)","a")
			if !errors.Is(err,context.Canceled) {t.Fatal("pre-canceled execution",err)}
			var state interface{SQLState() string};if errors.As(err,&state) {t.Fatal("context error acquired SQLSTATE",err)}
		}
		want:=fixture.ControlsReferenceDiagnostic("23502",`null value in column "v" of relation "lx_required" violates not-null constraint`)
		want.Detail,want.Schema,want.Table,want.Column="Failing row contains (2, null).","public","lx_required","v"
		for _,prepared:=range []bool{false,true} {
			ltreeError(t,p,b,c,"INSERT INTO lx_required VALUES (2,NULL)",prepared,false,false,want)
			for _,arg:=range []any{nil,[]byte(nil)} { ltreeError(t,p,b,c,"INSERT INTO lx_required VALUES (2,$1)",prepared,false,false,want,arg) }
		}
	})
}

func TestReferenceLtxtqueryGrammar(t *testing.T) {
	ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		ltreeInstall(t,p,b,c)
		q:="SELECT $1::ltxtquery AS v"
		stmt,err:=c.PrepareContext(p.Ctx,q); if err!=nil { t.Fatal(err) }; defer stmt.Close()
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE lx_grammar(id integer,v ltxtquery)",0))
		var expected [][]any
		for id,tc:=range ltxtqueryPositiveFixtures {
			for _,input:=range []string{tc.input,tc.output} {
				fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q},b.Recorder,ltxtquerySelect(b,q,[]string{"v"},[][]any{{tc.output}},input))
			}
			fixture.RunStep(t,p.Ctx,c,b.Recorder,ltxtquerySelect(b,"SELECT '"+tc.input+"'::ltxtquery AS v",[]string{"v"},[][]any{{tc.output}}))
			fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO lx_grammar VALUES ($1,$2)",1,id,tc.input))
			expected=append(expected,[]any{int64(id),tc.output})
		}
		fixture.RunStep(t,p.Ctx,c,b.Recorder,ltxtquerySelect(b,"SELECT id,v FROM lx_grammar ORDER BY id",[]string{"id","v"},expected))
	})
}
