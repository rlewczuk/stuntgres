package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseTemplateOptions(t *testing.T) {
	databaseCreateScenario(t,func(c *databaseCreateCase){
		c.run(fixture.Exec("CREATE ROLE dc_owner",0));c.run(fixture.Exec("CREATE ROLE dc_reader",0))
		c.create("dt_option_source","IS_TEMPLATE true CONNECTION LIMIT 2",false)
		source,_,rec:=c.pool("dt_option_source","")
		templateRun(c,source,rec,fixture.Exec("CREATE SCHEMA dt_owned AUTHORIZATION dc_owner",0),fixture.Exec("CREATE TABLE dt_owned.t(v integer)",0),fixture.Exec("INSERT INTO dt_owned.t VALUES(7)",1),fixture.Exec("ALTER TABLE dt_owned.t OWNER TO dc_owner",0),fixture.Exec("GRANT SELECT ON dt_owned.t TO dc_reader",0))
		c.run(fixture.Exec("GRANT CREATE ON DATABASE dt_option_source TO dc_reader",0));c.run(fixture.Exec("ALTER DATABASE dt_option_source SET application_name='source_only'",0))
		if err:=source.Close();err!=nil {c.t.Fatal(err)}
		actor:=c.scalar("SELECT current_user")
		for i,strategy:=range []string{"wal_log","FiLe_CoPy"} {
			name:=[]string{"dt_option_clone","dt_option_file"}[i]
			c.create(name,"TEMPLATE dt_option_source STRATEGY "+strategy,i==1)
			c.run(fixture.Select("SELECT r.rolname,d.datacl,d.datistemplate,d.datallowconn,d.datconnlimit FROM pg_database d JOIN pg_roles r ON r.oid=d.datdba WHERE d.datname=$1",[]string{"rolname","datacl","datistemplate","datallowconn","datconnlimit"},[]string{"NAME","_ACLITEM","BOOL","BOOL","INT4"},[][]any{{actor,nil,false,true,int64(-1)}},name))
			clone,_,cr:=c.pool(name,"")
			templateRun(c,clone,cr,fixture.Select("SELECT v FROM dt_owned.t",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}),fixture.Select("SELECT r.rolname,has_table_privilege('dc_reader','dt_owned.t','SELECT') AS reader FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_roles r ON r.oid=c.relowner WHERE n.nspname='dt_owned' AND c.relname='t'",[]string{"rolname","reader"},[]string{"NAME","BOOL"},[][]any{{"dc_owner",true}}),fixture.Select("SELECT r.rolname FROM pg_namespace n JOIN pg_roles r ON r.oid=n.nspowner WHERE n.nspname='dt_owned'",[]string{"rolname"},[]string{"NAME"},[][]any{{"dc_owner"}}),fixture.Select("SELECT s.setconfig FROM pg_db_role_setting s JOIN pg_database d ON d.oid=s.setdatabase WHERE d.datname=current_database()",[]string{"setconfig"},[]string{"_TEXT"},nil))
		}
		c.create("dt_option_explicit","TEMPLATE dt_option_source OWNER dc_owner IS_TEMPLATE true ALLOW_CONNECTIONS false CONNECTION LIMIT 0",false)
		metadata:=fixture.Select("SELECT r.rolname,d.datistemplate,d.datallowconn,d.datconnlimit FROM pg_database d JOIN pg_roles r ON r.oid=d.datdba WHERE d.datname='dt_option_explicit'",[]string{"rolname","datistemplate","datallowconn","datconnlimit"},[]string{"NAME","BOOL","BOOL","INT4"},[][]any{{"dc_owner",true,false,int64(0)}})
		c.run(metadata)
		peer,_,pr:=c.pool("dt_option_clone","");templateRun(c,peer,pr,fixture.Prepared(metadata))
		for _,tc:=range []struct{name,options,code,message,hint string}{
			{"dt_bad_strategy","STRATEGY bad","22023",`invalid create database strategy "bad"`,`Valid strategies are "wal_log" and "file_copy".`},
			{"dt_bad_encoding","ENCODING 'LATIN1'","22023","new encoding (LATIN1) is incompatible with the encoding of the template database (UTF8)","Use the same encoding as in the template database, or use template0 as template."},
		} {c.remember(tc.name);d:=databaseCreateDiagnostic(tc.code,tc.message);d.Hint=tc.hint;c.run(fixture.ErrorDiagnostic("CREATE DATABASE "+tc.name+" TEMPLATE dt_option_source "+tc.options,d));c.absent(tc.name)}
		// Native controls retain the destination's own policy, never the source's.
		if !c.backend.PG {
			c.create("dt_policy_control","TEMPLATE dt_option_source IS_TEMPLATE true CONNECTION LIMIT 3",false)
			db,raw,rr:=c.pool("dt_policy_control","");var dump bytes.Buffer
			if err:=stuntgres.Dump(c.p.Ctx,raw,&dump);err!=nil {c.t.Fatal(err)}
			for _,restore:=range []bool{false,true} {
				var err error;if restore {err=stuntgres.Restore(c.p.Ctx,raw,bytes.NewReader(dump.Bytes()))} else {err=stuntgres.Reset(c.p.Ctx,raw)};if err!=nil {c.t.Fatal(err)}
				templateRun(c,db,rr,fixture.Select("SELECT datistemplate,datallowconn,datconnlimit FROM pg_database WHERE datname=current_database()",[]string{"datistemplate","datallowconn","datconnlimit"},[]string{"BOOL","BOOL","INT4"},[][]any{{true,true,int64(3)}}))
			}
		}
	})
}
