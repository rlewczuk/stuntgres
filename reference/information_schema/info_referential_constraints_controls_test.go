package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func ircRetained(t *testing.T,rows *sql.Rows,want [][]any) {
	t.Helper()
	columns,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)};ircMetadata(t,columns,false)
	used:=make([]bool,len(want));count:=0
	for rows.Next() {
		var cells [9]any
		if err:=rows.Scan(&cells[0],&cells[1],&cells[2],&cells[3],&cells[4],&cells[5],&cells[6],&cells[7],&cells[8]);err!=nil{t.Fatal(err)}
		matched:=false
		for i,candidate:=range want {if !used[i]&&reflect.DeepEqual(cells[:],candidate){used[i],matched=true,true;break}}
		if !matched{t.Fatalf("unexpected retained row %v",cells)};count++
	}
	if rows.Err()!=nil||count!=len(want){t.Fatalf("retained count=%d want=%d error=%v",count,len(want),rows.Err())}
	ircMetadata(t,columns,false);if err:=rows.Close();err!=nil{t.Fatal(err)};ircMetadata(t,columns,false)
}

func TestReferenceInfoReferentialConstraintsNativeControls(t *testing.T) {
	ircScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fixture.Recorder,db string) {
		run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,r,s)}
		ircSeed(run)
		run(fixture.Exec("ALTER TABLE irc_scope.child DROP CONSTRAINT child_fk",0))
		run(fixture.Exec("ALTER TABLE irc_scope.child ADD CONSTRAINT child_fk FOREIGN KEY(pid) REFERENCES irc_scope.parent MATCH FULL ON UPDATE CASCADE ON DELETE SET NULL",0))
		want:=[][]any{ircRow(db,"irc_scope","child_fk","irc_scope","parent_pk","FULL","CASCADE","SET NULL")}
		s:=ircProjection(b.PG,"constraint_schema='irc_scope'",want)
		ircCheck(t,p,c,r,ircHold(t,p,c,s.Query),s)
		if b.PG{return} // Process-local controls have no PostgreSQL API counterpart.
		ctx,cancel:=context.WithCancel(p.Ctx);cancel()
		for _,x:=range []fixture.Executor{c,ircHold(t,p,c,s.Query)} {
			rows,err:=x.QueryContext(ctx,s.Query);if rows!=nil{rows.Close();t.Fatal("canceled query published rows")};if !errors.Is(err,context.Canceled){t.Fatal(err)}
			var state interface{SQLState()string};if errors.As(err,&state){t.Fatal("ordinary cancellation exposed SQLSTATE")}
		}
		run(s)
		var dump bytes.Buffer
		if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil{t.Fatal(err)}
		retained,err:=b.DB.QueryContext(p.Ctx,s.Query);if err!=nil{t.Fatal(err)};defer retained.Close()
		if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
		run(ircProjection(false,"constraint_schema='irc_scope'",nil))
		if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));err!=nil{t.Fatal(err)}
		run(s);run(fixture.Prepared(s));ircRetained(t,retained,want)
		target:=fixture.NewFixture(t,false,true)
		if err:=stuntgres.Restore(target.Ctx,target.Native,bytes.NewReader(dump.Bytes()));err!=nil{t.Fatal(err)}
		var targetDB string;if err:=target.Memory.QueryRowContext(target.Ctx,"SELECT current_database()").Scan(&targetDB);err!=nil{t.Fatal(err)}
		check:=func(){s:=ircProjection(false,"constraint_schema='irc_scope'",[][]any{ircRow(targetDB,"irc_scope","child_fk","irc_scope","parent_pk","FULL","CASCADE","SET NULL")});fixture.RunStep(t,target.Ctx,target.Memory,target.MemoryRecord,s);fixture.RunStep(t,target.Ctx,target.Memory,target.MemoryRecord,fixture.Prepared(s))}
		check()
		bad:="CREATE SCHEMA staged; CREATE TABLE staged.parent(id int); CREATE TABLE staged.child(pid int REFERENCES staged.parent(id));"
		fixture.State(t,stuntgres.Restore(target.Ctx,target.Native,strings.NewReader(bad)),"42830");check()
		if err:=stuntgres.Restore(ctx,target.Native,bytes.NewReader(dump.Bytes()));!errors.Is(err,context.Canceled){t.Fatal(err)};check()
	})
}
