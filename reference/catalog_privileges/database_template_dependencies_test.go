package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func templateIdentity(t *testing.T,c *databaseCreateCase,db *sql.DB,query string) int64 {
	t.Helper();var oid int64
	if err:=db.QueryRowContext(c.p.Ctx,query).Scan(&oid);err!=nil {t.Fatal(query,err)}
	if oid==0 {t.Fatal("zero identity",query)}
	return oid
}

func TestReferenceDatabaseTemplateDependencies(t *testing.T) {
	for _,family:=range []string{"types_constraints","routines_extensions","hierarchy"} {
		t.Run(family,func(t *testing.T){databaseCreateScenario(t,func(c *databaseCreateCase){
			c.create("dt_dependency_source","",false)
			source,_,sr:=c.pool("dt_dependency_source","")
			ddl:=[]string{"CREATE SCHEMA dt_dep"}
			identities:=[]string{}
			checks:=[]fixture.Step{}
			switch family {
			case "types_constraints":
				ddl=append(ddl,
					"CREATE TYPE dt_dep.mood AS ENUM('calm','busy')",
					"CREATE DOMAIN dt_dep.positive AS integer CONSTRAINT dt_positive CHECK(VALUE>0)",
					"CREATE TYPE dt_dep.pair AS (n integer,label text)",
					"CREATE TABLE dt_dep.parent(id integer CONSTRAINT dt_parent_pk PRIMARY KEY)",
					"CREATE SEQUENCE dt_dep.serial START 41",
					"CREATE TABLE dt_dep.child(id integer DEFAULT nextval('dt_dep.serial'), parent integer CONSTRAINT dt_child_fk REFERENCES dt_dep.parent(id), mood dt_dep.mood, amount dt_dep.positive, pair dt_dep.pair, readings integer[])",
					"ALTER SEQUENCE dt_dep.serial OWNED BY dt_dep.child.id",
					"CREATE UNIQUE INDEX dt_child_unique ON dt_dep.child(id)")
				identities=[]string{"SELECT 'dt_dep.parent'::regclass::oid","SELECT 'dt_dep.child'::regclass::oid","SELECT 'dt_dep.dt_child_unique'::regclass::oid","SELECT 'dt_dep.mood'::regtype::oid","SELECT 'dt_dep.positive'::regtype::oid","SELECT 'dt_dep.pair'::regtype::oid","SELECT 'dt_dep.serial'::regclass::oid"}
				checks=append(checks,
					fixture.Select("SELECT id,parent,mood::text,amount::integer,pair::text,readings::text FROM dt_dep.child",[]string{"id","parent","mood","amount","pair","readings"},[]string{"INT4","INT4","TEXT","INT4","TEXT","TEXT"},[][]any{{int64(1),int64(7),"calm",int64(3),"(5,label)","{5,9}"}}),
					fixture.Select("SELECT a.atttypid=t.oid AS column_type,t.typarray=ar.oid AS array_type,ar.typelem=t.oid AS element_type FROM pg_attribute a JOIN pg_type t ON t.oid=a.atttypid JOIN pg_type ar ON ar.oid=t.typarray WHERE a.attrelid='dt_dep.child'::regclass AND a.attname='mood'",[]string{"column_type","array_type","element_type"},[]string{"BOOL","BOOL","BOOL"},[][]any{{true,true,true}}),
					fixture.Select("SELECT a.atttypid='integer[]'::regtype AS column_type,t.typarray=ar.oid AS array_type,ar.typelem='integer'::regtype AS element_type FROM pg_attribute a JOIN pg_type ar ON ar.oid=a.atttypid JOIN pg_type t ON t.oid=ar.typelem WHERE a.attrelid='dt_dep.child'::regclass AND a.attname='readings'",[]string{"column_type","array_type","element_type"},[]string{"BOOL","BOOL","BOOL"},[][]any{{true,true,true}}),
					fixture.Select("SELECT t.typrelid=c.oid AS row_type FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='dt_dep.child'::regclass",[]string{"row_type"},[]string{"BOOL"},[][]any{{true}}),
					fixture.Select("SELECT conrelid='dt_dep.child'::regclass AS child,confrelid='dt_dep.parent'::regclass AS parent FROM pg_constraint WHERE conname='dt_child_fk'",[]string{"child","parent"},[]string{"BOOL","BOOL"},[][]any{{true,true}}),
					fixture.Select("SELECT indrelid='dt_dep.child'::regclass AS parent FROM pg_index WHERE indexrelid='dt_dep.dt_child_unique'::regclass",[]string{"parent"},[]string{"BOOL"},[][]any{{true}}),
					fixture.Select("SELECT pg_get_serial_sequence('dt_dep.child','id') AS owned",[]string{"owned"},[]string{"TEXT"},[][]any{{"dt_dep.serial"}}))
			case "routines_extensions":
				ddl=append(ddl,"CREATE TABLE dt_dep.input(id integer PRIMARY KEY,v integer)","CREATE TABLE dt_dep.log(v integer)","CREATE FUNCTION dt_dep.identity(n integer) RETURNS integer LANGUAGE SQL AS 'SELECT n'","CREATE FUNCTION dt_dep.record() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO dt_dep.log VALUES(NEW.v); RETURN NEW; END$$","CREATE TRIGGER dt_record AFTER INSERT ON dt_dep.input FOR EACH ROW EXECUTE FUNCTION dt_dep.record()","CREATE VIEW dt_dep.saved AS SELECT id,dt_dep.identity(v) AS v FROM dt_dep.input","CREATE EXTENSION unaccent SCHEMA dt_dep","CREATE EXTENSION amcheck SCHEMA dt_dep")
				identities=[]string{"SELECT 'dt_dep.input'::regclass::oid","SELECT 'dt_dep.saved'::regclass::oid","SELECT 'dt_dep.identity(integer)'::regprocedure::oid","SELECT oid FROM pg_extension WHERE extname='unaccent'"}
				checks=append(checks,
					templateRows("SELECT id,v FROM dt_dep.saved ORDER BY id",[]any{int64(1),int64(7)}),
					fixture.Select("SELECT dt_dep.unaccent('Hôtel') AS unaccent",[]string{"unaccent"},[]string{"TEXT"},[][]any{{"Hotel"}}),
					fixture.Select("SELECT p.proname FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid WHERE t.tgname='dt_record'",[]string{"proname"},[]string{"NAME"},[][]any{{"record"}}),
					fixture.Select("SELECT e.extname FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid='pg_proc'::regclass AND d.objid='dt_dep.unaccent(text)'::regprocedure AND d.refclassid='pg_extension'::regclass AND d.deptype='e'",[]string{"extname"},[]string{"NAME"},[][]any{{"unaccent"}}),
					fixture.Select("SELECT privtype FROM pg_init_privs WHERE classoid='pg_proc'::regclass AND objoid='dt_dep.bt_index_check(regclass)'::regprocedure",[]string{"privtype"},[]string{"CHAR"},[][]any{{"e"}}))
			case "hierarchy":
				ddl=append(ddl,"CREATE TABLE dt_dep.parent(id integer,v integer,CONSTRAINT dt_partition_pk PRIMARY KEY(id)) PARTITION BY RANGE(id)","CREATE TABLE dt_dep.leaf PARTITION OF dt_dep.parent FOR VALUES FROM(0) TO(10)","CREATE TABLE dt_dep.ancestor(id integer)","CREATE TABLE dt_dep.descendant(extra integer) INHERITS(dt_dep.ancestor)")
				identities=[]string{"SELECT 'dt_dep.parent'::regclass::oid","SELECT 'dt_dep.leaf'::regclass::oid"}
				checks=append(checks,templateRows("SELECT id,v FROM dt_dep.parent ORDER BY id",[]any{int64(1),int64(7)}),fixture.Select("SELECT inhparent='dt_dep.parent'::regclass AS parent FROM pg_inherits WHERE inhrelid='dt_dep.leaf'::regclass",[]string{"parent"},[]string{"BOOL"},[][]any{{true}}),fixture.Select("SELECT id,extra FROM dt_dep.descendant",[]string{"id","extra"},[]string{"INT4","INT4"},[][]any{{int64(3),int64(9)}}))
			}
			for _,q:=range ddl {templateRun(c,source,sr,fixture.Exec(q,0))}
			switch family {
			case "types_constraints":templateRun(c,source,sr,fixture.Exec("INSERT INTO dt_dep.parent VALUES(7)",1),fixture.Exec("INSERT INTO dt_dep.child VALUES(1,7,'calm',3,ROW(5,'label')::dt_dep.pair,ARRAY[5,9]::integer[])",1))
			case "routines_extensions":templateRun(c,source,sr,fixture.Exec("INSERT INTO dt_dep.input VALUES(1,7)",1))
			case "hierarchy":templateRun(c,source,sr,fixture.Exec("INSERT INTO dt_dep.parent VALUES(1,7)",1),fixture.Exec("INSERT INTO dt_dep.descendant VALUES(3,9)",1))
			}
			if family=="types_constraints" {templateRun(c,source,sr,fixture.Select("SELECT nextval('dt_dep.serial')",[]string{"nextval"},[]string{"INT8"},[][]any{{int64(41)}}))}
			old:=make([]int64,len(identities));for i,q:=range identities {old[i]=templateIdentity(c.t,c,source,q)}
			if err:=source.Close();err!=nil {c.t.Fatal(err)}
			c.create("dt_dependency_clone","TEMPLATE dt_dependency_source",true)
			clone,_,cr:=c.pool("dt_dependency_clone","")
			for i,q:=range identities {if got:=templateIdentity(c.t,c,clone,q);got!=old[i] {c.t.Fatalf("local OID changed for %s: %d want %d",q,got,old[i])}}
			for _,s:=range checks {templateRun(c,clone,cr,s,fixture.Prepared(s))}
			switch family {
			case "types_constraints":
				templateRun(c,clone,cr,fixture.Select("SELECT nextval('dt_dep.serial')",[]string{"nextval"},[]string{"INT8"},[][]any{{int64(42)}}))
				for _,tc:=range []struct{q,code,message,detail,schema,table,constraint string}{
					{"INSERT INTO dt_dep.parent VALUES(7)","23505",`duplicate key value violates unique constraint "dt_parent_pk"`,"Key (id)=(7) already exists.","dt_dep","parent","dt_parent_pk"},
					{"INSERT INTO dt_dep.child(id,parent) VALUES(2,99)","23503",`insert or update on table "child" violates foreign key constraint "dt_child_fk"`,`Key (parent)=(99) is not present in table "parent".`,"dt_dep","child","dt_child_fk"},
				} {d:=databaseCreateDiagnostic(tc.code,tc.message);d.Detail=tc.detail;d.Schema=tc.schema;d.Table=tc.table;d.Constraint=tc.constraint;templateRun(c,clone,cr,fixture.ErrorDiagnostic(tc.q,d))}
				d:=databaseCreateDiagnostic("23514",`value for domain dt_dep.positive violates check constraint "dt_positive"`);d.Schema="dt_dep";d.DataType="positive";d.Constraint="dt_positive"
				templateRun(c,clone,cr,fixture.ErrorDiagnostic("INSERT INTO dt_dep.child(id,parent,amount) VALUES(2,7,-1)",d),fixture.Exec("INSERT INTO dt_dep.child(parent) VALUES(7)",1),fixture.Select("SELECT id FROM dt_dep.child ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)},{int64(43)}}))
			case "routines_extensions":templateRun(c,clone,cr,fixture.Exec("INSERT INTO dt_dep.input VALUES(2,11)",1),fixture.Select("SELECT v FROM dt_dep.log ORDER BY v",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)},{int64(11)}}))
			case "hierarchy":templateRun(c,clone,cr,fixture.Exec("INSERT INTO dt_dep.parent VALUES(2,11)",1),templateRows("SELECT id,v FROM dt_dep.leaf ORDER BY id",[]any{int64(1),int64(7)},[]any{int64(2),int64(11)}))
			}
			source,_,sr=c.pool("dt_dependency_source","")
			if family=="types_constraints" {templateRun(c,source,sr,fixture.Select("SELECT nextval('dt_dep.serial')",[]string{"nextval"},[]string{"INT8"},[][]any{{int64(42)}}),fixture.Select("SELECT id FROM dt_dep.child",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)}}))}
			if family=="routines_extensions" {templateRun(c,source,sr,fixture.Select("SELECT v FROM dt_dep.log",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}))}
			if family=="hierarchy" {templateRun(c,source,sr,templateRows("SELECT id,v FROM dt_dep.parent ORDER BY id",[]any{int64(1),int64(7)}))}
		})})
	}
}
