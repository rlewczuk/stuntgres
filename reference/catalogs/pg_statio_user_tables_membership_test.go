package reference_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatioUserTablesMembership(t *testing.T) {
	fixture.SchemaReferenceCleanup(t,"statio_user_members")
	p:=fixture.NewScenario(t)
	for _,b:=range p.Backends() {t.Run(b.Name,func(t *testing.T) {
		a,peer:=fixture.LockConnections(t,p.Ctx,b.DB)
		for _,q:=range []string{
			"CREATE SCHEMA statio_user_members",
			"CREATE TABLE statio_user_members.ordinary(k integer)",
			"CREATE UNLOGGED TABLE statio_user_members.unlogged(k integer)",
			"CREATE TABLE statio_user_members.parent(k integer) PARTITION BY RANGE(k)",
			"CREATE TABLE statio_user_members.leaf PARTITION OF statio_user_members.parent FOR VALUES FROM(0) TO(10)",
			"CREATE MATERIALIZED VIEW statio_user_members.materialized AS SELECT k FROM statio_user_members.ordinary",
			"CREATE VIEW statio_user_members.not_table AS SELECT k FROM statio_user_members.ordinary",
			"CREATE SEQUENCE statio_user_members.not_heap",
			"CREATE TYPE statio_user_members.not_storage AS (k integer)",
			"CREATE INDEX statio_user_member_index ON statio_user_members.ordinary(k)",
			"CREATE TEMP TABLE statio_user_member_temp(k integer)",
			"CREATE FOREIGN DATA WRAPPER statio_user_fdw",
			"CREATE SERVER statio_user_server FOREIGN DATA WRAPPER statio_user_fdw",
			"CREATE FOREIGN TABLE statio_user_members.foreign_t(k integer) SERVER statio_user_server",
		} {statioSequenceExec(t,p,b,a,q)}
		defer func(){ctx,cancel:=context.WithTimeout(context.Background(),fixture.Liveness);defer cancel();if _,err:=a.ExecContext(ctx,"DROP FOREIGN DATA WRAPPER statio_user_fdw CASCADE");err!=nil {t.Error("foreign fixture cleanup",err)}}()
		want:=map[int64][2]string{}
		for _,name:=range []string{"ordinary","unlogged","leaf","materialized"} {want[statioSequenceOID(t,p,a,"statio_user_members."+name)]=[2]string{"statio_user_members",name}}
		temp:=statioSequenceOID(t,p,a,"pg_temp.statio_user_member_temp")
		var tempSchema string
		if err:=a.QueryRowContext(p.Ctx,"SELECT n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid=$1::oid",temp).Scan(&tempSchema);err!=nil {t.Fatal(err)}
		want[temp]=[2]string{tempSchema,"statio_user_member_temp"}
		q:="SELECT * FROM pg_statio_user_tables WHERE schemaname='statio_user_members' OR relname='statio_user_member_temp'"
		for _,prepared:=range []bool{false,true} {
			var e fixture.Executor=peer
			if prepared {stmt,err:=peer.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer stmt.Close();e=fixture.HeldStatement{Executor:peer,Statement:stmt,SQL:q}}
			got:=map[int64][2]string{}
			for _,r:=range statioTableRows(t,p,b,e,q) {statioTableAbsentToast(t,r);if _,ok:=got[r.oid];ok {t.Fatal("duplicate",r)};got[r.oid]=[2]string{r.schema,r.name}}
			if !reflect.DeepEqual(got,want) {t.Fatal("complete unordered actual membership",got,want)}
		}
		fixture.RunStep(t,p.Ctx,peer,b.Recorder,fixture.Select("SELECT count(*) FROM pg_statio_user_tables WHERE schemaname IN ('pg_catalog','information_schema') OR schemaname ~ '^pg_toast'",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
		statioSequenceExec(t,p,b,a,"BEGIN");defer a.ExecContext(context.Background(),"ROLLBACK")
		statioSequenceExec(t,p,b,a,"CREATE TABLE statio_user_members.private_birth(k integer)")
		private:=statioSequenceOID(t,p,a,"statio_user_members.private_birth")
		statioUserTableOne(t,p,b,a,private)
		if rows:=statioTableRows(t,p,b,peer,statioUserTableProbe,private);len(rows)!=0 {t.Fatal("uncommitted identity leaked",rows)}
		statioSequenceExec(t,p,b,a,"ROLLBACK")
		if rows:=statioTableRows(t,p,b,a,statioUserTableProbe,private);len(rows)!=0 {t.Fatal("aborted identity retained",rows)}
		{
			statioSequenceExec(t,p,b,peer,"BEGIN")
			defer peer.ExecContext(context.Background(),"ROLLBACK")
			statioSequenceExec(t,p,b,peer,"SET LOCAL stats_fetch_consistency=snapshot")
			userHeld,userStmt:=statioUserTableHeld(t,p,peer);defer userStmt.Close()
			allStmt,err:=peer.PrepareContext(p.Ctx,statioTableProbe);if err!=nil {t.Fatal(err)};defer allStmt.Close()
			allHeld:=fixture.HeldStatement{Executor:peer,Statement:allStmt,SQL:statioTableProbe}
			for oid:=range want {
				user:=statioUserTableCompanions(t,p,b,peer,oid)
				if got:=statioUserTableOne(t,p,b,userHeld,oid);got!=user {t.Fatal("held named USER",got,user)}
				if got:=statioTableOne(t,p,b,allHeld,oid);got!=user {t.Fatal("held named ALL",got,user)}
			}
			statioSequenceExec(t,p,b,peer,"ROLLBACK")
		}
	})}
}

