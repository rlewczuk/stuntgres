package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRelationSizeLifecycle(t *testing.T) {
	p:=fixture.NewScenario(t)
	for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){
		relationSizeFixture(t,p,b,"rs_lifecycle")
		relationSizeRun(t,p,b,fixture.Exec("CREATE INDEX rs_lifecycle_partial ON rs_lifecycle(i) WHERE i=1",0))
		relationSizeRun(t,p,b,fixture.Exec("CREATE INDEX rs_lifecycle_include ON rs_lifecycle(i) INCLUDE(s)",0))
		relationSizeRun(t,p,b,fixture.Exec("CREATE INDEX rs_lifecycle_hash ON rs_lifecycle USING hash(i)",0))
		relationSizeRun(t,p,b,fixture.Exec("CREATE INDEX rs_lifecycle_brin ON rs_lifecycle USING brin(i)",0))
		relationSizeRun(t,p,b,fixture.Exec("CREATE INDEX rs_lifecycle_expr ON rs_lifecycle((i+1))",0))
		relationSizeRun(t,p,b,fixture.Exec("CREATE VIEW rs_lifecycle_sizes AS SELECT pg_table_size('rs_lifecycle') AS heap,pg_indexes_size('rs_lifecycle') AS indexes",0))
		before:=relationSizeComponents(t,p,b,"rs_lifecycle")
		q:="SELECT pg_total_relation_size('rs_lifecycle') AS v"
		stmt,err:=b.DB.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
		old,err:=stmt.QueryContext(p.Ctx);if err!=nil{t.Fatal(err)};defer old.Close()
		oldTypes,err:=old.ColumnTypes();if err!=nil||len(oldTypes)!=1||oldTypes[0].DatabaseTypeName()!="INT8"{t.Fatal(oldTypes,err)}
		relationSizeRun(t,p,b,fixture.Exec("INSERT INTO rs_lifecycle VALUES(3,'y',TRUE)",1))
		if !old.Next(){t.Fatal(old.Err())};var saved int64
		if err:=old.Scan(&saved);err!=nil||saved!=before[2]{t.Fatal("retained row changed",saved,before,err)}
		old.Close()
		if !b.PG {
			var fresh int64
			if err:=stmt.QueryRowContext(p.Ctx).Scan(&fresh);err!=nil||fresh<=saved{t.Fatal(fresh,saved,err)}
		}
		published:=relationSizeComponents(t,p,b,"rs_lifecycle")
		relationSizeRun(t,p,b,fixture.Exec("CREATE TABLE rs_returning(i INT)",0))
		relationSizeRun(t,p,b,fixture.Exec("CREATE FUNCTION rs_returning_size() RETURNS bigint LANGUAGE SQL VOLATILE RETURN pg_table_size('rs_returning')",0))
		directTest:="pg_table_size('rs_returning')>0"
		nestedTest:="rs_returning_size()>0"
		if !b.PG {directTest="pg_table_size('rs_returning')=113";nestedTest="rs_returning_size()=153"}
		relationSizeRun(t,p,b,fixture.Select("INSERT INTO rs_returning VALUES(1) RETURNING "+directTest+" AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}}))
		relationSizeRun(t,p,b,fixture.Prepared(fixture.Select("INSERT INTO rs_returning VALUES(2) RETURNING "+nestedTest+" AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}})))
		// A private row is charged through transaction-owned typed storage,
		// without publishing it or using RLS-visible row enumeration.
		tx,err:=b.DB.BeginTx(p.Ctx,nil);if err!=nil{t.Fatal(err)}
		defer tx.Rollback()
		fixture.RunStep(t,p.Ctx,tx,b.Recorder,fixture.Exec("INSERT INTO rs_lifecycle VALUES(4,'z',TRUE)",1))
		var privateTotal int64
		if err:=tx.QueryRowContext(p.Ctx,q).Scan(&privateTotal);err!=nil{t.Fatal(err)}
		if !b.PG && privateTotal!=published[2]+208{t.Fatal("private heap/index allocations counted once",privateTotal,published)}
		if err:=tx.Rollback();err!=nil{t.Fatal(err)}
		if b.PG {return}
		parameterSQL:="SELECT pg_total_relation_size($1::regclass) AS v"
		parameterStmt,err:=b.DB.PrepareContext(p.Ctx,parameterSQL);if err!=nil{t.Fatal(err)};defer parameterStmt.Close()
		// Replay promises clean logical contents, not dead-version or stale
		// summary allocations. Capture after rebuilding through a clean restore.
		var dump bytes.Buffer
		if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil{t.Fatal(err)}
		for _,token:=range []string{"rs_lifecycle_idx","rs_lifecycle_partial","rs_lifecycle_include","rs_lifecycle_hash","rs_lifecycle_brin","rs_lifecycle_expr","pg_table_size","pg_indexes_size"}{if !strings.Contains(dump.String(),token){t.Fatal("dump lost typed metadata",token)}}
		if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
		if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));err!=nil{t.Fatal(err)}
		clean:=relationSizeComponents(t,p,b,"rs_lifecycle")
		var cleanDump bytes.Buffer
		if err:=stuntgres.Dump(p.Ctx,p.Native,&cleanDump);err!=nil{t.Fatal(err)}
		// Native replacement retires captured persistent REGCLASS literals,
		// unlike a parameter resolved against the current catalog on each use.
		retired,err:=stmt.QueryContext(p.Ctx)
		if retired!=nil{retired.Close();t.Fatal("retired literal exposed rows")}
		if err==nil||err.Error()!="stuntgres: catalog replaced before publication"{t.Fatal("literal lifetime fence",err)}
		var state interface{SQLState() string}
		if errors.As(err,&state){t.Fatal("native replacement acquired a SQLSTATE",state.SQLState())}
		replacement,err:=b.DB.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer replacement.Close()
		fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:b.DB,Statement:replacement,SQL:q},b.Recorder,relationSizeSelect(q,[][]any{{clean[2]}}))
		retained,err:=parameterStmt.QueryContext(p.Ctx,"rs_lifecycle");if err!=nil{t.Fatal(err)};defer retained.Close()
		if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
		if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(cleanDump.Bytes()));err!=nil{t.Fatal(err)}
		if got:=relationSizeComponents(t,p,b,"rs_lifecycle");got!=clean{t.Fatal("clean replay changed allocation",got,clean)}
		fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:b.DB,Statement:parameterStmt,SQL:parameterSQL},b.Recorder,relationSizeSelect(parameterSQL,[][]any{{clean[2]}},"rs_lifecycle"))
		metadata,err:=retained.ColumnTypes();if err!=nil||len(metadata)!=1||metadata[0].DatabaseTypeName()!="INT8"{t.Fatal(metadata,err)}
		if !retained.Next(){t.Fatal(retained.Err())};var previous int64
		if err:=retained.Scan(&previous);err!=nil||previous!=clean[2]{t.Fatal(previous,clean,err)};retained.Close()
		for _,input:=range []string{"CREATE TABLE rs_failed(i INT); INSERT INTO rs_missing VALUES(1)","CREATE TABLE"}{
			if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(input));err==nil{t.Fatal("invalid restore accepted")}
			if got:=relationSizeComponents(t,p,b,"rs_lifecycle");got!=clean{t.Fatal("failed restore changed allocation",got,clean)}
		}
		ctx,cancel:=context.WithCancel(p.Ctx);cancel()
		if err:=stuntgres.Restore(ctx,p.Native,bytes.NewReader(cleanDump.Bytes()));!errors.Is(err,context.Canceled){t.Fatal(err)}
		for _,prepared:=range []bool{false,true}{
			var rows *sql.Rows
			var err error
			if prepared {rows,err=parameterStmt.QueryContext(ctx,"rs_lifecycle")}else{rows,err=b.DB.QueryContext(ctx,q)}
			// Do not hide an ordinary adapter cancellation behind fabricated SQLSTATE.
			if !errors.Is(err,context.Canceled){t.Fatal(err)}
			if rows!=nil {rows.Close();t.Fatal("canceled query exposed rows")}
		}
		if got:=relationSizeComponents(t,p,b,"rs_lifecycle");got!=clean{t.Fatal(got,clean)}
		relationSizeRun(t,p,b,fixture.Select("SELECT heap,indexes FROM rs_lifecycle_sizes",[]string{"heap","indexes"},[]string{"INT8","INT8"},[][]any{{clean[0],clean[1]}}))
	})}
}

func TestReferenceRelationSizeSmoke(t *testing.T) {
	t.Run("values",TestReferenceRelationSizeValues)
	t.Run("lookup",TestReferenceRelationSizeLookupForksNull)
	t.Run("components",TestReferenceRelationSizeComponents)
	t.Run("privileges",TestReferenceRelationSizePrivileges)
	t.Run("action_invoker",TestReferenceRelationSizeActionInvoker)
	t.Run("lifecycle",TestReferenceRelationSizeLifecycle)
}
