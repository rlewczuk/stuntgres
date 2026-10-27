package reference_test

import (
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGTriggerForeignKeyMetadata(t *testing.T) {
	t.Run("SelfReferenceControlsVisibility",pgTriggerFKControls)
	for _,tc:=range []struct{action,deleteFunction,updateFunction string;deleteOID,updateOID int64}{
		{"NO ACTION","RI_FKey_noaction_del","RI_FKey_noaction_upd",1654,1655},
		{"RESTRICT","RI_FKey_restrict_del","RI_FKey_restrict_upd",1648,1649},
		{"CASCADE","RI_FKey_cascade_del","RI_FKey_cascade_upd",1646,1647},
		{"SET NULL","RI_FKey_setnull_del","RI_FKey_setnull_upd",1650,1651},
		{"SET DEFAULT","RI_FKey_setdefault_del","RI_FKey_setdefault_upd",1652,1653},
	} {
		t.Run(tc.action,func(t *testing.T) {
			p:=fixture.NewScenario(t)
			for _,b:=range p.Backends() {
				t.Run(b.Name,func(t *testing.T) {
					pgTriggerExec(t,p,b,b.DB,"CREATE TABLE pgt_fk_parent(id integer PRIMARY KEY)")
					pgTriggerExec(t,p,b,b.DB,fmt.Sprintf("CREATE TABLE pgt_fk_child(id integer CONSTRAINT pgt_fk REFERENCES pgt_fk_parent(id) ON DELETE %s ON UPDATE %s)",tc.action,tc.action))
					query:="SELECT c.relname,t.tgtype,t.tgfoid,p.proname,t.tgenabled,t.tgisinternal,t.tgparentid,t.tgconstraint=f.oid AS constraint_link,t.tgconstrindid=f.conindid AS index_link,opposite.relname AS opposite,t.tgdeferrable,t.tginitdeferred,t.tgnargs,t.tgattr::text AS attrs,encode(t.tgargs,'hex') AS args,t.tgqual,t.tgoldtable,t.tgnewtable FROM pg_trigger t JOIN pg_constraint f ON f.oid=t.tgconstraint JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_class opposite ON opposite.oid=t.tgconstrrelid JOIN pg_proc p ON p.oid=t.tgfoid WHERE f.conname=$1::name ORDER BY c.relname,t.tgtype"
					columns:=[]string{"relname","tgtype","tgfoid","proname","tgenabled","tgisinternal","tgparentid","constraint_link","index_link","opposite","tgdeferrable","tginitdeferred","tgnargs","attrs","args","tgqual","tgoldtable","tgnewtable"}
					types:=[]string{"NAME","INT2","OID","NAME","CHAR","BOOL","OID","BOOL","BOOL","NAME","BOOL","BOOL","INT2","TEXT","TEXT","PG_NODE_TREE","NAME","NAME"}
					row:=func(relation string,bits,oid int64,function,opposite string) []any { return []any{relation,bits,oid,function,"O",true,int64(0),true,true,opposite,false,false,int64(0),"","",nil,nil,nil} }
					rows:=[][]any{row("pgt_fk_child",5,1644,"RI_FKey_check_ins","pgt_fk_parent"),row("pgt_fk_child",17,1645,"RI_FKey_check_upd","pgt_fk_parent"),row("pgt_fk_parent",9,tc.deleteOID,tc.deleteFunction,"pgt_fk_child"),row("pgt_fk_parent",17,tc.updateOID,tc.updateFunction,"pgt_fk_child")}
					step:=fixture.Select(query,columns,types,rows,"pgt_fk")
					identities:=pgTriggerFKIDs(t,p,b,b.DB,"pgt_fk")
					pgTriggerBoth(t,p,b,step)
					stmt,err:=b.DB.PrepareContext(p.Ctx,query);if err!=nil { t.Fatal(err) };defer stmt.Close()
					held:=fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:query}
					fixture.PgDescriptionExecutorRows(t,p,b,held,step)
					missing:=step;missing.Args=[]any{"missing"};missing.Rows=[][]any{}
					fixture.PgDescriptionExecutorRows(t,p,b,held,missing)
					fixture.PgDescriptionExecutorRows(t,p,b,held,step)
					pgTriggerBoth(t,p,b,fixture.Select("SELECT relname,relhastriggers FROM pg_class WHERE relname IN ('pgt_fk_child','pgt_fk_parent') ORDER BY relname",[]string{"relname","relhastriggers"},[]string{"NAME","BOOL"},[][]any{{"pgt_fk_child",true},{"pgt_fk_parent",true}}))
					for _,function:=range []struct{oid int64;name string}{{1644,"RI_FKey_check_ins"},{1645,"RI_FKey_check_upd"},{tc.deleteOID,tc.deleteFunction},{tc.updateOID,tc.updateFunction}} {
						pgTriggerBoth(t,p,b,fixture.Select("SELECT p.provolatile,p.proparallel,p.proisstrict,p.procost,p.prorows,p.pronargs,p.prorettype,p.prosrc,l.lanname,p.proargtypes::text AS args,p.pronargdefaults,p.proretset,p.prosecdef,p.proleakproof FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.oid=$1::oid",[]string{"provolatile","proparallel","proisstrict","procost","prorows","pronargs","prorettype","prosrc","lanname","args","pronargdefaults","proretset","prosecdef","proleakproof"},[]string{"CHAR","CHAR","BOOL","FLOAT4","FLOAT4","INT2","OID","TEXT","NAME","TEXT","INT2","BOOL","BOOL","BOOL"},[][]any{{"v","s",true,float64(1),float64(0),int64(0),int64(2279),function.name,"internal","",int64(0),false,false,false}},function.oid))
					}
					violation:=fixture.ForeignKeyChild("pgt_fk_child","pgt_fk","Key (id)=(999) is not present in table \"pgt_fk_parent\".")
					for _,prepared:=range []bool{false,true} { fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.ForeignKeyFail("INSERT INTO pgt_fk_child VALUES(999)",violation,prepared)) }
					fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("INSERT INTO pgt_fk_parent VALUES(1)",1))
					fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("INSERT INTO pgt_fk_child VALUES(1)",1))
					if tc.action=="CASCADE" {
						fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("DELETE FROM pgt_fk_parent WHERE id=1",1))
						pgTriggerBoth(t,p,b,fixture.Select("SELECT count(*) AS n FROM pgt_fk_child",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))
					} else if tc.action=="RESTRICT" {
						violation:=fixture.ForeignKeyError("23001",`update or delete on table "pgt_fk_parent" violates RESTRICT setting of foreign key constraint "pgt_fk" on table "pgt_fk_child"`,"Key (id)=(1) is referenced from table \"pgt_fk_child\".","pgt_fk_child","pgt_fk")
						for _,prepared:=range []bool{false,true} { fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.ForeignKeyFail("DELETE FROM pgt_fk_parent WHERE id=1",violation,prepared)) }
						pgTriggerBoth(t,p,b,fixture.Select("SELECT count(*) AS n FROM pgt_fk_child",[]string{"n"},[]string{"INT8"},[][]any{{int64(1)}}))
					}
					fixture.PgDescriptionExecutorRows(t,p,b,held,step)
					pgTriggerExec(t,p,b,b.DB,"ALTER TABLE pgt_fk_child RENAME CONSTRAINT pgt_fk TO pgt_fk_renamed")
					if renamed:=pgTriggerFKIDs(t,p,b,b.DB,"pgt_fk_renamed");!reflect.DeepEqual(identities,renamed) { t.Fatal("rename replaced persisted identities",identities,renamed) }
					step.Args=[]any{"pgt_fk_renamed"}
					fixture.PgDescriptionExecutorRows(t,p,b,held,step)
					pgTriggerExec(t,p,b,b.DB,"ALTER TABLE pgt_fk_child DROP CONSTRAINT pgt_fk_renamed")
					step.Rows=[][]any{}
					fixture.PgDescriptionExecutorRows(t,p,b,held,step)
				})
			}
		})
	}
}
