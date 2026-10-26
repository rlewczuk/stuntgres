package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func normalControls(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
	run(fixture.Exec("CREATE VIEW normal_saved AS SELECT * FROM normal_part.normal_rand(3,9,0)",0))
	q:="SELECT * FROM normal_part.normal_rand($1,$2,$3)"
	st,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer st.Close()
	held:=fixture.HeldStatement{Executor:c,Statement:st,SQL:q}
	fixture.RunStep(t,p.Ctx,held,b.Recorder,normalSelect(q,2,9,2,9,0))
	run(fixture.Exec("ALTER FUNCTION normal_part.normal_rand(integer,float8,float8) RENAME TO normal_renamed",0))
	run(fixture.Exec("CREATE SCHEMA normal_moved",0))
	run(fixture.Exec("ALTER FUNCTION normal_part.normal_renamed(integer,float8,float8) SET SCHEMA normal_moved",0))
	movedCost := fixture.Select("SELECT p.procost,p.prosrc FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='normal_moved' AND p.proname='normal_renamed'", []string{"procost","prosrc"}, []string{"FLOAT4","TEXT"}, [][]any{{float64(1),"normal_rand"}})
	run(movedCost); run(fixture.Prepared(movedCost))
	check:=normalSelect("SELECT * FROM normal_saved",3,9)
	run(check)
	renamed:=normalSelect("SELECT * FROM normal_moved.normal_renamed(2,9,0)",2,9)
	// The table source adopts the current public routine label.
	renamed.Columns=[]string{"normal_renamed"};run(renamed)
	if b.PG{normalDropDependency(t,p,b,c);return}
	const renamedQuery="SELECT * FROM normal_moved.normal_renamed($1,$2,$3)"
	renamedStatement,err:=c.PrepareContext(p.Ctx,renamedQuery);if err!=nil{t.Fatal(err)};defer renamedStatement.Close()
	renamedHeld:=fixture.HeldStatement{Executor:c,Statement:renamedStatement,SQL:renamedQuery}
	heldCheck:=normalSelect(renamedQuery,2,9,2,9,0);heldCheck.Columns=[]string{"normal_renamed"}
	fixture.RunStep(t,p.Ctx,renamedHeld,b.Recorder,heldCheck)
	rows,err:=c.QueryContext(p.Ctx,"SELECT * FROM normal_saved");if err!=nil{t.Fatal(err)};defer rows.Close()
	cols,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)}
	dump:=extensionDump(t,p.Ctx,p.Native)
	if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
	run(normalMissing("SELECT normal_rand(1,9,0)","normal_rand","8"))
	run(fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='tablefunc'",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))
	if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump));err!=nil{t.Fatal(err)}
	run(check);run(renamed)
	run(movedCost); run(fixture.Prepared(movedCost))
	fixture.RunStep(t,p.Ctx,renamedHeld,b.Recorder,heldCheck)
	for range 3 {var value float64;if !rows.Next(){t.Fatal("lost published rows",rows.Err())};if err:=rows.Scan(&value);err!=nil||value!=9{t.Fatal(value,err)}}
	if rows.Next()||rows.Err()!=nil||len(cols)!=1||cols[0].DatabaseTypeName()!="FLOAT8"{t.Fatal("retained schema/tail",rows.Err())};rows.Close()
	before:=extensionDump(t,p.Ctx,p.Native)
	if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump+"\nINVALID SQL;"));err==nil{t.Fatal("accepted bad restore")}
	if extensionDump(t,p.Ctx,p.Native)!=before{t.Fatal("failed restore changed provider")};run(check)
	ctx,cancel:=context.WithCancel(p.Ctx)
	err=stuntgres.Restore(ctx,p.Native,&fixture.CatalogControlCancelReader{Cancel:cancel});cancel()
	if !errors.Is(err,context.Canceled)||extensionDump(t,p.Ctx,p.Native)!=before{t.Fatal("canceled restore",err)};run(check)
	other,target,record:=pgcryptoControlDestination(t,p,"normal_destination")
	if err:=stuntgres.Restore(p.Ctx,other,strings.NewReader(dump));err!=nil{t.Fatal(err)}
	fixture.RunStep(t,p.Ctx,target,record,check);fixture.RunStep(t,p.Ctx,target,record,renamed)
	fixture.RunStep(t,p.Ctx,target,record,movedCost)
	fixture.RunStep(t,p.Ctx,target,record,fixture.Prepared(movedCost))
	normalDropDependency(t,p,b,c)
}
func TestReferenceTablefuncNormalIdentityAndControls(t *testing.T){normalCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){normalControls(t,p,b,c)})}

func TestReferenceTablefuncNormalFeatureSmoke(t *testing.T){
	normalCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
		run(fixture.Exec("CREATE SCHEMA normal_smoke",0))
		run(fixture.Exec("ALTER FUNCTION normal_part.normal_rand(integer,float8,float8) SET SCHEMA normal_smoke",0))
		run(normalSelect("SELECT * FROM normal_smoke.normal_rand(3,-7,0)",3,-7))
		q:="SELECT * FROM normal_smoke.normal_rand($1,$2,$3)"
		st,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer st.Close()
		held:=fixture.HeldStatement{Executor:c,Statement:st,SQL:q}
		for _,s:=range []fixture.Step{normalSelect(q,2,9,2,9,0),normalSelect(q,0,0,0,9,0),normalSelect(q,0,0,2,9,nil),normalNegative(q,-1,9,0)}{fixture.RunStep(t,p.Ctx,held,b.Recorder,s)}
		values:=normalRead(t,p,held,q,32,0,-2)
		if len(values)!=32{t.Fatal("random count")};different:=false
		for _,v:=range values{if math.IsNaN(v)||math.IsInf(v,0){t.Fatal("nonfinite")};different=different||v!=values[0]};if !different{t.Fatal("constant Gaussian")}
		if b.PG{return}
		run(fixture.Exec("CREATE VIEW normal_smoke_saved AS SELECT * FROM normal_smoke.normal_rand(3,9,0)",0))
		saved,err:=c.QueryContext(p.Ctx,"SELECT * FROM normal_smoke_saved");if err!=nil{t.Fatal(err)};defer saved.Close()
		savedTypes,err:=saved.ColumnTypes();if err!=nil{t.Fatal(err)}
		dump:=extensionDump(t,p.Ctx,p.Native)
		if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
		if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump));err!=nil{t.Fatal(err)}
		run(normalSelect("SELECT * FROM normal_smoke_saved",3,9))
		for range 3{var value float64;if !saved.Next(){t.Fatal("smoke retained row",saved.Err())};if err:=saved.Scan(&value);err!=nil||value!=9{t.Fatal(value,err)}}
		if saved.Next()||saved.Err()!=nil||len(savedTypes)!=1||savedTypes[0].DatabaseTypeName()!="FLOAT8"{t.Fatal("smoke retained metadata",saved.Err())}
		saved.Close()
		run(fixture.Select("SELECT count(*) AS n FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_depend d ON d.objid=p.oid AND d.deptype='e' JOIN pg_extension e ON e.oid=d.refobjid WHERE n.nspname='normal_smoke' AND p.proname='normal_rand' AND e.extname='tablefunc'",[]string{"n"},[]string{"INT8"},[][]any{{int64(1)}}))
		fixture.RunStep(t,p.Ctx,held,b.Recorder,normalSelect(q,2,9,2,9,0))
	})
}
