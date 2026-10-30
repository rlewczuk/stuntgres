package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

type foreignAnalyzeDB interface {
	QueryContext(context.Context,string,...any)(*sql.Rows,error)
	PrepareContext(context.Context,string)(*sql.Stmt,error)
}

var foreignAnalyzeActual=regexp.MustCompile(`actual (?:time=[^)]*? )?rows=([0-9.]+) loops=([0-9]+)`)

func foreignAnalyzePlan(t *testing.T,p *fixture.Pair,db foreignAnalyzeDB,query string,prepared bool,want float64,args ...any) []string {
	t.Helper()
	var rows *sql.Rows
	var err error
	if prepared {
		statement,err:=db.PrepareContext(p.Ctx,query)
		if err!=nil {t.Fatal(err)}
		defer statement.Close()
		rows,err=statement.QueryContext(p.Ctx,args...)
		if err!=nil {t.Fatal(err)}
	} else {
		rows,err=db.QueryContext(p.Ctx,query,args...)
		if err!=nil {t.Fatal(err)}
	}
	defer rows.Close()
	columns,err:=rows.Columns()
	if err!=nil || len(columns)!=1 || columns[0]!="QUERY PLAN" {t.Fatal(columns,err)}
	types,err:=rows.ColumnTypes()
	if err!=nil || len(types)!=1 || types[0].DatabaseTypeName()!="TEXT" {t.Fatal(types,err)}
	var lines []string
	for rows.Next(){var line string;if err:=rows.Scan(&line);err!=nil {t.Fatal(err)};lines=append(lines,line)}
	if err:=rows.Err();err!=nil {t.Fatal(err)}
	if len(lines)==0 {t.Fatal("empty analyzed plan")}
	actual:=foreignAnalyzeActual.FindStringSubmatch(lines[0])
	if actual==nil {t.Fatal("missing actual root",lines)}
	count,err:=strconv.ParseFloat(actual[1],64)
	if err!=nil || count!=want || actual[2]!="1" {t.Fatal("incorrect emitted output",actual,lines)}
	if err:=rows.Close();err!=nil {t.Fatal(err)}
	return lines
}

func TestReferenceExplainAnalyzeForeignMutations(t *testing.T){
	for _,prepared:=range []bool{false,true}{
		for _,returning:=range []bool{false,true}{
			for _,rollback:=range []bool{false,true}{
				t.Run(fmt.Sprintf("prepared=%t/returning=%t/rollback=%t",prepared,returning,rollback),func(t *testing.T){
					fdwWriteScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,reader *fdwSQLReader,run func(fixture.Step)){
						fdwLive(t,p,b,reader,run)
						var db foreignAnalyzeDB=c
						var executor fixture.Executor=c
						var tx *sql.Tx
						if rollback {
							var err error
							tx,err=c.BeginTx(p.Ctx,nil)
							if err!=nil {t.Fatal(err)}
							defer tx.Rollback()
							db,executor=tx,tx
						}
						wantRows:=float64(0)
						if returning {wantRows=1}
						cases:=[]struct{query string;want [][]any}{
							{"INSERT INTO fdw_data VALUES(4,'four',true)",[][]any{{int64(1),"one",true},{int64(2),nil,false},{int64(3),"three",nil},{int64(4),"four",true}}},
							{"UPDATE fdw_data SET label='two' WHERE id=2",[][]any{{int64(1),"one",true},{int64(2),"two",false},{int64(3),"three",nil},{int64(4),"four",true}}},
							{"DELETE FROM fdw_data WHERE id=3",[][]any{{int64(1),"one",true},{int64(2),"two",false},{int64(4),"four",true}}},
						}
						for _,test:=range cases {
							query:="EXPLAIN ANALYZE "+test.query
							if returning {query+=" RETURNING id"}
							foreignAnalyzePlan(t,p,db,query,prepared,wantRows)
							fixture.RunStep(t,p.Ctx,executor,b.Recorder,fdwRows("SELECT id,label,flag FROM fdw_data ORDER BY id",test.want))
						}
						if rollback {
							if err:=tx.Rollback();err!=nil {t.Fatal(err)}
							fdwWriteSourceRows(t,p,b,reader,fdwRows("SELECT id,label,flag FROM fdw_source.data ORDER BY id",[][]any{{int64(1),"one",true},{int64(2),nil,false},{int64(3),"three",nil}}))
						} else {
							fdwWriteSourceRows(t,p,b,reader,fdwRows("SELECT id,label,flag FROM fdw_source.data ORDER BY id",cases[2].want))
						}
						step:=fixture.Exec("EXPLAIN ANALYZE UPDATE fdw_data SET label=label WHERE id=1",0)
						step.Prepared=prepared
						run(step)
					})
				})
			}
		}
	}
}
