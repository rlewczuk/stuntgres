package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGSeclabelsNativeControls(t *testing.T) {
	pgSeclabelsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		pgSeclabelsSetup(t,p,b,c)
		held:=pgSeclabelHeld(t,p,c,pgSeclabelsSelect)
		pgSeclabelExec(t,p,b,c,"ALTER TABLE sls_private.sentinel RENAME TO renamed","ALTER TABLE sls_private.renamed RENAME COLUMN note TO renamed_note")
		fixture.RunStep(t,p.Ctx,held,b.Recorder,pgSeclabelsMetadata(b,held.SQL))
		pgSeclabelError(t,p,b,c,"SECURITY LABEL FOR missing ON COLUMN sls_private.renamed.renamed_note IS NULL",pgSeclabelProvider("missing"))
		pgSeclabelExec(t,p,b,c,"ALTER TABLE sls_private.renamed RENAME COLUMN renamed_note TO note","ALTER TABLE sls_private.renamed RENAME TO sentinel")
		pgSeclabelsSentinel(t,p,b,c)
		pgSeclabelExec(t,p,b,c,"ALTER TABLE sls_private.sentinel DROP COLUMN note")
		fixture.RunStep(t,p.Ctx,held,b.Recorder,pgSeclabelsMetadata(b,held.SQL))
		pgSeclabelsShape(t,p,b,c)
		pgSeclabelError(t,p,b,c,"SECURITY LABEL FOR missing ON COLUMN sls_private.sentinel.note IS NULL",pgSeclabelProvider("missing"))
		pgSeclabelsEmpty(t,p,b,c)
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT a FROM sls_private.sentinel ORDER BY a",[]string{"a"},[]string{"INT4"},[][]any{{int64(7)}}))
		pgSeclabelExec(t,p,b,c,"DROP TABLE sls_private.sentinel","CREATE TABLE sls_private.sentinel(a INT,note TEXT)","COMMENT ON TABLE sls_private.sentinel IS 'table kept'","COMMENT ON COLUMN sls_private.sentinel.note IS 'column kept'")
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO sls_private.sentinel VALUES(7,'kept')",1))
		pgSeclabelsSentinel(t,p,b,c)
		pgSeclabelsShape(t,p,b,c)
		fixture.RunStep(t,p.Ctx,held,b.Recorder,pgSeclabelsMetadata(b,held.SQL))
		q:="SECURITY LABEL FOR missing ON TABLE sls_private.sentinel IS 'second'"
		pgSeclabelError(t,p,b,c,q,pgSeclabelProvider("missing"))
		if b.PG {return} // PostgreSQL has no public Reset/Restore API.

		var before bytes.Buffer
		if err:=stuntgres.Dump(p.Ctx,p.Native,&before);err!=nil{t.Fatal(err)}
		for _,s:=range []string{"sentinel","kept","table kept","column kept"}{if !bytes.Contains(before.Bytes(),[]byte(s)){t.Fatal("missing genuine dump state",s)}}
		if bytes.Contains(bytes.ToUpper(before.Bytes()),[]byte("SECURITY LABEL")){t.Fatal("fabricated security label assignment")}
		for _,write:=range pgSeclabelsWrites{pgSeclabelError(t,p,b,c,write.sql,pgSeclabelsWriteDiagnostic(write.operation))}
		pgSeclabelError(t,p,b,c,q,pgSeclabelProvider("missing"))
		var after bytes.Buffer
		if err:=stuntgres.Dump(p.Ctx,p.Native,&after);err!=nil{t.Fatal(err)}
		if !bytes.Equal(before.Bytes(),after.Bytes()){t.Fatal("rejected writes changed canonical dump")}
		retained,err:=b.DB.QueryContext(p.Ctx,pgSeclabelsSelect);if err!=nil{t.Fatal(err)};defer retained.Close()
		columns,err:=retained.ColumnTypes();if err!=nil{t.Fatal(err)}
		names,err:=retained.Columns();if err!=nil{t.Fatal(err)}
		pgSeclabelsDescriptors(t,b,columns)
		if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT relname::text FROM pg_catalog.pg_class WHERE relname='sentinel'",[]string{"relname"},[]string{"TEXT"},nil))
		pgSeclabelsShape(t,p,b,c);pgSeclabelsEmpty(t,p,b,c)
		fixture.RunStep(t,p.Ctx,held,b.Recorder,pgSeclabelsMetadata(b,held.SQL))
		pgSeclabelError(t,p,b,c,q,pgSeclabelProvider("missing"))
		if retained.Next() || retained.Err()!=nil{t.Fatal("retained EOF",retained.Err())};if err:=retained.Close();err!=nil{t.Fatal(err)}
		if !reflect.DeepEqual(names,pgSeclabelsColumns){t.Fatal("retained names",names)};pgSeclabelsDescriptors(t,b,columns)
		if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(before.Bytes()));err!=nil{t.Fatal(err)}
		pgSeclabelsShape(t,p,b,c);pgSeclabelsSentinel(t,p,b,c)
		fixture.RunStep(t,p.Ctx,held,b.Recorder,pgSeclabelsMetadata(b,held.SQL))
		pgSeclabelError(t,p,b,c,q,pgSeclabelProvider("missing"))
		pgSeclabelsDescriptors(t,b,columns)
		ctx,cancel:=context.WithCancel(p.Ctx);cancel()
		for _,x:=range []fixture.Executor{c,held}{rows,err:=x.QueryContext(ctx,pgSeclabelsSelect);if rows!=nil{rows.Close()};if rows!=nil || !errors.Is(err,context.Canceled){t.Fatal("canceled query",rows,err)}}
		pgSeclabelsSentinel(t,p,b,c)
		fixture.RunStep(t,p.Ctx,held,b.Recorder,pgSeclabelsMetadata(b,held.SQL))
	})
}
