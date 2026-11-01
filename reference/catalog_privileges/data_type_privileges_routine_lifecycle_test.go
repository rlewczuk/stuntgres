package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"stuntgres/reference/fixture"
)

func dtpNullableBag(rows [][]any) [][]any {
	sort.Slice(rows,func(i,j int)bool{return fmt.Sprint(rows[i])<fmt.Sprint(rows[j])})
	return rows
}

func dtpReadNullableRows(t *testing.T,rows *sql.Rows)[][]any{
	t.Helper();defer rows.Close()
	var got [][]any
	for rows.Next(){
		var cells [5]sql.NullString
		if err:=rows.Scan(&cells[0],&cells[1],&cells[2],&cells[3],&cells[4]);err!=nil{t.Fatal(err)}
		row:=make([]any,5);for i,v:=range cells{if v.Valid{row[i]=v.String}}
		got=append(got,row)
	}
	if err:=rows.Err();err!=nil{t.Fatal(err)}
	return dtpNullableBag(got)
}

func dtpExpectedDatabase(t *testing.T,p *fixture.Pair,c *sql.Conn,want [][]any)[][]any{
	t.Helper();var db string
	if err:=c.QueryRowContext(p.Ctx,"SELECT current_database()").Scan(&db);err!=nil{t.Fatal(err)}
	var rows [][]any;for _,row:=range want{rows=append(rows,append([]any{db},row...))}
	return dtpNullableBag(rows)
}

func dtpRetainedMetadata(t *testing.T,rows *sql.Rows)func(){
	t.Helper();before,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)}
	if len(before)!=5{t.Fatal(before)}
	for i,typ:=range before{if typ.Name()!=dtpColumns[i]||typ.DatabaseTypeName()!=dtpTypes[i]{t.Fatal("retained metadata",i,typ)}}
	return func(){
		after,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)}
		if len(after)!=len(before){t.Fatal("retained metadata width changed")}
		for i,typ:=range after{if !reflect.DeepEqual(*typ,*before[i]){t.Fatal("retained metadata changed",i)}}
	}
}

func TestReferenceDataTypePrivilegesRoutineLifecycle(t *testing.T){
	dtpScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,run func(fixture.Step)){
		for _,q:=range []string{"CREATE FUNCTION dtp_live(a integer,OUT b integer) LANGUAGE SQL AS 'SELECT a'","CREATE PROCEDURE dtp_live_proc() LANGUAGE SQL AS 'SELECT 1'","REVOKE EXECUTE ON FUNCTION dtp_live(integer) FROM PUBLIC","REVOKE EXECUTE ON PROCEDURE dtp_live_proc() FROM PUBLIC"}{run(fixture.Exec(q,0))}
		id:=dtpRoutineIdentity(t,p,c,"public","dtp_live","integer");pid:=dtpRoutineIdentity(t,p,c,"public","dtp_live_proc","")
		var oid int64;if err:=c.QueryRowContext(p.Ctx,"SELECT 'dtp_live(integer)'::regprocedure::oid").Scan(&oid);err!=nil{t.Fatal(err)}
		where:="object_schema='public' AND object_type='ROUTINE'"
		q:="SELECT * FROM information_schema.data_type_privileges WHERE "+where
		stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
		original:=append(dtpRoutineRows("public",id,"0","1","2"),dtpRoutineRows("public",pid,nil)...)
		expected:=dtpExpectedDatabase(t,p,c,original)
		check:=func(want [][]any){rows,err:=stmt.QueryContext(p.Ctx);if err!=nil{t.Fatal(err)};if got:=dtpReadNullableRows(t,rows);!reflect.DeepEqual(got,want){t.Fatal("held routine execution",got,want)}}
		check(expected)
		dtpActor(run,"dtp_reader",func(){check(nil)})
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION dtp_live(integer) TO dtp_reader",0));run(fixture.Exec("GRANT EXECUTE ON PROCEDURE dtp_live_proc() TO dtp_reader",0))
		peer,err:=b.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer peer.Close()
		dtpActor(run,"dtp_reader",func(){
			check(expected)
			old,err:=b.DB.QueryContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer old.Close()
			metadata:=dtpRetainedMetadata(t,old)
			// Retained Rows, held-statement execution and DDL each use their own
			// physical connection; never interleave PostgreSQL protocol commands.
			mutate:=func(sql string){fixture.RunStep(t,p.Ctx,peer,b.Recorder,fixture.Exec(sql,0))}
			mutate("REVOKE EXECUTE ON FUNCTION dtp_live(integer) FROM dtp_reader")
			mutate("REVOKE EXECUTE ON PROCEDURE dtp_live_proc() FROM dtp_reader")
			check(nil)
			mutate("GRANT EXECUTE ON FUNCTION dtp_live(integer) TO dtp_reader")
			mutate("GRANT EXECUTE ON PROCEDURE dtp_live_proc() TO dtp_reader")
			check(expected)
			mutate("ALTER FUNCTION dtp_live(integer) RENAME TO dtp_live_renamed")
			var actualOID int64
			if err:=peer.QueryRowContext(p.Ctx,"SELECT 'dtp_live_renamed(integer)'::regprocedure::oid").Scan(&actualOID);err!=nil||actualOID!=oid{t.Fatal("rename changed real identity",actualOID,oid,err)}
			renamedRows:=append(dtpRoutineRows("public",dtpSpecificName(t,"dtp_live_renamed",oid),"0","1","2"),dtpRoutineRows("public",pid,nil)...)
			check(dtpExpectedDatabase(t,p,c,renamedRows))
			mutate("DROP FUNCTION dtp_live_renamed(integer)")
			check(dtpExpectedDatabase(t,p,c,dtpRoutineRows("public",pid,nil)))
			mutate("DROP PROCEDURE dtp_live_proc()")
			metadata()
			if got:=dtpReadNullableRows(t,old);!reflect.DeepEqual(got,expected){t.Fatal("retained routine rows changed",got,expected)}
			check(nil)
		})
		// A fresh real declaration has a fresh identity; the held query sees it.
		run(fixture.Exec("CREATE FUNCTION dtp_live(a integer,OUT b integer) LANGUAGE SQL AS 'SELECT a'",0))
		renamed:=dtpRoutineIdentity(t,p,c,"public","dtp_live","integer")
		run(fixture.Exec("ALTER FUNCTION dtp_live(integer) RENAME TO dtp_live_renamed",0))
		var retainedOID int64;if err:=c.QueryRowContext(p.Ctx,"SELECT 'dtp_live_renamed(integer)'::regprocedure::oid").Scan(&retainedOID);err!=nil{t.Fatal(err)}
		if renamed!=dtpSpecificName(t,"dtp_live",retainedOID)||retainedOID==oid{t.Fatal("routine identity lifecycle",oid,retainedOID,renamed)}
		newID:=dtpSpecificName(t,"dtp_live_renamed",retainedOID)
		check(dtpExpectedDatabase(t,p,c,dtpRoutineRows("public",newID,"0","1","2")))
		dtpCheckRoutineJoins(t,p,c,run,"public",newID,[]string{"1","2"},"0")
	})
}
