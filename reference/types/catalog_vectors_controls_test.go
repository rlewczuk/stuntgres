package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func catalogVectorControlRows() [][]any {
	return [][]any{{int64(1),"1 2 3","1 2 3"},{int64(2),"",""},{int64(3),nil,nil},{int64(4),"-32768 32767","4294967295 2147483648"}}
}

func catalogVectorControlSetup(t *testing.T,p *fixture.Pair,b fixture.Backend) {
	t.Helper()
	for _,s:=range []fixture.Step{
		fixture.Exec("CREATE TABLE vectors(id INT,s INT2VECTOR DEFAULT '1 2 3'::int2vector,o OIDVECTOR DEFAULT '1 2 3'::oidvector)",0),
		fixture.Exec("CREATE TABLE vector_empty(s INT2VECTOR,o OIDVECTOR)",0),
		fixture.Exec("INSERT INTO vectors(id) VALUES(1)",1),
		fixture.Exec("INSERT INTO vectors VALUES(2,'',''),(3,NULL,NULL),(4,'-32768 32767','-1 -2147483648')",3),
		fixture.Exec("CREATE FUNCTION vector_control_args(smallint,oid) RETURNS integer LANGUAGE SQL AS 'SELECT 1'",0),
		fixture.Exec("CREATE INDEX vector_control_idx ON vectors(id DESC NULLS FIRST)",0),
	}{fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)}
}

func TestReferenceCatalogVectorsControls(t *testing.T) {
	catalogVectorScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		catalogVectorControlSetup(t,p,b)
		catalogVectorRows(t,p,b,b.DB,fixture.Select("SELECT id,s,o FROM vectors ORDER BY id",[]string{"id","s","o"},[]string{"INT4","INT2VECTOR","OIDVECTOR"},catalogVectorControlRows()))
		catalogVectorRows(t,p,b,b.DB,fixture.Select("SELECT s,o FROM vector_empty",[]string{"s","o"},[]string{"INT2VECTOR","OIDVECTOR"},nil))
		catalogVectorControlFacts(t,p,b,b.DB)
		if !b.PG { catalogVectorNativeControls(t,p,b) }
	})
}

func catalogVectorNativeControls(t *testing.T,p *fixture.Pair,b fixture.Backend) {
	t.Helper()
	var dump bytes.Buffer
	if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil{t.Fatal(err)}
	// The original raw cursor holds a detached schema and immutable snapshot.
	conn,err:=p.Native.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer conn.Close()
	err=conn.Raw(func(raw any)error{
		cursor,err:=raw.(driver.QueryerContext).QueryContext(p.Ctx,"SELECT id,s,o FROM vectors ORDER BY id",nil);if err!=nil{return err};defer cursor.Close()
		check:=func(){
			names:=cursor.Columns();if !reflect.DeepEqual(names,[]string{"id","s","o"}){t.Fatal(names)};names[1]="corrupt"
			if cursor.Columns()[1]!="s"{t.Fatal("Columns aliases retained schema")}
			for i,typ:=range []string{"INT2VECTOR","OIDVECTOR"}{if cursor.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(i+1)!=typ||cursor.(driver.RowsColumnTypeScanType).ColumnTypeScanType(i+1)!=reflect.TypeFor[string](){t.Fatal("retained vector descriptor",typ)}}
		}
		check()
		if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{return err}
		if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));err!=nil{return err}
		var redump bytes.Buffer;if err:=stuntgres.Dump(p.Ctx,p.Native,&redump);err!=nil{return err};if dump.String()!=redump.String(){t.Fatal("nondeterministic vector redump",dump.String(),redump.String())}
		check()
		catalogVectorRows(t,p,b,b.DB,fixture.Select("SELECT id,s,o FROM vectors ORDER BY id",[]string{"id","s","o"},[]string{"INT4","INT2VECTOR","OIDVECTOR"},catalogVectorControlRows()))
		catalogVectorRows(t,p,b,b.DB,fixture.Select("SELECT s,o FROM vector_empty",[]string{"s","o"},[]string{"INT2VECTOR","OIDVECTOR"},nil))
		catalogVectorControlFacts(t,p,b,b.DB)
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("INSERT INTO vectors(id) VALUES(5)",1))
		catalogVectorRows(t,p,b,b.DB,fixture.Select("SELECT s,o FROM vectors WHERE id=5",[]string{"s","o"},[]string{"INT2VECTOR","OIDVECTOR"},[][]any{{"1 2 3","1 2 3"}}))
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("DELETE FROM vectors WHERE id=5",1))
		for _,tc:=range []struct{typ,input,scalar,code string}{{"int2vector","1 x 2","smallint","22P02"},{"int2vector","1 32768 2","smallint","22003"},{"oidvector","1 -2147483649 2","oid","22003"},{"oidvector","1 4294967296 2","oid","22003"}}{
			script:="CREATE TABLE replacement(v "+tc.typ+"); INSERT INTO replacement VALUES('1'); INSERT INTO replacement VALUES("+catalogVectorQuote(tc.input)+")"
			err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(script));suffix:=strings.TrimPrefix(tc.input,"1 ")
			d:=catalogVectorDiagnostic(tc.code,tc.scalar,suffix);d.Position=fixture.Position(strings.LastIndex(script,catalogVectorQuote(tc.input))+1);fixture.AssertDiagnostic(t,err,&d)
			catalogVectorRows(t,p,b,b.DB,fixture.Select("SELECT id,s,o FROM vectors ORDER BY id",[]string{"id","s","o"},[]string{"INT4","INT2VECTOR","OIDVECTOR"},catalogVectorControlRows()))
		}
		// Reader I/O cancels after the complete replacement script is consumed.
		canceled,cancel:=context.WithCancel(p.Ctx);defer cancel()
		err=stuntgres.Restore(canceled,p.Native,&catalogVectorCancelReader{Reader:strings.NewReader("CREATE TABLE replacement(v int2vector); INSERT INTO replacement VALUES('1')"),cancel:cancel})
		if !errors.Is(err,context.Canceled){t.Fatal("canceled Restore",err)}
		catalogVectorRows(t,p,b,b.DB,fixture.Select("SELECT id,s,o FROM vectors ORDER BY id",[]string{"id","s","o"},[]string{"INT4","INT2VECTOR","OIDVECTOR"},catalogVectorControlRows()))
		if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader("CREATE TABLE replacement(v text); INSERT INTO replacement VALUES('new')"));err!=nil{return err}
		check()
		for _,want:=range catalogVectorControlRows(){values:=make([]driver.Value,3);if err:=cursor.Next(values);err!=nil{return err};for i:=range values{if !reflect.DeepEqual(values[i],want[i]){t.Fatal("held vector",values,want)}}}
		if err:=cursor.Next(make([]driver.Value,3));err!=io.EOF{t.Fatal("held EOF",err)};check();if err:=cursor.Close();err!=nil{return err};check()
		return nil
	});if err!=nil{t.Fatal(err)}
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Select("SELECT v FROM replacement",[]string{"v"},[]string{"TEXT"},[][]any{{"new"}}))
	// Reown descriptors and data into a genuinely independent Driver.
	connector,err:=stuntgres.NewDriver().OpenConnector("catalog-vector-destination");if err!=nil{t.Fatal(err)}
	destination:=sql.OpenDB(connector);defer destination.Close()
	if err:=stuntgres.Restore(p.Ctx,destination,bytes.NewReader(dump.Bytes()));err!=nil{t.Fatal(err)}
	wrapped,recorder:=fixture.RecordedNativePool(t,connector.(*stuntgres.Connector))
	copyBackend:=b;copyBackend.DB=wrapped;copyBackend.Recorder=recorder
	catalogVectorRows(t,p,copyBackend,wrapped,fixture.Select("SELECT id,s,o FROM vectors ORDER BY id",[]string{"id","s","o"},[]string{"INT4","INT2VECTOR","OIDVECTOR"},catalogVectorControlRows()))
	catalogVectorControlFacts(t,p,copyBackend,wrapped)
	if p.Postgres!=nil{
		tx,err:=p.Postgres.BeginTx(p.Ctx,nil);if err!=nil{t.Fatal(err)};defer tx.Rollback()
		if _,err:=tx.ExecContext(p.Ctx,"DROP TABLE IF EXISTS vectors; DROP TABLE IF EXISTS vector_empty; DROP FUNCTION IF EXISTS vector_control_args(smallint,oid)");err!=nil{t.Fatal(err)}
		if _,err:=tx.ExecContext(p.Ctx,dump.String());err!=nil{t.Fatal(err)}
		pg:=b;pg.PG=true;pg.DB=p.Postgres;pg.Recorder=p.PostgresRecord
		catalogVectorRows(t,p,pg,tx,fixture.Select("SELECT id,s,o FROM vectors ORDER BY id",[]string{"id","s","o"},[]string{"INT4","INT2VECTOR","OIDVECTOR"},catalogVectorControlRows()))
		catalogVectorRows(t,p,pg,tx,fixture.Select("SELECT s,o FROM vector_empty",[]string{"s","o"},[]string{"INT2VECTOR","OIDVECTOR"},nil))
		catalogVectorControlFacts(t,p,pg,tx)
		if err:=tx.Rollback();err!=nil{t.Fatal(err)}
	}
}

type catalogVectorCancelReader struct { io.Reader;cancel context.CancelFunc }
func(r *catalogVectorCancelReader)Read(dst []byte)(int,error){n,err:=r.Reader.Read(dst);if err==io.EOF{r.cancel()};return n,err}

func catalogVectorControlFacts(t *testing.T,p *fixture.Pair,b fixture.Backend,executor fixture.Executor){
	t.Helper()
	catalogVectorRows(t,p,b,executor,fixture.Select("SELECT proargtypes FROM pg_proc WHERE oid='vector_control_args(smallint,oid)'::regprocedure",[]string{"proargtypes"},[]string{"OIDVECTOR"},[][]any{{"21 26"}}))
	catalogVectorRows(t,p,b,executor,fixture.Select("SELECT indkey,indcollation,indclass,indoption FROM pg_index WHERE indexrelid='vector_control_idx'::regclass",[]string{"indkey","indcollation","indclass","indoption"},[]string{"INT2VECTOR","OIDVECTOR","OIDVECTOR","INT2VECTOR"},[][]any{{"1","0","1978","3"}}))
}
