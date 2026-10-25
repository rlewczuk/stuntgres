package reference_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceCatalogVectorsInputDiagnostics(t *testing.T) {
	// int2vectorin is decimal strtol, unlike scalar int2 input. oidvectorin
	// repeatedly calls uint32in_subr with an end pointer, without separators.
	valid := []struct { typ, input, output string }{
		{"int2vector","1 2 3","1 2 3"}, {"int2vector"," +001 -0002 32767 -32768 ","1 -2 32767 -32768"},
		{"int2vector","",""}, {"int2vector"," \t\n\r\v\f",""}, {"int2vector","\t1  \n2","1 2"},
		{"oidvector","0 2147483648 4294967295","0 2147483648 4294967295"}, {"oidvector","-1 -2147483648","4294967295 2147483648"},
		{"oidvector","010 0x10 +10","8 16 10"}, {"oidvector","1-2","1 4294967294"}, {"oidvector","1+2","1 2"}, {"oidvector","08","0 8"},
		{"oidvector","1\t2\n3","1 2 3"}, {"oidvector","",""}, {"oidvector"," \t\n\r\v\f",""},
		{"oidvector","18446744073709551615","4294967295"}, {"oidvector","18446744071562067968","2147483648"}, {"oidvector","-18446744073709551615","1"},
	}
	for i, tc := range valid {
		t.Run(fmt.Sprintf("valid_%02d_%s",i,tc.typ),func(t *testing.T) {
			catalogVectorScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
				for _, prepared := range []bool{false,true} {
					for _, parameter := range []bool{false,true} {
						q := "SELECT "+catalogVectorQuote(tc.input)+"::"+tc.typ+" AS v"; var args []any
						if parameter { q="SELECT $1::"+tc.typ+" AS v"; args=[]any{tc.input} }
						s := fixture.Select(q,[]string{"v"},[]string{strings.ToUpper(tc.typ)},[][]any{{tc.output}},args...); s.Prepared=prepared
						catalogVectorRows(t,p,b,b.DB,s)
					}
				}
			})
		})
	}
	invalid := []struct { typ, scalar, input, suffix, code string }{
		{"int2vector","smallint","1\t2","1\t2","22P02"}, {"int2vector","smallint","1-2","1-2","22P02"},
		{"int2vector","smallint","1,2","1,2","22P02"}, {"int2vector","smallint","0x10","0x10","22P02"},
		{"int2vector","smallint","1_0","1_0","22P02"}, {"int2vector","smallint","NULL","NULL","22P02"}, {"int2vector","smallint","{1,2}","{1,2}","22P02"},
		{"int2vector","smallint","1 x 2","x 2","22P02"}, {"int2vector","smallint","1 32768 2","32768 2","22003"},
		{"int2vector","smallint","1 -32769 2","-32769 2","22003"}, {"int2vector","smallint","32768x","32768x","22003"},
		{"int2vector","smallint","999999999999999999999999x","999999999999999999999999x","22003"},
		{"int2vector","smallint","\u00a01","\u00a01","22P02"}, {"int2vector","smallint","+","+","22P02"},
		{"oidvector","oid","1x","x","22P02"}, {"oidvector","oid","1,2",",2","22P02"}, {"oidvector","oid","1 NULL","NULL","22P02"},
		{"oidvector","oid","1 4294967296 2","4294967296 2","22003"}, {"oidvector","oid","1 -2147483649 2","-2147483649 2","22003"},
		{"oidvector","oid","18446744071562067967","18446744071562067967","22003"},
		{"oidvector","oid","18446744073709551616x","18446744073709551616x","22003"},
		{"oidvector","oid","-18446744073709551616","-18446744073709551616","22003"},
		{"oidvector","oid","+","+","22P02"}, {"oidvector","oid","x","x","22P02"},
		{"oidvector","oid","{1,2}","{1,2}","22P02"}, {"oidvector","oid","\u00a01","\u00a01","22P02"},
	}
	for i, tc := range invalid {
		t.Run(fmt.Sprintf("invalid_%02d_%s",i,tc.typ),func(t *testing.T) {
			catalogVectorScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
				run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)}
				run(fixture.Exec("CREATE TABLE vectors(id INT,s INT2VECTOR,o OIDVECTOR)",0))
				run(fixture.Exec("INSERT INTO vectors VALUES(99,'1','1')",1))
				unchanged:=func(){catalogVectorRows(t,p,b,b.DB,fixture.Select("SELECT id,s,o FROM vectors ORDER BY id",[]string{"id","s","o"},[]string{"INT4","INT2VECTOR","OIDVECTOR"},[][]any{{int64(99),"1","1"}}))}
				for _, prefix:=range []string{"","/*é*/ "} {
					q:=prefix+"SELECT "+catalogVectorQuote(tc.input)+"::"+tc.typ+" FROM vectors WHERE FALSE"
					d:=catalogVectorDiagnostic(tc.code,tc.scalar,tc.suffix);d.Position=fixture.Position(8+utf8.RuneCountInString(prefix))
					for _, prepared:=range []bool{false,true} { s:=fixture.ErrorDiagnostic(q,d);s.SelectRows=true;s.Prepared=prepared;s.ErrorStage="execute";if prepared{s.ErrorStage="prepare"};run(s);unchanged() }
				}
				for _, arg:=range []any{tc.input,[]byte(tc.input)} {
					for _, prepared:=range []bool{false,true} {
						d:=catalogVectorDiagnostic(tc.code,tc.scalar,tc.suffix);d.Context="unnamed portal parameter $1 = '...'"
						s:=fixture.ErrorDiagnostic("SELECT $1::"+tc.typ,d,arg);s.SelectRows=true;s.Prepared=prepared;s.ErrorStage="execute";run(s);unchanged()
					}
				}
				col:="s";if tc.typ=="oidvector"{col="o"}
				for _, prepared:=range []bool{false,true} {
					q:="INSERT INTO vectors(id,"+col+") VALUES(1,'1'),(2,"+catalogVectorQuote(tc.input)+")"
					d:=catalogVectorDiagnostic(tc.code,tc.scalar,tc.suffix);d.Position=fixture.Position(utf8.RuneCountInString(q[:strings.LastIndex(q,catalogVectorQuote(tc.input))])+1)
					s:=fixture.ErrorDiagnostic(q,d);s.Prepared=prepared;s.ErrorStage="execute";if prepared{s.ErrorStage="prepare"};run(s);unchanged()
					d=catalogVectorDiagnostic(tc.code,tc.scalar,tc.suffix);d.Context="unnamed portal parameter $2 = '...'"
					s=fixture.ErrorDiagnostic("INSERT INTO vectors(id,"+col+") VALUES(1,$1),(2,$2)",d,"1",tc.input);s.Prepared=prepared;s.ErrorStage="execute";run(s);unchanged()
				}
				// The cast depends on the second source row, so evaluation, not
				// literal analysis, discovers the failure after a valid first row.
				q:="INSERT INTO vectors(id,"+col+") SELECT id,v::"+tc.typ+" FROM (VALUES(1,'1'::text),(2,"+catalogVectorQuote(tc.input)+"::text)) AS source(id,v) ORDER BY id"
				for _, prepared:=range []bool{false,true} { s:=fixture.ErrorDiagnostic(q,catalogVectorDiagnostic(tc.code,tc.scalar,tc.suffix));s.Prepared=prepared;s.ErrorStage="execute";run(s);unchanged() }
				run(fixture.Exec("INSERT INTO vectors VALUES(100,'-32768 32767','-1 -2147483648')",1))
				catalogVectorRows(t,p,b,b.DB,fixture.Select("SELECT s,o FROM vectors WHERE id=100",[]string{"s","o"},[]string{"INT2VECTOR","OIDVECTOR"},[][]any{{"-32768 32767","4294967295 2147483648"}}))
			})
		})
	}
	// Explicit character-position witnesses from the living plan.
	t.Run("insert_positions",func(t *testing.T){catalogVectorScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("CREATE TABLE vectors(s INT2VECTOR)",0))
		for _, tc:=range []struct{prefix,pos string}{{"","32"},{"/*é*/ ","38"}} {d:=catalogVectorDiagnostic("22P02","smallint","x 2");d.Position=tc.pos;fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.ErrorDiagnostic(tc.prefix+"INSERT INTO vectors(s) VALUES ('1 x 2')",d))}
	})})
}
