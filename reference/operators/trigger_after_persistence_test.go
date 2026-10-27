package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func afterPersistence(t *testing.T, smoke bool) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t,"aft_saved")
	triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		userOperatorExec(t,p,b,c,
			"CREATE SCHEMA aft_saved",
			"CREATE TABLE aft_saved.data(id integer PRIMARY KEY,v integer)",
			"CREATE TABLE aft_saved.audit(op text,level text,id integer,v integer,n bigint)",
			"CREATE FUNCTION aft_saved.before_row() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v+1; RETURN NEW; END$$",
			"CREATE FUNCTION aft_saved.row_event() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO aft_saved.audit VALUES(TG_OP,TG_LEVEL,COALESCE(NEW.id,OLD.id),COALESCE(NEW.v,OLD.v),(SELECT count(*) FROM aft_saved.data)); IF TG_OP<>'DELETE' THEN NEW.v:=999; END IF; RETURN NULL; END$$",
			"CREATE FUNCTION aft_saved.statement_event() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO aft_saved.audit VALUES(TG_OP,TG_LEVEL,NULL,NULL,(SELECT count(*) FROM aft_saved.data)); RETURN NULL; END$$",
			"CREATE TRIGGER before_row BEFORE INSERT ON aft_saved.data FOR EACH ROW EXECUTE FUNCTION aft_saved.before_row()",
			"CREATE TRIGGER \"row ' quoted\" AFTER INSERT ON aft_saved.data FOR EACH ROW WHEN(NEW.v>0) EXECUTE FUNCTION aft_saved.row_event('x''y')",
			"CREATE TRIGGER row_update AFTER UPDATE ON aft_saved.data FOR EACH ROW WHEN(NEW.v>0) EXECUTE FUNCTION aft_saved.row_event()",
			"CREATE TRIGGER row_delete AFTER DELETE ON aft_saved.data FOR EACH ROW WHEN(OLD.v>0) EXECUTE FUNCTION aft_saved.row_event()",
			"CREATE TRIGGER statement AFTER INSERT OR UPDATE OR DELETE OR TRUNCATE ON aft_saved.data EXECUTE FUNCTION aft_saved.statement_event()")
		run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
		insert:="INSERT INTO aft_saved.data VALUES($1,$2) RETURNING id,v"
		held,err:=c.PrepareContext(p.Ctx,insert);if err!=nil {t.Fatal(err)};defer held.Close()
		update:="UPDATE aft_saved.data SET v=v WHERE id=$1 RETURNING id,v"
		u,err:=c.PrepareContext(p.Ctx,update);if err!=nil {t.Fatal(err)};defer u.Close()
		run(fixture.Select("SELECT count(*) AS n FROM aft_saved.audit",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))
		s:=triggerRows("INSERT INTO aft_saved.data VALUES(1,10),(2,20) RETURNING id,v",[]any{int64(1),int64(11)},[]any{int64(2),int64(21)});s.Bag=true;run(s)
		logs:=func(q string,rows ...[]any) fixture.Step {s:=fixture.Select(q,[]string{"op","level","id","v","n"},[]string{"TEXT","TEXT","INT4","INT4","INT8"},rows);s.Bag=true;return s}
		want:=[][]any{{"INSERT","ROW",int64(1),int64(11),int64(2)},{"INSERT","ROW",int64(2),int64(21),int64(2)},{"INSERT","STATEMENT",nil,nil,int64(2)}}
		run(logs("SELECT op,level,id,v,n FROM aft_saved.audit",want...))
		s=triggerRows(update);s.Args=[]any{999};fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:u,SQL:update},b.Recorder,s)
		want=append(want,[]any{"UPDATE","STATEMENT",nil,nil,int64(2)})
		if smoke {
			userOperatorExec(t,p,b,c,"CREATE FUNCTION aft_saved.truncate_fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO aft_saved.audit VALUES(TG_OP,TG_LEVEL,NULL,NULL,0); PERFORM 1/0; RETURN NULL; END$$","CREATE TRIGGER z AFTER TRUNCATE ON aft_saved.data EXECUTE FUNCTION aft_saved.truncate_fail()")
			d:=fixture.ControlsReferenceDiagnostic("22012","division by zero");d.Context="SQL statement \"SELECT 1/0\"\nPL/pgSQL function aft_saved.truncate_fail() line 1 at PERFORM"
			run(fixture.ErrorDiagnostic("TRUNCATE aft_saved.data",d))
			run(triggerRows("SELECT id,v FROM aft_saved.data ORDER BY id",[]any{int64(1),int64(11)},[]any{int64(2),int64(21)}))
			run(logs("SELECT op,level,id,v,n FROM aft_saved.audit",want...))
			userOperatorExec(t,p,b,c,"CREATE OR REPLACE FUNCTION aft_saved.truncate_fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$")
			run(fixture.Exec("TRUNCATE aft_saved.data",0))
			run(triggerRows("SELECT id,v FROM aft_saved.data"))
			want=append(want,[]any{"TRUNCATE","STATEMENT",nil,nil,int64(0)})
			run(fixture.Prepared(fixture.Exec("INSERT INTO aft_saved.data VALUES(1,10),(2,20)",2)))
			want=append(want,[]any{"INSERT","ROW",int64(1),int64(11),int64(2)},[]any{"INSERT","ROW",int64(2),int64(21),int64(2)},[]any{"INSERT","STATEMENT",nil,nil,int64(2)})
		}
		check:=func(conn *sql.Conn,backend fixture.Backend){
			fixture.RunStep(t,p.Ctx,conn,backend.Recorder,triggerRows("SELECT id,v FROM aft_saved.data ORDER BY id",[]any{int64(1),int64(11)},[]any{int64(2),int64(21)}))
			fixture.RunStep(t,p.Ctx,conn,backend.Recorder,logs("SELECT op,level,id,v,n FROM aft_saved.audit",want...))
			fixture.RunStep(t,p.Ctx,conn,backend.Recorder,fixture.Exec("BEGIN",0))
			s:=triggerRows(insert,[]any{int64(99),int64(41)});s.Args=[]any{99,40}
			if conn==c {fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:conn,Statement:held,SQL:insert},backend.Recorder,s)} else {fixture.RunStep(t,p.Ctx,conn,backend.Recorder,fixture.Prepared(s))}
			s=triggerRows(insert,[]any{int64(98),int64(41)});s.Args=[]any{98,40}
			fixture.RunStep(t,p.Ctx,conn,backend.Recorder,fixture.Prepared(s))
			replayed:=append([][]any(nil),want...)
			replayed=append(replayed,
				[]any{"INSERT","ROW",int64(99),int64(41),int64(3)},
				[]any{"INSERT","STATEMENT",nil,nil,int64(3)},
				[]any{"INSERT","ROW",int64(98),int64(41),int64(4)},
				[]any{"INSERT","STATEMENT",nil,nil,int64(4)})
			fixture.RunStep(t,p.Ctx,conn,backend.Recorder,fixture.Prepared(triggerRows("UPDATE aft_saved.data SET v=v+5 WHERE id=1 RETURNING id,v",[]any{int64(1),int64(16)})))
			replayed=append(replayed,[]any{"UPDATE","ROW",int64(1),int64(16),int64(4)},[]any{"UPDATE","STATEMENT",nil,nil,int64(4)})
			fixture.RunStep(t,p.Ctx,conn,backend.Recorder,triggerRows("DELETE FROM aft_saved.data WHERE id=2 RETURNING id,v",[]any{int64(2),int64(21)}))
			replayed=append(replayed,[]any{"DELETE","ROW",int64(2),int64(21),int64(3)},[]any{"DELETE","STATEMENT",nil,nil,int64(3)})
			fixture.RunStep(t,p.Ctx,conn,backend.Recorder,fixture.Exec("TRUNCATE aft_saved.data",0))
			replayed=append(replayed,[]any{"TRUNCATE","STATEMENT",nil,nil,int64(0)})
			fixture.RunStep(t,p.Ctx,conn,backend.Recorder,triggerRows("SELECT id,v FROM aft_saved.data"))
			fixture.RunStep(t,p.Ctx,conn,backend.Recorder,logs("SELECT op,level,id,v,n FROM aft_saved.audit",replayed...))
			fixture.RunStep(t,p.Ctx,conn,backend.Recorder,fixture.Exec("ROLLBACK",0))
			fixture.RunStep(t,p.Ctx,conn,backend.Recorder,logs("SELECT op,level,id,v,n FROM aft_saved.audit",want...))
		}
		parametersReplay(t,p,b,c,"aft_saved",check)
		s=triggerRows(insert,[]any{int64(3),int64(31)});s.Args=[]any{3,30};fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:held,SQL:insert},b.Recorder,s)
		want=append(want,[]any{"INSERT","ROW",int64(3),int64(31),int64(3)},[]any{"INSERT","STATEMENT",nil,nil,int64(3)})
		run(logs("SELECT op,level,id,v,n FROM aft_saved.audit",want...))
		if b.PG {return}
		old,err:=c.QueryContext(p.Ctx,"SELECT id,v FROM aft_saved.data ORDER BY id");if err!=nil {t.Fatal(err)};defer old.Close()
		var dump bytes.Buffer;if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil {t.Fatal(err)}
		bad:=dump.String()+"; CREATE TRIGGER bad AFTER INSERT ON aft_saved.data EXECUTE FUNCTION aft_saved.missing();"
		if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(bad));err==nil {t.Fatal("late invalid AFTER trigger accepted")}
		if err:=stuntgres.Restore(p.Ctx,p.Native,&plpgsqlFailReader{data:dump.Bytes()});!errors.Is(err,io.ErrUnexpectedEOF) {t.Fatal(err)}
		ctx,cancel:=context.WithCancel(p.Ctx);cancel()
		if err:=stuntgres.Restore(ctx,p.Native,bytes.NewReader(dump.Bytes()));!errors.Is(err,context.Canceled) {t.Fatal(err)}
		run(logs("SELECT op,level,id,v,n FROM aft_saved.audit",want...))
		for i,v:=range []int64{11,21,31} {if !old.Next(){t.Fatal(old.Err())};var id,got int64;if err:=old.Scan(&id,&got);err!=nil || id!=int64(i+1) || got!=v {t.Fatal(id,got,err)}}
		if old.Next() || old.Err()!=nil {t.Fatal("held rows",old.Err())}
		s=triggerRows(insert,[]any{int64(4),int64(41)});s.Args=[]any{4,40};fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:held,SQL:insert},b.Recorder,s)
	})
}
func TestReferenceTriggerAfterPersistence(t *testing.T) {afterPersistence(t,false)}
func TestReferenceTriggerAfterFeatureSmoke(t *testing.T) {afterPersistence(t,true)}

func TestReferenceTriggerAfterRetainedClonePersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t,"aft_clone_saved")
	triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		userOperatorExec(t,p,b,c,
			"CREATE SCHEMA aft_clone_saved",
			"CREATE EXTENSION unaccent SCHEMA aft_clone_saved",
			"CREATE TABLE aft_clone_saved.z_parent(id integer) PARTITION BY RANGE(id)",
			"CREATE TABLE aft_clone_saved.a_child PARTITION OF aft_clone_saved.z_parent FOR VALUES FROM(0) TO(100)",
			"CREATE TABLE aft_clone_saved.audit(rel text,level text,arg text,id integer)",
			"CREATE FUNCTION aft_clone_saved.log() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO aft_clone_saved.audit VALUES(TG_TABLE_NAME,TG_LEVEL,TG_ARGV[0],NEW.id); RETURN NULL; END$$",
			"CREATE TRIGGER changing AFTER INSERT ON aft_clone_saved.z_parent FOR EACH ROW WHEN(NEW.id>0) EXECUTE FUNCTION aft_clone_saved.log('old row')",
			"CREATE OR REPLACE TRIGGER changing AFTER INSERT ON aft_clone_saved.z_parent EXECUTE FUNCTION aft_clone_saved.log('new statement')",
			"CREATE TABLE aft_clone_saved.b_later(id integer)",
			"ALTER TABLE aft_clone_saved.z_parent ATTACH PARTITION aft_clone_saved.b_later FOR VALUES FROM(100) TO(200)",
			"ALTER TRIGGER changing ON aft_clone_saved.a_child DEPENDS ON EXTENSION unaccent")
		check:=func(conn *sql.Conn,backend fixture.Backend){
			run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,conn,backend.Recorder,s)}
			run(fixture.Select("SELECT event_object_table,action_orientation FROM information_schema.triggers WHERE trigger_schema='aft_clone_saved' ORDER BY event_object_table",[]string{"event_object_table","action_orientation"},[]string{"NAME","VARCHAR"},[][]any{{"a_child","ROW"},{"z_parent","STATEMENT"}}))
			run(triggerDDLX("aft_clone_saved.a_child","unaccent",1))
			run(fixture.Exec("BEGIN",0))
			run(fixture.Prepared(fixture.Exec("INSERT INTO aft_clone_saved.z_parent VALUES(0),(1),(101)",3)))
			run(fixture.Select("SELECT rel,level,arg,id FROM aft_clone_saved.audit ORDER BY rel",[]string{"rel","level","arg","id"},[]string{"TEXT","TEXT","TEXT","INT4"},[][]any{{"a_child","ROW","old row",int64(1)},{"z_parent","STATEMENT","new statement",nil}}))
			run(fixture.Exec("ROLLBACK",0))
			run(fixture.Select("SELECT count(*) AS n FROM aft_clone_saved.audit",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))
		}
		parametersReplay(t,p,b,c,"aft_clone_saved",check)
		if b.PG {return}
		var dump bytes.Buffer
		if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil {t.Fatal(err)}
		// The ordinary SQL preserves firing on PostgreSQL; clone LINK restoration
		// is native-only and must also preserve drop/detach dependency behavior.
		run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
		run(fixture.Exec("CREATE OR REPLACE TRIGGER changing AFTER INSERT ON aft_clone_saved.z_parent FOR EACH ROW EXECUTE FUNCTION aft_clone_saved.log('reverse row')",0))
		run(fixture.Select("SELECT event_object_table,action_orientation FROM information_schema.triggers WHERE trigger_schema='aft_clone_saved' ORDER BY event_object_table",[]string{"event_object_table","action_orientation"},[]string{"NAME","VARCHAR"},[][]any{{"a_child","ROW"},{"b_later","ROW"},{"z_parent","ROW"}}))
		run(fixture.Prepared(fixture.Exec("INSERT INTO aft_clone_saved.z_parent VALUES(3),(103)",2)))
		run(fixture.Select("SELECT rel,level,arg,id FROM aft_clone_saved.audit ORDER BY rel",[]string{"rel","level","arg","id"},[]string{"TEXT","TEXT","TEXT","INT4"},[][]any{{"a_child","ROW","reverse row",int64(3)},{"b_later","ROW","reverse row",int64(103)}}))
		if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));err!=nil {t.Fatal(err)}
		check(c,b)
		for _,q:=range []string{"ALTER TABLE aft_clone_saved.z_parent DETACH PARTITION aft_clone_saved.a_child","DROP TRIGGER changing ON aft_clone_saved.z_parent"} {
			run(fixture.Exec(q,0))
			run(fixture.Select("SELECT trigger_name FROM information_schema.triggers WHERE event_object_schema='aft_clone_saved' AND event_object_table='a_child'",[]string{"trigger_name"},[]string{"NAME"},nil))
			run(fixture.Exec("INSERT INTO aft_clone_saved.a_child VALUES(2)",1))
			run(fixture.Select("SELECT count(*) AS n FROM aft_clone_saved.audit",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))
			if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));err!=nil {t.Fatal(err)}
			check(c,b)
		}
		text:=dump.String()
		start:=strings.Index(text,"-- stuntgres-trigger-clone-v1 ")
		if start<0 {t.Fatal("missing retained clone annotation")}
		end:=strings.IndexByte(text[start:],'\n')
		if end<0 {t.Fatal("unterminated retained clone annotation")}
		line:=text[start:start+end+1]
		var record map[string]string
		payload,err:=base64.StdEncoding.DecodeString(strings.TrimSpace(strings.TrimPrefix(line,"-- stuntgres-trigger-clone-v1 ")))
		if err!=nil {t.Fatal(err)}
		if err:=json.Unmarshal(payload,&record);err!=nil {t.Fatal(err)}
		rewrite:=func(change func(map[string]string)) string {
			copy:=make(map[string]string,len(record));for k,v:=range record {copy[k]=v};change(copy)
			payload,err:=json.Marshal(copy);if err!=nil {t.Fatal(err)}
			annotation:="-- stuntgres-trigger-clone-v1 "+base64.StdEncoding.EncodeToString(payload)+"\n"
			return strings.Replace(text,line,annotation,1)
		}
		invalids:=[]string{
			strings.Replace(text,line,"-- stuntgres-trigger-clone-v1 malformed\n",1),
			strings.Replace(text,line,line+line,1),
			text+line,
			rewrite(func(r map[string]string){r["child_table"]="b_later"}),
			rewrite(func(r map[string]string){r["parent_trigger"]="missing"}),
			rewrite(func(r map[string]string){r["parent_table"]="missing"}),
			rewrite(func(r map[string]string){r["parent_table"]="other"})+
				"CREATE TABLE aft_clone_saved.other(id integer) PARTITION BY RANGE(id); CREATE TRIGGER changing AFTER INSERT ON aft_clone_saved.other EXECUTE FUNCTION aft_clone_saved.log('unrelated');",
			text+"CREATE OR REPLACE TRIGGER changing AFTER INSERT ON aft_clone_saved.a_child FOR EACH ROW WHEN(NEW.id>50) EXECUTE FUNCTION aft_clone_saved.log('replacement');",
			text+"CREATE OR REPLACE TRIGGER changing AFTER INSERT ON aft_clone_saved.a_child EXECUTE FUNCTION aft_clone_saved.log('wrong kind');",
			text+"CREATE OR REPLACE TRIGGER changing AFTER INSERT ON aft_clone_saved.z_parent FOR EACH ROW EXECUTE FUNCTION aft_clone_saved.log('wrong parent kind');",
			text+"CREATE OR REPLACE "+strings.TrimPrefix(record["sql"],"CREATE "),
			text+"DROP TABLE aft_clone_saved.a_child; CREATE TABLE aft_clone_saved.a_child PARTITION OF aft_clone_saved.z_parent FOR VALUES FROM(0) TO(100); "+record["sql"],
		}
		// The rejected source has no rows. Distinct live data, eager rows and a
		// held writer must survive every failure, not merely remain queryable.
		run(fixture.Exec("INSERT INTO aft_clone_saved.z_parent VALUES(7),(107)",2))
		heldSQL:="INSERT INTO aft_clone_saved.z_parent VALUES($1) RETURNING id"
		held,err:=c.PrepareContext(p.Ctx,heldSQL);if err!=nil {t.Fatal(err)};defer held.Close()
		old,err:=c.QueryContext(p.Ctx,"SELECT id FROM aft_clone_saved.z_parent ORDER BY id");if err!=nil {t.Fatal(err)};defer old.Close()
		saved:=func(extra bool) fixture.Step {
			rows:=[][]any{{"a_child","ROW","old row",int64(7)},{"z_parent","STATEMENT","new statement",nil}}
			if extra {rows=append(rows,[]any{"a_child","ROW","old row",int64(8)},[]any{"z_parent","STATEMENT","new statement",nil})}
			s:=fixture.Select("SELECT rel,level,arg,id FROM aft_clone_saved.audit",[]string{"rel","level","arg","id"},[]string{"TEXT","TEXT","TEXT","INT4"},rows);s.Bag=true;return s
		}
		for i,invalid:=range invalids {
			if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(invalid));err==nil {t.Fatalf("invalid retained clone case %d accepted",i)}
			run(fixture.Select("SELECT id FROM aft_clone_saved.z_parent ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(7)},{int64(107)}}))
			run(saved(false))
			run(fixture.Select("SELECT event_object_table,action_orientation FROM information_schema.triggers WHERE trigger_schema='aft_clone_saved' ORDER BY event_object_table",[]string{"event_object_table","action_orientation"},[]string{"NAME","VARCHAR"},[][]any{{"a_child","ROW"},{"z_parent","STATEMENT"}}))
			run(fixture.Exec("BEGIN",0))
			s:=fixture.Select(heldSQL,[]string{"id"},[]string{"INT4"},[][]any{{int64(8)}},8)
			fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:held,SQL:heldSQL},b.Recorder,s)
			run(saved(true))
			run(fixture.Exec("ROLLBACK",0))
		}
		for _,want:=range []int64{7,107} {
			if !old.Next(){t.Fatal("lost held rows",old.Err())}
			var id int64;if err:=old.Scan(&id);err!=nil || id!=want {t.Fatal(id,err)}
		}
		if old.Next() || old.Err()!=nil {t.Fatal("held rows",old.Err())}
	})
}
