package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBitwiseAggregateGrammar(t *testing.T) {
	for _, name:=range []string{"bit_and","bit_or","bit_xor"} {
		for _, prefix:=range []string{"","pg_catalog.","\"bw_schema\"."} {
			for _, tc:=range []struct{ suffix string; args,keys int; star,distinct,filter,window bool }{
				{"()",0,0,false,false,false,false},
				{"(*)",0,0,true,false,false,false},
				{"(i)",1,0,false,false,false,false},
				{"(i,1)",2,0,false,false,false,false},
				{"(DISTINCT i ORDER BY i) FILTER(WHERE flag)",1,1,false,true,true,false},
				{"(ALL i ORDER BY k DESC NULLS LAST)",1,1,false,false,false,false},
				{"(i) FILTER(WHERE flag) OVER ()",1,0,false,false,true,true},
				{"(bit_or(i))",1,0,false,false,false,false},
			} {
				q:="SELECT /*é*/ "+prefix+name+tc.suffix+" FROM t"
				statements,err:=Parse(q)
				if err!=nil || len(statements)!=1 { t.Fatal(q,err) }
				call,ok:=statements[0].(*Select).Targets[0].(AggregateExpr)
				if !ok || call.Name!=name || len(call.Args)!=tc.args || len(call.Order)!=tc.keys || call.Star!=tc.star || call.Distinct!=tc.distinct || (call.Filter!=nil)!=tc.filter || (call.Window!=nil)!=tc.window {
					t.Fatal("aggregate provenance lost",q,call)
				}
				if call.Pos.Character!=14 { t.Fatal("Unicode call position",call.Pos) }
				if prefix!="" && call.Schema==nil { t.Fatal("qualification lost",call) }
			}
		}
		statements,err:=Parse("SELECT \""+name+"\"(7)")
		if err!=nil || len(statements)!=1 || statements[0].(*Select).From!=nil { t.Fatal("quoted no-FROM aggregate",err) }
		if call,ok:=statements[0].(*Select).Targets[0].(AggregateExpr); !ok || call.Name!=name { t.Fatal("quoted aggregate lost",statements[0]) }
		for _, q:=range []string{"SELECT "+name+"(DISTINCT *) FROM t","SELECT "+name+"(i) FROM t unexpected suffix"} {
			statements,err:=Parse(q)
			if err==nil || len(statements)!=0 { t.Fatal("whole-input/star restriction lost",q,statements,err) }
		}
	}
}

func TestBitwiseAggregateConflictAndGenerationProvenance(t *testing.T) {
	for _, name:=range []string{"bit_and","bit_or","bit_xor"} {
		for _, prefix:=range []string{"","pg_catalog."} {
			statements,err:=Parse("INSERT INTO t VALUES(1) ON CONFLICT("+prefix+name+"(i)) DO NOTHING")
			if err!=nil || len(statements)!=1 { t.Fatal(err) }
			call,ok:=statements[0].(*Insert).Conflict.Keys[0].Expression.(AggregateExpr)
			if !ok || call.Name!=name || len(call.Args)!=1 || call.Pos.Character==0 || prefix!="" && call.Schema==nil { t.Fatal("conflict aggregate provenance",call) }
		}
		q:="/*é*/ ALTER TABLE bw_gen ALTER g SET EXPRESSION AS ("+name+"(i))"
		statements,err:=Parse(q)
		if err!=nil || len(statements)!=1 { t.Fatal(q,err) }
		raw:=statements[0].(*AlterTable).Actions[0].Generation.Expression
		forbidden,ok:=raw.(ForbiddenGenerationExpr)
		position:=utf8.RuneCountInString(q[:strings.Index(q,name)])+1
		if !ok || forbidden.Code!="42803" || forbidden.Message!="aggregate functions are not allowed in column generation expressions" || forbidden.Pos.Character!=position {
			t.Fatal("deferred generated restriction lost",raw)
		}
	}
}

func TestBitwiseAggregateValuesAdmission(t *testing.T) {
	for _, name:=range []string{"bit_and","bit_or","bit_xor"} {
		for _, call:=range []string{name+"(missing)",name+"(TRUE) FILTER(WHERE 1)","CAST("+name+"(missing) AS integer)"} {
			statements,err:=Parse("INSERT INTO t VALUES("+call+")")
			if err!=nil || len(statements)!=1 || len(statements[0].(*Insert).Rows)!=1 { t.Fatal("provider analysis must own VALUES child diagnostics",call,err) }
		}
	}
	for _, call:=range []string{"missing","count(missing)"} {
		_,err:=Parse("INSERT INTO t VALUES("+call+")")
		diagnostic,ok:=err.(interface{ SQLState() string })
		if !ok || diagnostic.SQLState()!="0A000" { t.Fatal("unrelated scalar prewalk changed",call,err) }
	}
}
