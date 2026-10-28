package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterAccessMethodHierarchy(t *testing.T) {
	accessMethodScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		persistenceExec(t,p,b,c,"CREATE TABLE p(id int) PARTITION BY RANGE(id)")
		persistenceExec(t,p,b,c,"CREATE TABLE p0 PARTITION OF p FOR VALUES FROM(0) TO(10)")
		accessMethodProperty(t,p,b,c,"p","")
		persistenceExec(t,p,b,c,"ALTER TABLE p SET ACCESS METHOD heap2")
		persistenceExec(t,p,b,c,"CREATE TABLE p1 PARTITION OF p FOR VALUES FROM(10) TO(20)")
		accessMethodProperty(t,p,b,c,"p","heap2")
		accessMethodProperty(t,p,b,c,"p0","heap")
		accessMethodProperty(t,p,b,c,"p1","heap2")
		persistenceExec(t,p,b,c,"SET default_table_access_method=heap3")
		persistenceRun(t,p,b,c,fixture.Prepared(fixture.Exec("ALTER TABLE p SET ACCESS METHOD heap",0)))
		persistenceExec(t,p,b,c,"CREATE TABLE p2 PARTITION OF p FOR VALUES FROM(20) TO(30)")
		accessMethodProperty(t,p,b,c,"p","heap")
		accessMethodProperty(t,p,b,c,"p2","heap")
		persistenceExec(t,p,b,c,"ALTER TABLE p SET ACCESS METHOD DEFAULT")
		persistenceExec(t,p,b,c,"CREATE TABLE p3 PARTITION OF p FOR VALUES FROM(30) TO(40)")
		accessMethodProperty(t,p,b,c,"p","")
		for _,v:=range []struct{name,method string}{{"p0","heap"},{"p1","heap2"},{"p2","heap"},{"p3","heap3"}} {accessMethodProperty(t,p,b,c,v.name,v.method)}
		persistenceRun(t,p,b,c,fixture.Exec("INSERT INTO p VALUES(1),(11),(21),(31)",4))
		rows:=fixture.Select("SELECT p.id,c.relname,a.amname FROM p JOIN pg_class c ON c.oid=p.tableoid JOIN pg_am a ON a.oid=c.relam ORDER BY p.id",[]string{"id","relname","amname"},[]string{"INT4","NAME","NAME"},[][]any{{int64(1),"p0","heap"},{int64(11),"p1","heap2"},{int64(21),"p2","heap"},{int64(31),"p3","heap3"}})
		persistenceRun(t,p,b,c,rows);persistenceRun(t,p,b,c,fixture.Prepared(rows))
		for _,q:=range []string{
			"CREATE TABLE nested(id int) PARTITION BY RANGE(id)",
			"ALTER TABLE nested SET ACCESS METHOD heap2",
			"CREATE TABLE nested_child PARTITION OF nested FOR VALUES FROM(0) TO(100) PARTITION BY RANGE(id)",
			"CREATE TABLE nested_default PARTITION OF nested_child DEFAULT",
			"CREATE TABLE fallback(id int) PARTITION BY RANGE(id)",
			"CREATE TABLE fallback_child PARTITION OF fallback DEFAULT PARTITION BY RANGE(id)",
			"CREATE TABLE fallback_leaf PARTITION OF fallback_child DEFAULT",
		} {persistenceExec(t,p,b,c,q)}
		for _,name:=range []string{"nested_child","nested_default"} {accessMethodProperty(t,p,b,c,name,"heap2")}
		accessMethodProperty(t,p,b,c,"fallback_child","")
		accessMethodProperty(t,p,b,c,"fallback_leaf","heap3")
		persistenceExec(t,p,b,c,"SET default_table_access_method=heap")
		for _,q:=range []string{"CREATE TABLE parent(id int)","CREATE TABLE child() INHERITS(parent)","ALTER TABLE parent SET ACCESS METHOD heap2","ALTER TABLE ONLY parent SET ACCESS METHOD heap3","ALTER TABLE parent * SET ACCESS METHOD heap2, ADD COLUMN propagated int DEFAULT 4"} {persistenceExec(t,p,b,c,q)}
		accessMethodProperty(t,p,b,c,"parent","heap2");accessMethodProperty(t,p,b,c,"child","heap")
		persistenceRun(t,p,b,c,fixture.Exec("INSERT INTO child(id) VALUES(9)",1))
		persistenceRun(t,p,b,c,fixture.Select("SELECT id,propagated FROM child",[]string{"id","propagated"},[]string{"INT4","INT4"},[][]any{{int64(9),int64(4)}}))
		persistenceExec(t,p,b,c,"CREATE TABLE ordinary_child() INHERITS(parent)")
		accessMethodProperty(t,p,b,c,"ordinary_child","heap")
		for _,q:=range []string{"CREATE TABLE attached(id int)","ALTER TABLE attached SET ACCESS METHOD heap2","ALTER TABLE p ATTACH PARTITION attached FOR VALUES FROM(40) TO(50)"} {persistenceExec(t,p,b,c,q)}
		accessMethodProperty(t,p,b,c,"attached","heap2")
	})
}

func TestReferenceAlterAccessMethodMaterialized(t *testing.T) {
	accessMethodScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		persistenceExec(t,p,b,c,"CREATE TABLE source(id int)")
		persistenceRun(t,p,b,c,fixture.Exec("INSERT INTO source VALUES(7)",1))
		persistenceRun(t,p,b,c,fixture.Exec("CREATE MATERIALIZED VIEW mv AS SELECT id FROM source",1))
		for _,q:=range []string{"CREATE MATERIALIZED VIEW mv_empty AS SELECT id FROM source WITH NO DATA","CREATE TEMP TABLE temp_t(id int)","CREATE UNLOGGED TABLE unlogged_t(id int)"} {persistenceExec(t,p,b,c,q)}
		before:=accessMethodIdentity(t,p,c,"mv")
		var recipe string
		if err:=c.QueryRowContext(p.Ctx,"SELECT pg_get_viewdef('mv'::regclass)").Scan(&recipe);err!=nil {t.Fatal(err)}
		for _,change:=range []struct{sql,method string}{{"ALTER TABLE mv SET ACCESS METHOD heap2","heap2"},{"ALTER MATERIALIZED VIEW mv SET ACCESS METHOD heap3","heap3"}} {
			persistenceRun(t,p,b,c,fixture.Prepared(fixture.Exec(change.sql,0)))
			accessMethodProperty(t,p,b,c,"mv",change.method)
			if !reflect.DeepEqual(before,accessMethodIdentity(t,p,c,"mv")) {t.Fatal("materialized identity changed")}
			var current string
			if err:=c.QueryRowContext(p.Ctx,"SELECT pg_get_viewdef('mv'::regclass)").Scan(&current);err!=nil || current!=recipe {t.Fatal(current,recipe,err)}
			fixture.MaterializedViewPopulation(func(s fixture.Step){persistenceRun(t,p,b,c,s)},"mv",true)
			persistenceRun(t,p,b,c,fixture.Select("SELECT id FROM mv ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(7)}}))
		}
		persistenceExec(t,p,b,c,"ALTER MATERIALIZED VIEW mv_empty SET ACCESS METHOD heap2")
		accessMethodProperty(t,p,b,c,"mv_empty","heap2")
		fixture.MaterializedViewPopulation(func(s fixture.Step){persistenceRun(t,p,b,c,s)},"mv_empty",false)
		persistenceRun(t,p,b,c,fixture.MaterializedViewUnpopulated("mv_empty"))
		persistenceRun(t,p,b,c,fixture.Prepared(fixture.MaterializedViewUnpopulated("mv_empty")))
		persistenceRun(t,p,b,c,fixture.Exec("INSERT INTO source VALUES(8)",1))
		for _,name:=range []string{"mv","mv_empty"} {
			persistenceExec(t,p,b,c,"REFRESH MATERIALIZED VIEW "+name)
			persistenceRun(t,p,b,c,fixture.Select("SELECT id FROM "+name+" ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(7)},{int64(8)}}))
		}
		accessMethodProperty(t,p,b,c,"mv","heap3");accessMethodProperty(t,p,b,c,"mv_empty","heap2")
		for _,v:=range []struct{name,persistence string}{{"temp_t","t"},{"unlogged_t","u"}} {
			persistenceExec(t,p,b,c,"ALTER TABLE "+v.name+" SET ACCESS METHOD heap2")
			accessMethodProperty(t,p,b,c,v.name,"heap2");persistenceProperty(t,p,b,c,v.name,v.persistence)
			persistenceRun(t,p,b,c,fixture.Exec("INSERT INTO "+v.name+" VALUES(5)",1))
			persistenceRun(t,p,b,c,fixture.Prepared(fixture.Select("SELECT id FROM "+v.name,[]string{"id"},[]string{"INT4"},[][]any{{int64(5)}})))
		}
	})
}
