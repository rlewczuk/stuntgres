package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"
	"stuntgres"
	"stuntgres/reference/fixture"
)

func xmlAggLifecycle(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,controls bool){
	t.Helper()
	run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
	run(fixture.Exec("CREATE TABLE xa(k int,x xml)",0))
	run(fixture.Exec("INSERT INTO xa VALUES(2,'<b/>'),(NULL,'<n/>'),(1,'<a/>'),(3,NULL)",4))
	const q="SELECT xmlagg(x ORDER BY k) AS v FROM xa"
	xmlAggBoth(t,p,b,c,xmlAggSelect(q,"XML","<a/><b/><n/>"))
	xmlAggBoth(t,p,b,c,xmlAggSelect("SELECT xmlagg(x ORDER BY k DESC NULLS LAST) AS v FROM xa","XML","<b/><a/><n/>"))
	xmlAggBoth(t,p,b,c,xmlAggSelect("SELECT xmlagg(x) AS v FROM xa WHERE false","XML",nil))
	stmt,err:=b.DB.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
	held:=fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:q}
	rows,err:=stmt.QueryContext(context.WithValue(p.Ctx,fixture.RecorderKey{},b.Recorder));if err!=nil{t.Fatal(err)};defer rows.Close()
	rangeAggregateMetadata(t,rows,b.PG,[]string{"XML"})
	run(fixture.Exec("CREATE TABLE xo(v xml)",0))
	for _,prepared:=range []bool{false,true}{s:=fixture.Exec("INSERT INTO xo SELECT xmlagg(x ORDER BY k) FROM xa",1);s.Prepared=prepared;run(s)}
	xmlAggBoth(t,p,b,c,xmlAggSelect("SELECT v FROM xo","XML","<a/><b/><n/>","<a/><b/><n/>"))
	run(fixture.Exec("INSERT INTO xo SELECT xmlagg(x) FROM xa WHERE false",1))
	xmlAggBoth(t,p,b,c,xmlAggSelect("SELECT v FROM xo WHERE v IS NULL","XML",nil))
	run(fixture.Exec("INSERT INTO xa VALUES(0,'<z/>')",1))
	fixture.RunStep(t,p.Ctx,held,b.Recorder,xmlAggSelect(q,"XML","<z/><a/><b/><n/>"))
	if !rows.Next(){t.Fatal("missing retained row")};var retained string;if err:=rows.Scan(&retained);err!=nil{t.Fatal(err)}
	if retained!="<a/><b/><n/>"{t.Fatal("retained output changed",retained)}
	if rows.Next()||rows.Err()!=nil{t.Fatal(rows.Err())};rows.Close()
	d:=xmlAggDiagnostic("42883","function xmlagg(integer) does not exist","No function matches the given name and argument types. You might need to add explicit type casts.",8)
	xmlAggFailure(t,p,b,c,"SELECT xmlagg(1) FROM xa",d,true)
	if !controls{return}
	run(fixture.Exec("CREATE VIEW xv AS SELECT xmlagg(x ORDER BY k) AS v FROM xa",0))
	xmlAggBoth(t,p,b,c,xmlAggSelect("SELECT v FROM xv","XML","<z/><a/><b/><n/>"))
	for _,prepared:=range []bool{false,true}{
		d:=xmlAggDiagnostic("2200N","invalid XML content","",0);d.Detail="line 1: Premature end of data in tag a line 1\n<a>\n   ^"
		s:=fixture.ErrorDiagnostic("INSERT INTO xo SELECT xmlagg(XMLPARSE(CONTENT $1)) FROM xa",d,"<a>");s.Prepared=prepared;s.ErrorStage="execute";run(s)
	}
	xmlAggBoth(t,p,b,c,xmlAggSelect("SELECT count(*) AS v FROM xo","INT8",int64(3)))
	if !b.PG {
		// Native admission rejects pre-canceled work. A fast pq request need not
		// observe cancellation; do not interrupt its transport before held reuse.
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		for _, prepared := range []bool{false, true} {
			var r *sql.Rows
			var err error
			if prepared {
				r, err = stmt.QueryContext(ctx)
			} else {
				r, err = c.QueryContext(ctx, q)
			}
			if r != nil {
				r.Close()
				t.Fatal("pre-canceled XML aggregate published rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation %v", err)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("ordinary cancellation acquired SQLSTATE", state.SQLState())
			}
		}
	}
	fixture.RunStep(t,p.Ctx,held,b.Recorder,xmlAggSelect(q,"XML","<z/><a/><b/><n/>"))
	if !b.PG{
		var dump bytes.Buffer
		if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil{t.Fatal(err)}
		if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));err!=nil{t.Fatal(err)}
		xmlAggBoth(t,p,b,c,xmlAggSelect("SELECT v FROM xv","XML","<z/><a/><b/><n/>"))
		fixture.RunStep(t,p.Ctx,held,b.Recorder,xmlAggSelect(q,"XML","<z/><a/><b/><n/>"))
	}
	if retained!="<a/><b/><n/>"{t.Fatal("retained scalar changed")}
}
func TestReferenceXMLAggLifecycle(t *testing.T){xmlAggScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){xmlAggLifecycle(t,p,b,c,true)})}
func TestReferenceXMLAggSmoke(t *testing.T){xmlAggScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){xmlAggLifecycle(t,p,b,c,false)})}
