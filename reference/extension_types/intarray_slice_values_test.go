package reference_test

import (
	"database/sql"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIntarraySliceValues(t *testing.T) {
	intarrayScenario(t, func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		intarrayInstall(t,p,b,c)
		for _,tc := range intarraySliceCases {
			t.Run(tc.name+strconv.Itoa(len(tc.args)),func(t *testing.T) {
				literal := tc.expression(tc.literals(tc.args))
				intarrayBoth(t,p,b,c,intarraySliceResult(b,"SELECT "+literal+" AS v",tc.family,tc.want))
				// A scalar FROM source returns exactly one row, including NULL.
				if tc.name != "prefix" && tc.name != "binary" {
					intarrayBoth(t,p,b,c,intarraySliceResult(b,"SELECT v FROM "+literal+" AS result(v)",tc.family,tc.want))
					nulls := append([]any(nil),tc.args...); nulls[0] = nil
					intarrayBoth(t,p,b,c,intarraySliceResult(b,"SELECT v FROM "+tc.expression(tc.literals(nulls))+" AS result(v)",tc.family,nil))
				}
				for _,typed := range []bool{false,true} {
					q := "SELECT "+tc.expression(tc.parameters(typed))+" AS v"
					intarrayBoth(t,p,b,c,intarraySliceResult(b,q,tc.family,tc.want,tc.args...))
					stmt,err := c.PrepareContext(p.Ctx,q); if err != nil { t.Fatal(err) }; defer stmt.Close()
					held := fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
					for _,bytes := range []bool{false,true} {
						args := append([]any(nil),tc.args...)
						if bytes && tc.types[0] == "integer[]" { args[0] = []byte(intarraySliceA) }
						fixture.RunStep(t,p.Ctx,held,b.Recorder,intarraySliceResult(b,q,tc.family,tc.want,args...))
					}
					nulls := append([]any(nil),tc.args...); nulls[0] = nil
					fixture.RunStep(t,p.Ctx,held,b.Recorder,intarraySliceResult(b,q,tc.family,nil,nulls...))
					if tc.types[0] == "integer[]" {
						nulls[0] = []byte(nil)
						fixture.RunStep(t,p.Ctx,held,b.Recorder,intarraySliceResult(b,q,tc.family,nil,nulls...))
					}
					if tc.name == "idx" || tc.name == "binary" || tc.name == "subarray" {
						bad := append([]any(nil),tc.args...); bad[0] = "{22,NULL}"
						s := fixture.ErrorDiagnostic(q,intarraySetNullError(),bad...); s.SelectRows,s.ErrorStage = true,"execute"
						fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
					} else {
						input := "x"
						if tc.types[0] == "integer[]" { input = "{x}" }
						want := fixture.ControlsReferenceDiagnostic("22P02",`invalid input syntax for type integer: "x"`)
						want.Context = "unnamed portal parameter $1 = '...'"
						s := fixture.ErrorDiagnostic(q,want,input); s.SelectRows,s.ErrorStage = true,"execute"
						fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
					}
					changed := append([]any(nil),tc.args...)
					if tc.name == "intset" { changed[0] = int64(9) } else { changed[0] = []byte("{5}") }
					fixture.RunStep(t,p.Ctx,held,b.Recorder,intarraySliceResult(b,q,tc.family,tc.changed,changed...))
					if err := stmt.Close(); err != nil { t.Fatal(err) }
				}
			})
		}
		for _,s := range []fixture.Step{
			fixture.Exec("CREATE DOMAIN ia_slice_integer AS integer",0),
			fixture.Exec("CREATE DOMAIN ia_slice_array AS integer[]",0),
			fixture.Exec("SET search_path=ia_provider,public",0),
		} { fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
		for _,s := range []fixture.Step{
			intarraySliceResult(b,"SELECT icount('{11,22,33,22}') AS v","INT4",int64(4)),
			intarraySliceResult(b,"SELECT idx('{11,22,33,22}',22::smallint) AS v","INT4",int64(2)),
			intarraySliceResult(b,"SELECT subarray('{11,22,33,22}',2::smallint) AS v","_INT4","{22,33,22}"),
			intarraySliceResult(b,"SELECT subarray('{11,22,33,22}',2::smallint,2::smallint) AS v","_INT4","{22,33}"),
			intarraySliceResult(b,"SELECT intset(7::smallint) AS v","_INT4","{7}"),
			intarraySliceResult(b,"SELECT # '{11,22,33,22}'::integer[] AS v","INT4",int64(4)),
			intarraySliceResult(b,"SELECT '{11,22,33,22}'::integer[] # 22 AS v","INT4",int64(2)),
			intarraySliceResult(b,"SELECT 5 # 3 AS v","INT4",int64(6)),
		} { intarrayBoth(t,p,b,c,s) }
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("SET search_path=public",0))
		for _,tc := range intarraySliceCases {
			args := tc.literals(tc.args)
			for i,typ := range tc.types {
				domain := "ia_slice_integer"; if typ == "integer[]" { domain = "ia_slice_array" }
				args[i] = "("+args[i]+")::"+domain
			}
			intarrayBoth(t,p,b,c,intarraySliceResult(b,"SELECT "+tc.expression(args)+" AS v",tc.family,tc.want))
		}
		for _,s := range []fixture.Step{
			fixture.Exec("CREATE TABLE ia_slice_inputs(id integer NOT NULL,a integer[])",0),
			fixture.Exec("INSERT INTO ia_slice_inputs VALUES (1,'{11,22,33,22}'),(2,'{}'),(3,NULL),(4,'{NULL,1,NULL}'),(5,'[0:3]={11,22,33,22}'),(6,'{{11,22},{33,22}}')",6),
		} { fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
		counted := intarraySelect(b,"SELECT id,ia_provider.icount(a) AS n,OPERATOR(ia_provider.#) a AS counted FROM ia_slice_inputs ORDER BY id",[]string{"id","n","counted"},[]string{"INT4","INT4","INT4"},nil,[][]any{{int64(1),int64(4),int64(4)},{int64(2),int64(0),int64(0)},{int64(3),nil,nil},{int64(4),int64(3),int64(3)},{int64(5),int64(4),int64(4)},{int64(6),int64(4),int64(4)}})
		counted.CheckMetadata = func(t *testing.T,rows *sql.Rows) {
			columns,err := rows.ColumnTypes()
			if err != nil {t.Fatal(err)}
			if len(columns) != 3 {t.Fatalf("column count %d want 3",len(columns))}
			// The stored id retains NOT NULL; computed columns have unknown
			// nullability. PostgreSQL transport reports unknown for all three.
			intarrayMetadata(t,b.PG,columns[:1],[]string{"id"},[]string{"INT4"},[]bool{false})
			intarrayMetadata(t,b.PG,columns[1:],[]string{"n","counted"},[]string{"INT4","INT4"},nil)
		}
		intarrayBoth(t,p,b,c,counted)
		for _,tc := range intarraySliceCases {
			args := tc.literals(tc.args); args[0] = "a"; if tc.name == "intset" { args[0] = "id" }
			want := []any{tc.want,nil,nil,tc.want,tc.want}
			switch tc.name {
			case "icount","prefix": want = []any{int64(4),int64(0),nil,int64(4),int64(4)}
			case "idx","binary": want = []any{int64(2),int64(0),nil,int64(2),int64(2)}
			case "subarray": want = []any{tc.want,"{}",nil,tc.want,tc.want}
			case "intset": want = []any{"{1}","{2}","{3}","{5}","{6}"}
			}
			rows := make([][]any,len(want)); for i,v := range want { rows[i] = []any{v} }
			q := "SELECT "+tc.expression(args)+" AS v FROM ia_slice_inputs WHERE id<>4 ORDER BY id"
			intarrayBoth(t,p,b,c,intarraySelect(b,q,[]string{"v"},[]string{tc.family},nil,rows))
			q = "SELECT "+tc.expression(args)+" AS v FROM ia_slice_inputs WHERE id=99"
			intarrayBoth(t,p,b,c,intarraySelect(b,q,[]string{"v"},[]string{tc.family},nil,[][]any{}))
		}
		intarrayBoth(t,p,b,c,intarraySelect(b,"SELECT id,a FROM ia_slice_inputs WHERE id=3",[]string{"id","a"},[]string{"INT4","_INT4"},[]bool{false,true},[][]any{{int64(3),nil}}))
		intarrayError(t,p,b,c,"SELECT ia_provider.idx(a,22) FROM ia_slice_inputs WHERE id=4",false,"execute",intarraySetNullError())
		intarrayBoth(t,p,b,c,intarraySliceResult(b,"SELECT ia_provider.idx(a,33) AS v FROM ia_slice_inputs WHERE id=6","INT4",int64(3)))
	})
}
