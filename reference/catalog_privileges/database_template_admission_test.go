package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

func templateBusy(c *databaseCreateCase, target, source, detail string, prepared bool) {
	c.t.Helper()
	c.remember(target)
	d:=databaseCreateDiagnostic("55006",fmt.Sprintf("source database \"%s\" is being accessed by other users",source))
	d.Detail=detail
	s:=fixture.ErrorDiagnostic("CREATE DATABASE "+pq.QuoteIdentifier(target)+" TEMPLATE "+pq.QuoteIdentifier(source),d)
	if prepared {s=fixture.Prepared(s)}
	c.run(s);c.absent(target)
}

func TestReferenceDatabaseTemplateAdmission(t *testing.T) {
	databaseCreateScenario(t,func(c *databaseCreateCase){
		c.create("dt_busy_source","",false)
		a,_,_:=c.pool("dt_busy_source","")
		if err:=a.PingContext(c.p.Ctx);err!=nil {c.t.Fatal(err)}
		templateBusy(c,"dt_busy_one","dt_busy_source","There is 1 other session using the database.",false)
		b,_,_:=c.pool("dt_busy_source","")
		if err:=b.PingContext(c.p.Ctx);err!=nil {c.t.Fatal(err)}
		templateBusy(c,"dt_busy_two","dt_busy_source","There are 2 other sessions using the database.",true)
		c.create("dt_duplicate","",false)
		c.error("CREATE DATABASE dt_duplicate TEMPLATE dt_busy_source","42P04",`database "dt_duplicate" already exists`)
		c.run(fixture.Exec("CREATE ROLE dc_denied CREATEDB",0));c.run(fixture.Exec("SET ROLE dc_denied",0))
		c.error("CREATE DATABASE dt_duplicate TEMPLATE dt_busy_source","42501",`permission denied to copy database "dt_busy_source"`)
		c.run(fixture.Exec("RESET ROLE",0))
		if err:=a.Close();err!=nil {c.t.Fatal(err)};if err:=b.Close();err!=nil {c.t.Fatal(err)}
		c.create("dt_busy_one","TEMPLATE dt_busy_source",true)
		quotedSource,quotedTarget:="dt busy\"source","dt busy\"target"
		c.create(quotedSource,"",false)
		quoted,_,_:=c.pool(quotedSource,"")
		if err:=quoted.PingContext(c.p.Ctx);err!=nil {c.t.Fatal(err)}
		templateBusy(c,quotedTarget,quotedSource,"There is 1 other session using the database.",true)
		c.run(fixture.Exec("SET ROLE dc_denied",0))
		c.error("CREATE DATABASE "+pq.QuoteIdentifier(quotedTarget)+" TEMPLATE "+pq.QuoteIdentifier(quotedSource),"42501","permission denied to copy database \""+quotedSource+"\"")
		templateAbsent(c,quotedTarget)
		c.run(fixture.Exec("RESET ROLE",0))
		if err:=quoted.Close();err!=nil {c.t.Fatal(err)}
		c.create(quotedTarget,"TEMPLATE "+pq.QuoteIdentifier(quotedSource),true)
	})
}

func TestReferenceDatabaseTemplateExecutorSelf(t *testing.T) {
	databaseCreateScenario(t,func(c *databaseCreateCase){
		c.create("dt_self_source","",false)
		db,_,rec:=c.pool("dt_self_source","")
		conn,err:=db.Conn(c.p.Ctx);if err!=nil {c.t.Fatal(err)};defer conn.Close()
		run:=func(s fixture.Step){fixture.RunStep(c.t,c.p.Ctx,conn,rec,s)}
		run(fixture.Exec("CREATE TABLE dt_permanent(v integer)",0));run(fixture.Exec("INSERT INTO dt_permanent VALUES(7)",1))
		run(fixture.Exec("CREATE TEMP TABLE dt_temporary(v integer)",0))
		run(fixture.Exec("INSERT INTO dt_temporary VALUES(19)",1))
		var sourceTemporary int64
		var sourceTemporaryName string
		if err:=conn.QueryRowContext(c.p.Ctx,"SELECT oid::bigint,nspname FROM pg_namespace WHERE oid=pg_my_temp_schema()").Scan(&sourceTemporary,&sourceTemporaryName);err!=nil {c.t.Fatal(err)}
		// Physical filenames are outside the embedded logical clone contract.
		// Derive the oracle's orphan-file diagnostic from independent catalog
		// identities, not from the error being asserted.
		const physicalIdentity = "SELECT d.oid::bigint,c.relfilenode::bigint FROM pg_database d CROSS JOIN pg_class c WHERE d.datname=current_database() AND c.relnamespace=$1::oid AND c.relname='dt_temporary' AND c.reltablespace=0 AND d.dattablespace=(SELECT oid FROM pg_tablespace WHERE spcname='pg_default')"
		var sourceDatabase, sourceFile int64
		if c.backend.PG {
			if err:=conn.QueryRowContext(c.p.Ctx,physicalIdentity,sourceTemporary).Scan(&sourceDatabase,&sourceFile);err!=nil {c.t.Fatal(err)}
			if sourceDatabase<=0 || sourceFile<=0 {c.t.Fatalf("invalid source physical identity: database=%d file=%d",sourceDatabase,sourceFile)}
		}
		c.remember("dt_self_clone");run(fixture.Prepared(fixture.Exec("CREATE DATABASE dt_self_clone TEMPLATE dt_self_source",0)))
		clone,_,cr:=c.pool("dt_self_clone","")
		cloneConn,err:=clone.Conn(c.p.Ctx);if err!=nil {c.t.Fatal(err)};defer cloneConn.Close()
		cloneRun:=func(s fixture.Step){fixture.RunStep(c.t,c.p.Ctx,cloneConn,cr,s)}
		cloneRun(fixture.Select("SELECT v FROM dt_permanent",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}))
		// A catalog row is not a physical session attachment. PostgreSQL copies
		// the TEMP metadata, but the destination starts without an own TEMP schema.
		cloneRun(fixture.Select("SELECT relname,relpersistence,relnamespace=$1::oid AS local_namespace,pg_is_other_temp_schema(relnamespace) AS other_temp FROM pg_class WHERE relname='dt_temporary'",[]string{"relname","relpersistence","local_namespace","other_temp"},[]string{"NAME","CHAR","BOOL","BOOL"},[][]any{{"dt_temporary","t",true,true}},sourceTemporary))
		unattached:=fixture.Select("SELECT pg_my_temp_schema()=0 AS unattached,to_regclass('pg_temp.dt_temporary') IS NULL AS unresolved",[]string{"unattached","unresolved"},[]string{"BOOL","BOOL"},[][]any{{true,true}})
		cloneRun(unattached);cloneRun(fixture.Prepared(unattached))
		foreignDiagnostic:=databaseCreateDiagnostic("0A000","cannot access temporary tables of other sessions")
		if c.backend.PG {
			owner,err:=strconv.ParseUint(strings.TrimPrefix(sourceTemporaryName,"pg_temp_"),10,32)
			if err!=nil || owner==0 || sourceTemporaryName!=fmt.Sprintf("pg_temp_%d",owner) {c.t.Fatalf("invalid source TEMP owner namespace %q",sourceTemporaryName)}
			var cloneDatabase, cloneFile int64
			if err:=cloneConn.QueryRowContext(c.p.Ctx,physicalIdentity,sourceTemporary).Scan(&cloneDatabase,&cloneFile);err!=nil {c.t.Fatal(err)}
			if cloneDatabase<=0 || cloneDatabase==sourceDatabase || cloneFile!=sourceFile {c.t.Fatalf("copied physical identities: source=(%d,%d) clone=(%d,%d)",sourceDatabase,sourceFile,cloneDatabase,cloneFile)}
			foreignDiagnostic=databaseCreateDiagnostic("58P01",fmt.Sprintf("could not open file \"base/%d/t%d_%d\": No such file or directory",cloneDatabase,owner,cloneFile))
		}
		foreign:=fixture.ErrorDiagnostic("SELECT v FROM "+pq.QuoteIdentifier(sourceTemporaryName)+".dt_temporary",foreignDiagnostic)
		foreign.SelectRows=true
		cloneRun(foreign)
		cloneRun(fixture.Exec("CREATE TEMP TABLE dt_temporary(v integer)",0))
		cloneRun(fixture.Exec("INSERT INTO dt_temporary VALUES(23)",1))
		cloneRun(fixture.Select("SELECT v FROM pg_temp.dt_temporary",[]string{"v"},[]string{"INT4"},[][]any{{int64(23)}}))
		cloneRun(fixture.Select("SELECT pg_my_temp_schema()<>$1::oid AS own_namespace FROM pg_namespace WHERE oid=pg_my_temp_schema()",[]string{"own_namespace"},[]string{"BOOL"},[][]any{{true}},sourceTemporary))
		run(fixture.Select("SELECT v FROM pg_temp.dt_temporary",[]string{"v"},[]string{"INT4"},[][]any{{int64(19)}}))
		other,_,_:=c.pool("dt_self_source","");if err:=other.PingContext(c.p.Ctx);err!=nil {c.t.Fatal(err)}
		c.remember("dt_self_retry");d:=databaseCreateDiagnostic("55006",`source database "dt_self_source" is being accessed by other users`);d.Detail="There is 1 other session using the database."
		run(fixture.ErrorDiagnostic("CREATE DATABASE dt_self_retry TEMPLATE dt_self_source",d));c.absent("dt_self_retry")
		if err:=other.Close();err!=nil {c.t.Fatal(err)}
		run(fixture.Exec("CREATE DATABASE dt_self_retry TEMPLATE dt_self_source",0))
		c.remember("dt_self_tx");run(fixture.Exec("BEGIN",0));run(fixture.ErrorDiagnostic("CREATE DATABASE dt_self_tx TEMPLATE dt_self_source",databaseCreateDiagnostic("25001","CREATE DATABASE cannot run inside a transaction block")));run(fixture.Exec("ROLLBACK",0));c.absent("dt_self_tx")
	})
}
