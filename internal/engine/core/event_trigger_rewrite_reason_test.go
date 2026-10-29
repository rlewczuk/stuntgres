package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/table"
)

func TestEventTriggerRewriteReasonMask(t *testing.T) {
	for _,tc:=range []struct{name string; transforms []index.ColumnTransformation; stored []int32; perRow bool; want int32}{
		{name:"none"},
		{name:"nonrewrite_type",transforms:[]index.ColumnTransformation{{}}},
		{name:"default",perRow:true,want:2},
		{name:"generated",stored:[]int32{2},want:2},
		{name:"type",transforms:[]index.ColumnTransformation{{EventRewrite:true}},want:4},
		{name:"multiple_types",transforms:[]index.ColumnTransformation{{EventRewrite:true},{EventRewrite:true}},want:4},
		{name:"combined",transforms:[]index.ColumnTransformation{{EventRewrite:true}},perRow:true,stored:[]int32{2},want:6},
	} {
		t.Run(tc.name,func(t *testing.T) {
			if got:=table.AlterEventRewriteReason(tc.transforms,tc.stored,tc.perRow); got!=tc.want { t.Fatal("reason",got,"want",tc.want) }
		})
	}
	// Exercise the real preparation/physical owner, retaining committed roots
	// across Execute and a genuine terminal Finish veto. Empty stored tables
	// still notify; a partition root never gains invented storage identity.
	for _,tc:=range []struct{name,definition,action string; want int64}{
		{"persistence","(v integer)","SET UNLOGGED",1},
		{"persistence_noop","(v integer)","SET LOGGED",0},
		{"method","(v integer)","SET ACCESS METHOD et_mask_heap",8},
		{"method_noop","(v integer)","SET ACCESS METHOD heap",0},
		{"partition_method","(v integer) PARTITION BY RANGE(v)","SET ACCESS METHOD et_mask_heap",0},
		{"all","(v integer)","SET UNLOGGED, ADD extra bigint DEFAULT nextval('et_mask_seq'), ALTER v TYPE bigint, SET ACCESS METHOD et_mask_heap",15},
	} {
		t.Run("owner/"+tc.name,func(t *testing.T) {
			e:=engine.New()
			actor:=enginetest.StartSession(t,e,nil)
			for _,q:=range []string{
				"CREATE ACCESS METHOD et_mask_heap TYPE TABLE HANDLER heap_tableam_handler",
				"CREATE SEQUENCE et_mask_seq",
				"CREATE TABLE et_mask_t"+tc.definition,
				"CREATE TABLE et_mask_audit(correct_target boolean,reason integer)",
				"CREATE FUNCTION et_mask_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO et_mask_audit VALUES(pg_event_trigger_table_rewrite_oid()='et_mask_t'::regclass,pg_event_trigger_table_rewrite_reason()); END$$",
				"CREATE EVENT TRIGGER et_mask ON table_rewrite EXECUTE FUNCTION et_mask_fn()",
			} { enginetest.SessionSQL(t,actor,q) }
			target:=enginetest.RoleTableRef(t,e,"et_mask_t")
			audit:=enginetest.RoleTableRef(t,e,"et_mask_audit")
			before,frontier:=e.Catalog.State,e.Visibility.CommitSeq()
			store,auditStore:=e.Storage.Relations[target.Address],e.Storage.Relations[audit.Address]
			op,err:=actor.Acquire(context.Background()); if err!=nil { t.Fatal(err) }; defer op.Done()
			scope,err:=e.StartStatement(op); if err!=nil { t.Fatal(err) }
			defer scope.Finish(errors.New("unfinished mask proof"))
			if _,err:=scope.Execute(enginetest.Statement(t,"ALTER TABLE et_mask_t "+tc.action),0,nil); err!=nil { t.Fatal(err) }
			rows:=scope.Stage.Rows[audit.Address]
			if tc.want==0 {
				if len(rows)!=0 { t.Fatal("metadata-only action notified",rows) }
			} else {
				if len(rows)!=1 || len(rows[0].Values)!=2 { t.Fatal("private callback row",rows) }
				targetOK,targetErr:=rows[0].Values[0].Boolean()
				reason,reasonErr:=rows[0].Values[1].Integer()
				if targetErr!=nil || reasonErr!=nil || !targetOK || reason!=tc.want { t.Fatal("private live pair",targetOK,reason,targetErr,reasonErr) }
			}
			if e.Catalog.State!=before || e.Visibility.CommitSeq()!=frontier || e.Storage.Relations[target.Address]!=store || e.Storage.Relations[audit.Address]!=auditStore || len(auditStore.Versions)!=0 {
				t.Fatal("physical preparation escaped private publication")
			}
			veto:=errors.New("mask Finish veto")
			if err:=scope.Finish(veto); err!=veto { t.Fatal(err) }
			if e.Catalog.State!=before || e.Visibility.CommitSeq()!=frontier || e.Storage.Relations[target.Address]!=store || e.Storage.Relations[audit.Address]!=auditStore || len(auditStore.Versions)!=0 {
				t.Fatal("terminal veto published rewrite prefix")
			}
		})
	}
}
