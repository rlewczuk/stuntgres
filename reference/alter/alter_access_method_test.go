package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func accessMethodScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	clean := fixture.AccessMethodReferenceCleanup(t, []string{"heap2", "heap3", "Heap 方法"})
	p := fixture.NewScenario(t)
	clean()
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer c.Close()
			for _, q := range []string{
				"CREATE ACCESS METHOD heap2 TYPE TABLE HANDLER heap_tableam_handler",
				"CREATE ACCESS METHOD heap3 TYPE TABLE HANDLER heap_tableam_handler",
			} { persistenceExec(t,p,b,c,q) }
			body(t,p,b,c)
		})
	}
}

// Resolve custom OIDs on each backend, never compare allocator sequences.
func accessMethodStep(name, method string) fixture.Step {
	if method == "" {
		return fixture.Select("SELECT relam::bigint FROM pg_class WHERE oid=$1::regclass", []string{"relam"}, []string{"INT8"}, [][]any{{int64(0)}},name)
	}
	return fixture.Select("SELECT a.amname,c.relam=a.oid AS exact FROM pg_class c JOIN pg_am a ON a.oid=c.relam WHERE c.oid=$1::regclass", []string{"amname","exact"}, []string{"NAME","BOOL"}, [][]any{{method,true}},name)
}

func accessMethodProperty(t *testing.T,p *fixture.Pair,b fixture.Backend,x fixture.Executor,name,method string) {
	t.Helper()
	persistenceRun(t,p,b,x,accessMethodStep(name,method))
	if method=="heap" {
		persistenceRun(t,p,b,x,fixture.Select("SELECT relam::bigint FROM pg_class WHERE oid=$1::regclass",[]string{"relam"},[]string{"INT8"},[][]any{{int64(2)}},name))
	}
}

func accessMethodRows(rows [][]any) fixture.Step {
	return fixture.Select("SELECT id,note FROM t ORDER BY id",[]string{"id","note"},[]string{"INT4","TEXT"},rows)
}

func accessMethodIdentity(t *testing.T,p *fixture.Pair,c *sql.Conn,name string) []any {
	t.Helper()
	var oid,typ,owner int64
	var persistence string
	var acl sql.NullString
	if err:=c.QueryRowContext(p.Ctx,"SELECT oid::bigint,reltype::bigint,relowner::bigint,relpersistence,relacl::text FROM pg_class WHERE oid=$1::regclass",name).Scan(&oid,&typ,&owner,&persistence,&acl);err!=nil {t.Fatal(err)}
	if oid<=0 || typ<=0 || owner<=0 {t.Fatal("invalid relation identity",oid,typ,owner)}
	return []any{oid,typ,owner,persistence,acl}
}

func TestReferenceAlterAccessMethodValues(t *testing.T) {
	accessMethodScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		for _,q:=range []string{"CREATE TABLE t(id int NOT NULL,note text,CONSTRAINT positive CHECK(id>0))","CREATE INDEX t_note ON t(note)"} {persistenceExec(t,p,b,c,q)}
		persistenceRun(t,p,b,c,fixture.Exec("INSERT INTO t VALUES(1,'one'),(2,NULL)",2))
		before:=accessMethodIdentity(t,p,c,"t")
		selectSQL,insertSQL:="SELECT id,note FROM t ORDER BY id","INSERT INTO t VALUES($1,$2)"
		selected,err:=c.PrepareContext(p.Ctx,selectSQL);if err!=nil {t.Fatal(err)};defer selected.Close()
		inserted,err:=c.PrepareContext(p.Ctx,insertSQL);if err!=nil {t.Fatal(err)};defer inserted.Close()
		// pq cursors hold an AccessShareLock until exhausted. Consume its server
		// result before AX; native detached Rows deliberately remain unread.
		retained,err:=selected.QueryContext(p.Ctx);if err!=nil {t.Fatal(err)};defer retained.Close()
		if b.PG { fixture.StringCoreRetainedRows(t,retained,nil,[]string{"id","note"},[]string{"INT4","TEXT"},[][]any{{int64(1),"one"},{int64(2),nil}},false) }
		want:=[][]any{{int64(1),"one"},{int64(2),nil}}
		for i,change:=range []struct{setup,sql,method string;prepared bool}{
			{"","ALTER TABLE t SET ACCESS METHOD heap2","heap2",false},
			{"","ALTER TABLE t SET ACCESS METHOD heap3","heap3",true},
			{"SET default_table_access_method=heap2","ALTER TABLE t SET ACCESS METHOD DEFAULT","heap2",true},
			{"SET default_table_access_method=heap","ALTER TABLE t SET ACCESS METHOD DEFAULT","heap",false},
			{"","ALTER TABLE t SET ACCESS METHOD heap, SET ACCESS METHOD heap","heap",true},
		} {
			if change.setup!="" {persistenceExec(t,p,b,c,change.setup)}
			step:=fixture.Exec(change.sql,0);if change.prepared {step=fixture.Prepared(step)};persistenceRun(t,p,b,c,step)
			accessMethodProperty(t,p,b,c,"t",change.method)
			if !reflect.DeepEqual(before,accessMethodIdentity(t,p,c,"t")) {t.Fatal("method changed unrelated identity")}
			persistenceRun(t,p,b,c,fixture.Select("SELECT indexdef FROM pg_indexes WHERE indexname='t_note'",[]string{"indexdef"},[]string{"TEXT"},[][]any{{"CREATE INDEX t_note ON public.t USING btree (note)"}}))
			persistenceRun(t,p,b,c,fixture.Select("SELECT conname,convalidated FROM pg_constraint WHERE conrelid='t'::regclass AND conname='positive'",[]string{"conname","convalidated"},[]string{"NAME","BOOL"},[][]any{{"positive",true}}))
			persistenceRun(t,p,b,fixture.HeldStatement{Executor:c,Statement:inserted,SQL:insertSQL},fixture.Exec(insertSQL,1,10+i,fmt.Sprint(i)))
			want=append(want,[]any{int64(10+i),fmt.Sprint(i)})
			data:=accessMethodRows(want)
			data.CheckMetadata=func(t *testing.T,rows *sql.Rows) {
				types,err:=rows.ColumnTypes();if err!=nil || len(types)!=2 {t.Fatal(types,err)}
				intScan:=reflect.TypeFor[int64]();if b.PG {intScan=reflect.TypeFor[int32]()}
				if types[0].ScanType()!=intScan || types[1].ScanType()!=reflect.TypeFor[string]() {t.Fatal("changed stored ScanType",types[0].ScanType(),types[1].ScanType())}
				for j,typ:=range types {
					nullable,known:=typ.Nullable()
					if known==b.PG || !b.PG && nullable!=(j==1) {t.Fatal("stored nullability",j,nullable,known)}
				}
			}
			persistenceRun(t,p,b,c,data)
			persistenceRun(t,p,b,fixture.HeldStatement{Executor:c,Statement:selected,SQL:selectSQL},data)
		}
		if !b.PG {fixture.StringCoreRetainedRows(t,retained,nil,[]string{"id","note"},[]string{"INT4","TEXT"},[][]any{{int64(1),"one"},{int64(2),nil}},false)}
		persistenceRun(t,p,b,c,fixture.Select("SELECT count(*) FROM pg_depend WHERE classid='pg_class'::regclass AND objid='t'::regclass AND refclassid='pg_am'::regclass",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
		defaultSQL:="ALTER TABLE t SET ACCESS METHOD DEFAULT"
		defaulted,err:=c.PrepareContext(p.Ctx,defaultSQL);if err!=nil {t.Fatal(err)};defer defaulted.Close()
		persistenceExec(t,p,b,c,"SET default_table_access_method=heap3")
		persistenceRun(t,p,b,fixture.HeldStatement{Executor:c,Statement:defaulted,SQL:defaultSQL},fixture.Exec(defaultSQL,0))
		accessMethodProperty(t,p,b,c,"t","heap3")
		persistenceExec(t,p,b,c,"ALTER TABLE t SET ACCESS METHOD heap2")
		persistenceRun(t,p,b,c,fixture.Select("SELECT d.deptype,a.amname FROM pg_depend d JOIN pg_am a ON a.oid=d.refobjid WHERE d.classid='pg_class'::regclass AND d.objid='t'::regclass AND d.refclassid='pg_am'::regclass",[]string{"deptype","amname"},[]string{"CHAR","NAME"},[][]any{{"n","heap2"}}))
	})
}
