package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIntarraySliceDiagnostics(t *testing.T) {
	t.Run("matrix",func(t *testing.T) {
	intarrayScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		intarrayInstall(t,p,b,c)
		for _,tc := range intarraySliceCases {
			q := "SELECT "+tc.expression(tc.parameters(true))+" AS v"
			for i := range tc.args {
				args := append([]any(nil),tc.args...); args[i] = nil
				intarrayBoth(t,p,b,c,intarraySliceResult(b,q,tc.family,nil,args...))
				intarrayBoth(t,p,b,c,intarraySliceResult(b,"SELECT "+tc.expression(tc.literals(args))+" AS v",tc.family,nil))
				if i > 0 && tc.types[0] == "integer[]" {
					args[0] = "{NULL}"
					intarrayBoth(t,p,b,c,intarraySliceResult(b,q,tc.family,nil,args...))
					intarrayBoth(t,p,b,c,intarraySliceResult(b,"SELECT "+tc.expression(tc.literals(args))+" AS v",tc.family,nil))
				}
			}
			if tc.name == "idx" || tc.name == "binary" || tc.name == "subarray" {
				for _,array := range []string{"{NULL}","{22,NULL}","{1,NULL,22}"} {
					args := append([]any(nil),tc.args...); args[0] = array
					for _,prepared := range []bool{false,true} {
						intarrayError(t,p,b,c,q,prepared,"execute",intarraySetNullError(),args...)
						intarrayError(t,p,b,c,"SELECT "+tc.expression(tc.literals(args))+" AS v",prepared,"execute",intarraySetNullError())
					}
				}
			}
			// Every selected formal has both literal and bound input conversion
			// witnesses. STRICT does not hide a malformed demanded argument.
			for i,typ := range tc.types {
				for _,bad := range []struct{ value,code,message string }{
					{"x","22P02",`invalid input syntax for type integer: "x"`},
					{"2147483648","22003",`value "2147483648" is out of range for type integer`},
				} {
					value := bad.value; if typ == "integer[]" { value = "{"+value+"}" }
					args := append([]any(nil),tc.args...); args[i] = value
					literals := tc.literals(tc.args); literals[i] = "'"+value+"'::"+typ
					literal := "SELECT "+tc.expression(literals)+" AS v"
					for _,prepared := range []bool{false,true} {
						want := fixture.ControlsReferenceDiagnostic(bad.code,bad.message)
						want.Position = strconv.Itoa(strings.Index(literal,"'"+value+"'")+1)
						stage := "execute"; if prepared { stage = "prepare" }
						intarrayError(t,p,b,c,literal,prepared,stage,want)
						want.Position,want.Context = "","unnamed portal parameter $"+strconv.Itoa(i+1)+" = '...'"
						intarrayError(t,p,b,c,q,prepared,"execute",want,args...)
						if i > 0 {
							args[0] = nil
							intarrayError(t,p,b,c,q,prepared,"execute",want,args...)
						}
					}
				}
			}
		}
		for _,q := range []string{
			"SELECT ia_provider.subarray('{NULL}'::integer[],99)",
			"SELECT ia_provider.subarray('{NULL}'::integer[],99,-99)",
		} { for _,prepared := range []bool{false,true} { intarrayError(t,p,b,c,q,prepared,"execute",intarraySetNullError()) } }
		for _,tc := range []struct{ query,signature string; operator bool }{
			{"SELECT ia_provider.icount(1)","ia_provider.icount(integer)",false},
			{"SELECT ia_provider.icount()","ia_provider.icount()",false},
			{"SELECT ia_provider.idx('{1}'::integer[],'x'::text)","ia_provider.idx(integer[], text)",false},
			{"SELECT ia_provider.subarray('{1}'::integer[],1,2,3)","ia_provider.subarray(integer[], integer, integer, integer)",false},
			{"SELECT ia_provider.intset('x'::text)","ia_provider.intset(text)",false},
			{"SELECT ia_provider.no_such_slice('{1}'::integer[])","ia_provider.no_such_slice(integer[])",false},
			{"SELECT OPERATOR(ia_provider.#) 1","ia_provider.# integer",true},
			{"SELECT '{1}'::integer[] OPERATOR(ia_provider.#) 'x'::text","integer[] ia_provider.# text",true},
			{"SELECT 1 OPERATOR(ia_provider.#) '{1}'::integer[]","integer ia_provider.# integer[]",true},
		} {
			want := fixture.ControlsReferenceDiagnostic("42883","function "+tc.signature+" does not exist")
			want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			want.Position = "8"
			if tc.operator {
				want.Message = "operator does not exist: "+tc.signature
				want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
				if tc.signature == "ia_provider.# integer" {
					want.Hint = "No operator matches the given name and argument type. You might need to add an explicit type cast."
				}
				want.Position = strconv.Itoa(strings.Index(tc.query,"OPERATOR")+1)
			}
			for _,prepared := range []bool{false,true} {
				stage := "execute"; if prepared { stage = "prepare" }
				intarrayError(t,p,b,c,tc.query,prepared,stage,want)
			}
		}
	})
	})
	t.Run("statement_atomicity",func(t *testing.T) {
		intarrayScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			intarraySliceSeed(t,p,b,c)
			intarraySliceAtomicity(t,p,b,c)
		})
	})
}

func intarraySliceAtomicity(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	original := intarraySelect(b,"SELECT id,a FROM ia_slice_rows ORDER BY id",[]string{"id","a"},[]string{"INT4","_INT4"},[]bool{false,true},[][]any{{int64(1),intarraySliceA},{int64(2),"{}"},{int64(3),nil}})
	for _,expr := range []string{
		"ia_provider.subarray((CASE WHEN id=1 THEN $1::text ELSE $2::text END)::integer[],1)",
		"ia_provider.intset(ia_provider.idx((CASE WHEN id=1 THEN $1::text ELSE $2::text END)::integer[],22))",
	} {
		for _,insert := range []bool{false,true} {
			q := "UPDATE ia_slice_rows SET a="+expr+" WHERE id<3"
			if insert { q = "INSERT INTO ia_slice_rows(id,a) SELECT id+10,"+expr+" FROM ia_slice_rows WHERE id<3" }
			stmt,err := c.PrepareContext(p.Ctx,q); if err != nil { t.Fatal(err) }; defer stmt.Close()
			held := fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
			for _,args := range [][]any{{"{NULL}","{22}"},{"{22}","{NULL}"}} {
				for _,prepared := range []bool{false,true} {
					intarrayError(t,p,b,c,q,prepared,"execute",intarraySetNullError(),args...)
					intarrayBoth(t,p,b,c,original)
				}
				s := fixture.ErrorDiagnostic(q,intarraySetNullError(),args...); s.ErrorStage = "execute"
				fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
				intarrayBoth(t,p,b,c,original)
			}
			fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Exec(q,2,"{22}","{33}"))
			first,second := "{22}","{33}"
			if strings.HasPrefix(expr,"ia_provider.intset") { first,second = "{1}","{0}" }
			suffix := " WHERE id<3 ORDER BY id"; id1,id2 := int64(1),int64(2)
			if insert { suffix = " WHERE id>10 ORDER BY id"; id1,id2 = 11,12 }
			intarrayBoth(t,p,b,c,intarraySelect(b,"SELECT id,a FROM ia_slice_rows"+suffix,[]string{"id","a"},[]string{"INT4","_INT4"},[]bool{false,true},[][]any{{id1,first},{id2,second}}))
			reset := fixture.Exec("UPDATE ia_slice_rows SET a=CASE WHEN id=1 THEN '{11,22,33,22}'::integer[] ELSE '{}'::integer[] END WHERE id<3",2)
			if insert { reset = fixture.Exec("DELETE FROM ia_slice_rows WHERE id>10",2) }
			fixture.RunStep(t,p.Ctx,c,b.Recorder,reset)
			if err := stmt.Close(); err != nil { t.Fatal(err) }
			intarrayBoth(t,p,b,c,original)
		}
	}
}
