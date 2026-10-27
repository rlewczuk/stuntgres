package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

var relationSizeCalls = []string{
	"pg_relation_size('rs_values')", "pg_relation_size('rs_values','main')",
	"pg_table_size('rs_values')", "pg_indexes_size('rs_values')", "pg_total_relation_size('rs_values')",
	"pg_database_size(current_database()::name)", "pg_database_size((SELECT oid FROM pg_database WHERE datname=current_database()))",
	"pg_tablespace_size('pg_default'::name)", "pg_tablespace_size(1663::oid)",
}

func relationSizeRun(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
}

func relationSizeSelect(q string, rows [][]any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"v"}, []string{"INT8"}, rows, args...)
}

func relationSizeFixture(t *testing.T, p *fixture.Pair, b fixture.Backend, name string) {
	t.Helper()
	relationSizeRun(t,p,b,fixture.Exec("CREATE TABLE "+name+"(i INT,s TEXT,b BOOL)",0))
	relationSizeRun(t,p,b,fixture.Exec("INSERT INTO "+name+" VALUES(1,'x',TRUE),(2,NULL,FALSE)",2))
	relationSizeRun(t,p,b,fixture.Exec("CREATE INDEX "+name+"_idx ON "+name+"(i)",0))
}

func TestReferenceRelationSizeValues(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() { t.Run(b.Name,func(t *testing.T) {
		relationSizeFixture(t,p,b,"rs_values")
		for _, prepared := range []bool{false,true} {
			for _, call := range relationSizeCalls {
				for _, prefix := range []string{"", "pg_catalog."} {
					s := fixture.Select("SELECT "+prefix+call+">=0 AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}})
					s.Prepared = prepared
					relationSizeRun(t,p,b,s)
				}
			}
			for _, call := range []string{
				"pg_database_size(NULL::name)","pg_database_size(NULL::oid)","pg_database_size(NULL)",
				"pg_tablespace_size(NULL::name)","pg_tablespace_size(NULL::oid)","pg_tablespace_size(NULL)",
				"pg_relation_size(NULL::regclass)","pg_relation_size(NULL::regclass,NULL::text)",
				"pg_table_size(NULL::regclass)","pg_indexes_size(NULL::regclass)","pg_total_relation_size(NULL::regclass)",
			} {
				s := relationSizeSelect("SELECT "+call+" AS v",[][]any{{nil}})
				s.Prepared = prepared
				relationSizeRun(t,p,b,s)
			}
		}
		for _, name := range []string{"pg_database_size","pg_tablespace_size","pg_relation_size","pg_table_size","pg_indexes_size","pg_total_relation_size"} {
			q := "SELECT "+name+"($1) AS v"
			stmt,err := b.DB.PrepareContext(p.Ctx,q)
			if err != nil { t.Fatal(err) }
			held := fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:q}
			fixture.RunStep(t,p.Ctx,held,b.Recorder,relationSizeSelect(q,[][]any{{nil}},nil))
			stmt.Close()
		}
		for _, arg := range []string{"'rs_values'::text","'rs_values'::varchar","'rs_values'::regclass","('rs_values'::regclass)::oid","('rs_values'::regclass)::integer"} {
			relationSizeRun(t,p,b,fixture.Prepared(fixture.Select("SELECT pg_relation_size("+arg+")=pg_relation_size('rs_values') AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}})))
		}
		for _, arg := range []string{"current_database()::text","current_database()::varchar","current_database()::name"} {
			relationSizeRun(t,p,b,fixture.Select("SELECT pg_database_size("+arg+")>=0 AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}}))
		}
		for _, tc := range []struct{oid int; name string; nargs int}{
			{2168,"pg_database_size",1},{2286,"pg_total_relation_size",1},{2322,"pg_tablespace_size",1},{2323,"pg_tablespace_size",1},{2324,"pg_database_size",1},{2325,"pg_relation_size",1},{2332,"pg_relation_size",2},{2997,"pg_table_size",1},{2998,"pg_indexes_size",1},
		} {
			relationSizeRun(t,p,b,fixture.Select(fmt.Sprintf("SELECT proname,pronargs,prorettype::bigint AS result,proisstrict,provolatile,proparallel FROM pg_proc WHERE oid=%d",tc.oid),
				[]string{"proname","pronargs","result","proisstrict","provolatile","proparallel"},[]string{"NAME","INT2","INT8","BOOL","CHAR","CHAR"},[][]any{{tc.name,int64(tc.nargs),int64(20),true,"v","s"}}))
			formals:="2205"
			switch tc.oid {case 2168,2323:formals="19";case 2322,2324:formals="26";case 2332:formals="2205 25"}
			relationSizeRun(t,p,b,fixture.Select(fmt.Sprintf("SELECT proargtypes::text AS args,proretset,prosecdef,proargnames,pronargdefaults,provariadic::bigint AS variadic,prosqlbody IS NOT NULL AS has_body FROM pg_proc WHERE oid=%d",tc.oid),
				[]string{"args","proretset","prosecdef","proargnames","pronargdefaults","variadic","has_body"},[]string{"TEXT","BOOL","BOOL","_TEXT","INT2","INT8","BOOL"},[][]any{{formals,false,false,nil,int64(0),int64(0),tc.oid==2325}}))
		}
	}) }
}

func TestReferenceRelationSizeMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() { t.Run(b.Name,func(t *testing.T) {
		relationSizeFixture(t,p,b,"rs_values")
		for _, prepared := range []bool{false,true} {
			for _, call := range append(append([]string{},relationSizeCalls...),"pg_relation_size(0::oid)") {
				for _, empty := range []bool{false,true} {
					q := "SELECT "+call+" AS v"
					if empty { q += " WHERE FALSE" }
					var stmt *sql.Stmt
					var rows *sql.Rows
					var err error
					if prepared {
						stmt,err = b.DB.PrepareContext(p.Ctx,q)
						if err == nil { rows,err = stmt.QueryContext(p.Ctx) }
					} else { rows,err = b.DB.QueryContext(p.Ctx,q) }
					if err != nil { t.Fatal(q,err) }
					columns,err := rows.ColumnTypes()
					if err != nil || len(columns)!=1 || columns[0].Name()!="v" || columns[0].DatabaseTypeName()!="INT8" { t.Fatal(q,columns,err) }
					if rows.Next() {
						if empty { t.Fatal("empty result returned row") }
						var raw any
						if err := rows.Scan(&raw); err != nil { t.Fatal(err) }
						if call=="pg_relation_size(0::oid)" { if raw!=nil { t.Fatal(raw) } } else if n,ok := raw.(int64); !ok || n<0 { t.Fatalf("raw bigint: %T %v",raw,raw) }
					} else if !empty { t.Fatal("missing scalar row",rows.Err()) }
					if err := rows.Err(); err != nil { t.Fatal(err) }
					rows.Close()
					if stmt!=nil { stmt.Close() }
				}
			}
		}
		relationSizeRun(t,p,b,fixture.Select("SELECT pg_relation_size(0::regclass)",[]string{"pg_relation_size"},[]string{"INT8"},[][]any{{nil}}))
	}) }
}
