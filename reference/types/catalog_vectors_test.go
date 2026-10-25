package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceCatalogVectorsAdmission(t *testing.T) {
	catalogVectorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE vectors(id INT,s INT2VECTOR,o OIDVECTOR)", 0))
		run(fixture.Exec("INSERT INTO vectors VALUES(1,'1 2 3','1 2 3'),(2,'',''),(3,NULL,NULL),(4,'-32768 32767','-1 -2147483648')", 4))
		want := [][]any{{int64(1), "1 2 3", "1 2 3"}, {int64(2), "", ""}, {int64(3), nil, nil}, {int64(4), "-32768 32767", "4294967295 2147483648"}}
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct { where string; rows [][]any }{{"", want}, {" WHERE id<0", nil}, {" WHERE id=2", want[1:2]}, {" WHERE id=3", want[2:3]}} {
				s := fixture.Select("SELECT id,s,o FROM vectors"+tc.where+" ORDER BY id", []string{"id", "s", "o"}, []string{"INT4", "INT2VECTOR", "OIDVECTOR"}, tc.rows)
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					cols, err := rows.ColumnTypes(); if err != nil { t.Fatal(err) }
					for _, c := range cols[1:] { nullable, known := c.Nullable(); if b.PG { if known { t.Fatal("pq nullability unexpectedly known") } } else if !known || !nullable { t.Fatal("stored nullable vector descriptor", nullable, known) } }
				}
				catalogVectorRows(t, p, b, b.DB, s)
			}
		}
	})
}

func TestReferenceCatalogVectorsBoundsOrdering(t *testing.T) {
	t.Run("bounds", func(t *testing.T) {
		catalogVectorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			for _, typ := range []string{"int2vector", "oidvector"} {
				for _, tc := range []struct { input string; row []any }{
					{"'1 2 3'", []any{int64(1), "[0:2]", int64(0), int64(2), int64(3), int64(3), int64(1), int64(2), int64(3), nil, nil, nil}},
					{"''", []any{int64(1), "[0:-1]", int64(0), int64(-1), int64(0), int64(0), nil, nil, nil, nil, nil, nil}},
					{"NULL", []any{nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil}},
				} {
					q := "SELECT array_ndims(v) AS n,array_dims(v) AS d,array_lower(v,1) AS l,array_upper(v,1) AS u,array_length(v,1) AS z,cardinality(v) AS c,v[0] AS a,v[1] AS b,v[2] AS e,v[-1] AS x,v[3] AS y,v[NULL] AS k FROM (SELECT "+tc.input+"::"+typ+" AS v) AS t"
					element := "INT2"; if typ == "oidvector" { element = "OID" }
					for _, prepared := range []bool{false, true} {
						s := fixture.Select(q, []string{"n","d","l","u","z","c","a","b","e","x","y","k"}, []string{"INT4","TEXT","INT4","INT4","INT4","INT4",element,element,element,element,element,element}, [][]any{tc.row}); s.Prepared = prepared
						catalogVectorRows(t, p, b, b.DB, s)
					}
				}
				q := "SELECT v[0:1] AS a,v[9:10] AS b,array_lower(v[0:1],1) AS l FROM (SELECT '1 2 3'::"+typ+" AS v) AS t"
				array := "_INT2"; if typ == "oidvector" { array = "_OID" }
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select(q, []string{"a","b","l"}, []string{array,array,"INT4"}, [][]any{{"{1,2}","{}",int64(1)}}))
				q = "SELECT v[0:1] AS a FROM (SELECT ''::"+typ+" AS v) AS t"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select(q, []string{"a"}, []string{array}, [][]any{{"{}"}}))
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT array_ndims('{}'::int2[]) AS n,array_length('{}'::int2[],1) AS l", []string{"n","l"}, []string{"INT4","INT4"}, [][]any{{nil,nil}}))
		})
	})
	t.Run("ordering", func(t *testing.T) {
		catalogVectorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE vectors(id INT,s INT2VECTOR,o OIDVECTOR)",0))
			run(fixture.Exec("INSERT INTO vectors VALUES(1,'99','99'),(2,'1 2','1 2'),(3,'',''),(4,NULL,NULL)",4))
			for _, tc := range []struct { col, dir string; values [][]any }{
				{"s","ASC",[][]any{{""},{"1 2"},{"99"},{nil}}}, {"o","ASC",[][]any{{""},{"99"},{"1 2"},{nil}}},
				{"s","DESC",[][]any{{nil},{"99"},{"1 2"},{""}}}, {"o","DESC",[][]any{{nil},{"1 2"},{"99"},{""}}},
			} { typ := "INT2VECTOR"; if tc.col == "o" { typ = "OIDVECTOR" }; catalogVectorRows(t,p,b,b.DB,fixture.Select("SELECT "+tc.col+" FROM vectors ORDER BY "+tc.col+" "+tc.dir,[]string{tc.col},[]string{typ},tc.values)) }
			for _, typ := range []string{"int2vector","oidvector"} {
				for _, op := range []struct { op string; want bool }{{"=",false},{"<>",true},{"<",true},{">",false},{"<=",true},{">=",false}} {
					q := "SELECT '1 9'::"+typ+op.op+"'2 0'::"+typ+" AS b,NULL::"+typ+op.op+"'1'::"+typ+" AS n"
					run(fixture.Select(q,[]string{"b","n"},[]string{"BOOL","BOOL"},[][]any{{op.want,nil}}))
				}
				q := "SELECT '+01 002'::"+typ+" = '1 2'::"+typ+" AS e,NULL::"+typ+" IS DISTINCT FROM ''::"+typ+" AS d,NULL::"+typ+" IS NOT DISTINCT FROM NULL::"+typ+" AS n"
				run(fixture.Select(q,[]string{"e","d","n"},[]string{"BOOL","BOOL","BOOL"},[][]any{{true,true,true}}))
			}
			run(fixture.Select("SELECT '99'::int2vector > '1 2'::int2vector AS s,'99'::oidvector < '1 2'::oidvector AS o,'-1 9'::int2vector < '0 0'::int2vector AS n,'2147483648'::oidvector < '4294967295'::oidvector AS u",[]string{"s","o","n","u"},[]string{"BOOL","BOOL","BOOL","BOOL"},[][]any{{true,true,true,true}}))
		})
	})
	t.Run("container_boundaries", func(t *testing.T) {
		catalogVectorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			for _, pair := range []struct { vector, element string }{{"int2vector","smallint"},{"oidvector","oid"}} {
				for _, input := range []string{"'{1,2}'", "NULL"} {
					q := "SELECT ("+input+"::"+pair.element+"[])::"+pair.vector
					d := fixture.ControlsReferenceDiagnostic("42846","cannot cast type "+pair.element+"[] to "+pair.vector)
					d.Position = fixture.Position(utf8.RuneCountInString(q[:strings.LastIndex(q,"::")])+1)
					for _, prepared := range []bool{false,true} { s := fixture.ErrorDiagnostic(q,d); s.SelectRows=true; s.Prepared=prepared; s.ErrorStage="execute"; if prepared { s.ErrorStage="prepare" }; fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s) }
				}
				fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("CREATE TABLE assign_"+pair.vector+"(v "+pair.vector+")",0))
				fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("INSERT INTO assign_"+pair.vector+" VALUES('1 2')",1))
				q := "UPDATE assign_"+pair.vector+" SET v[0]=3"
				d := fixture.ControlsReferenceDiagnostic("42846","cannot cast type "+pair.element+"[] to "+pair.vector)
				d.Position=fixture.Position(strings.Index(q,"v[0]")+1)
				fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.ErrorDiagnostic(q,d))
				catalogVectorRows(t,p,b,b.DB,fixture.Select("SELECT v FROM assign_"+pair.vector,[]string{"v"},[]string{strings.ToUpper(pair.vector)},[][]any{{"1 2"}}))
			}
		})
	})
}

func TestReferenceCatalogVectorsArrayCoercion(t *testing.T) {
	catalogVectorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("CREATE TABLE vector_cast_bounds(id INT,s INT2VECTOR,o OIDVECTOR)",0))
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("INSERT INTO vector_cast_bounds VALUES(1,'',''),(2,'-32768 0 32767','1 2147483648 4294967295'),(3,NULL,NULL)",3))
		// REL_18_6 ExecEvalArrayCoerce preserves the received header for binary
		// relabeling, but calls array_map for actual element conversion.
		// array_map's empty-input branch calls construct_empty_array (rank zero).
		// Thus empty conversion has NULL dimensional inquiries, cardinality zero,
		// and is not SQL NULL; canonical-element relabeling retains rank one.
		for _,tc:=range []struct{
			name,column,target,element string
			converts bool
			nonempty []any
		}{
			{"int2_to_int4","s","integer[]","INT4",true,[]any{int64(-32768),int64(0),int64(32767)}},
			{"int2_to_int8","s","bigint[]","INT8",true,[]any{int64(-32768),int64(0),int64(32767)}},
			{"oid_to_int8","o","bigint[]","INT8",true,[]any{int64(1),int64(2147483648),int64(4294967295)}},
			{"int2_binary_lowering","s","smallint[]","INT2",false,[]any{int64(-32768),int64(0),int64(32767)}},
			{"oid_binary_lowering","o","oid[]","OID",false,[]any{int64(1),int64(2147483648),int64(4294967295)}},
		}{
			t.Run(tc.name,func(t *testing.T){
				empty:=[]any{int64(1),int64(1),"[0:-1]",int64(0),int64(-1),int64(0),int64(0),false,nil,nil,nil}
				if tc.converts {empty=[]any{int64(1),nil,nil,nil,nil,nil,int64(0),false,nil,nil,nil}}
				want:=[][]any{
					empty,
					{int64(2),int64(1),"[0:2]",int64(0),int64(2),int64(3),int64(3),false,tc.nonempty[0],tc.nonempty[1],tc.nonempty[2]},
					{int64(3),nil,nil,nil,nil,nil,nil,true,nil,nil,nil},
				}
				columns:=[]string{"id","n","d","l","u","z","c","is_null","a","b","e"}
				types:=[]string{"INT4","INT4","TEXT","INT4","INT4","INT4","INT4","BOOL",tc.element,tc.element,tc.element}
				q:="SELECT id,array_ndims(v) AS n,array_dims(v) AS d,array_lower(v,1) AS l,array_upper(v,1) AS u,array_length(v,1) AS z,cardinality(v) AS c,v IS NULL AS is_null,v[0] AS a,v[1] AS b,v[2] AS e FROM (SELECT id,"+tc.column+"::"+tc.target+" AS v FROM vector_cast_bounds) AS t"
				for _,prepared:=range []bool{false,true}{
					for _,frame:=range []struct{suffix string;rows [][]any}{{" ORDER BY id",want},{" WHERE id<0 ORDER BY id",nil}}{
						s:=fixture.Select(q+frame.suffix,columns,types,frame.rows)
						s.Prepared=prepared
						catalogVectorRows(t,p,b,b.DB,s)
					}
				}
				vector,input:="int2vector","-32768 0 32767"
				if tc.column=="o" {vector,input="oidvector","1 2147483648 4294967295"}
				bound:=q[:strings.Index(q," FROM (")]+" FROM (SELECT $2::integer AS id,($1::"+vector+")::"+tc.target+" AS v) AS t"
				stmt,err:=b.DB.PrepareContext(p.Ctx,bound)
				if err!=nil {t.Fatal(err)}
				defer stmt.Close()
				held:=fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:bound}
				for _,frame:=range []struct{input any;row int}{{"",0},{input,1},{nil,2},{[]byte{},0}}{
					catalogVectorRows(t,p,b,held,fixture.Select(bound,columns,types,[][]any{want[frame.row]},frame.input,want[frame.row][0]))
				}
			})
		}
	})
}
