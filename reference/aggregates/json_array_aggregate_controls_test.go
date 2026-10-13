package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonArrayAggregateControls(t *testing.T, emitted *string) {
	t.Helper()
	jsonArrayAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		const q="SELECT a,b,s,t,f FROM ja_control_view"
		names:=[]string{"a","b","s","t","f"};types:=[]string{"JSON","JSONB","JSON","JSONB","TEXT"}
		want:=[][]any{{"[2, null, 1]","[2, null, 1]","[2, 1]","[2, 1]","[[2], null, [1]]"}}
		s:=fixture.Select(q,names,types,want)
		s.CheckMetadata=func(t *testing.T,r *sql.Rows){jsonArrayAggregateViewMetadata(t,r,b.PG,names,types,nil)}
		if b.PG {
			if *emitted=="" { t.Fatal("native dump missing: PostgreSQL must replay actual emitted SQL") }
			conn,err:=b.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer conn.Close();defer conn.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION")
			if _,err=conn.ExecContext(p.Ctx,*emitted);err!=nil{t.Fatal(err)}
			fixture.RunStep(t,p.Ctx,conn,b.Recorder,s)
			return
		}
		for _, setup:=range []fixture.Step{
			fixture.Exec("CREATE TABLE ja_control_source(id int,v int,f text)",0),
			fixture.Exec("INSERT INTO ja_control_source VALUES(1,2,'[2]'),(2,NULL,NULL),(3,1,'[1]')",3),
			fixture.Exec("CREATE VIEW ja_control_view AS SELECT json_agg(v ORDER BY id) AS a,jsonb_agg(v ORDER BY id) AS b,json_agg_strict(v ORDER BY id) AS s,jsonb_agg_strict(v ORDER BY id) AS t,json_arrayagg(f FORMAT JSON ORDER BY id NULL ON NULL RETURNING text FORMAT JSON) AS f FROM ja_control_source",0),
		} {fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,setup)}
		jsonArrayAggregateRoutes(t,p,b,s)
		stmt,err:=b.DB.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
		x:=fixture.HeldStatement{b.DB,stmt,q}
		held,err:=stmt.QueryContext(context.WithValue(p.Ctx,fixture.RecorderKey{},b.Recorder));if err!=nil{t.Fatal(err)};defer held.Close()
		before,err:=held.ColumnTypes();if err!=nil{t.Fatal(err)}
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("CREATE TABLE ja_unrelated(v int)",0))
		fixture.RunStep(t,p.Ctx,x,b.Recorder,s)
		if !b.PG {
			var dump bytes.Buffer
			if err=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil{t.Fatal(err)}
			*emitted=dump.String()
			fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("INSERT INTO ja_control_source VALUES(4,9,'[9]')",1))
			if err=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
			if err=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(*emitted));err!=nil{t.Fatal(err)}
			fixture.RunStep(t,p.Ctx,x,b.Recorder,s)
			if err=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(*emitted+"\nCREATE TABLE ja_invalid(v missing_ja_type);"));err==nil{t.Fatal("invalid replacement succeeded")}
			ctx,cancel:=context.WithCancel(p.Ctx);cancel()
			if err=stuntgres.Restore(ctx,p.Native,strings.NewReader(*emitted));!errors.Is(err,context.Canceled){t.Fatalf("canceled restore=%v",err)}
			var redump bytes.Buffer
			if err=stuntgres.Dump(p.Ctx,p.Native,&redump);err!=nil{t.Fatal(err)}
			if redump.String()!=*emitted{t.Fatal("failed replacement changed native dump")}
			fixture.RunStep(t,p.Ctx,x,b.Recorder,s)
			driver:=stuntgres.NewDriver();connector,err:=driver.OpenConnector("json-array-aggregate-fresh/"+t.Name());if err!=nil{t.Fatal(err)}
			fresh:=sql.OpenDB(connector);defer fresh.Close()
			if err=stuntgres.Restore(p.Ctx,fresh,strings.NewReader(*emitted));err!=nil{t.Fatal(err)}
			rows,err:=fresh.QueryContext(p.Ctx,q);if err!=nil{t.Fatal(err)}
			jsonArrayAggregateViewMetadata(t,rows,false,names,types,nil)
			fixture.ScalarRows(t,false,rows,names,types,want,false)
		}
		// The old native cursor retains its captured schema after actual replacement.
		jsonArrayAggregateViewMetadata(t,held,b.PG,names,types,nil)
		after,err:=held.ColumnTypes();if err!=nil||!reflect.DeepEqual(before,after){t.Fatalf("retained metadata changed %v",err)}
		if !held.Next(){t.Fatal("missing retained result",held.Err())}
		raw:=make([]any,len(names));dest:=make([]any,len(names));for i:=range raw{dest[i]=&raw[i]}
		if err=held.Scan(dest...);err!=nil{t.Fatal(err)}
		for i,value:=range raw{fixture.AssertRawValue(t,b.PG,types[i],value);raw[i]=fixture.Normalize(value)}
		if !reflect.DeepEqual(raw,want[0])||held.Next()||held.Err()!=nil{t.Fatalf("retained=%#v err=%v",raw,held.Err())}
	})
}

func TestReferenceJSONArrayAggregateControls(t *testing.T) {
	var emitted string
	jsonArrayAggregateControls(t,&emitted)
}

func TestReferenceJSONArrayAggregateFeatureSmoke(t *testing.T) {
	var emitted string
	t.Run("controls",func(t *testing.T){jsonArrayAggregateControls(t,&emitted)})
	t.Run("behavior",func(t *testing.T){jsonArrayAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		jsonArrayAggregateSetup(t,p,b)
		jsonArrayAggregateCore(t,p,b)
		jsonArrayAggregateReturningCore(t,p,b)
		jsonArrayAggregateSmokeDiagnostics(t,p,b)
		jsonArrayAggregateHeld(t,p,b)
		jsonArrayAggregateConsumers(t,p,b)
		jsonArrayAggregateRoutes(t,p,b,jsonArrayAggregateSelect("SELECT json_agg_strict(j ORDER BY id) AS v FROM ja_input WHERE g='a'","JSON",`[ {"z":1,"a":1,"a":2} , null]`))
	})})
}
