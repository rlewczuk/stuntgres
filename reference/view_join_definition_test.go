//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceViewJoinedDefinition(t *testing.T) {
	p := newReferencePair(t)
	for _, step := range []referenceStep{
		referenceExec("CREATE TABLE a(id INT,x TEXT,d DATE)",0),
		referenceExec("CREATE TABLE b(id BIGINT,y TEXT)",0),
		referenceExec("CREATE TABLE c(id NUMERIC(5,2))",0),
		referenceExec("INSERT INTO a VALUES(1,'a','2000-02-01'),(2,'b','2000-01-02'),(3,'c','2000-02-01')",3),
		referenceExec("INSERT INTO b VALUES(1,'u'),(1,'v'),(3,NULL)",3),
		referenceExec("INSERT INTO c VALUES(1.00),(3.00)",2),
		referenceExec("CREATE VIEW joined AS SELECT l.id,l.x,r.y FROM a AS l JOIN b AS r ON l.id=r.id AND l.d='2000-02-01'",0),
		referenceExec("CREATE VIEW merged AS SELECT id,x,y FROM a JOIN b USING(id)",0),
		referenceExec("CREATE VIEW natural_view AS SELECT id,x,y FROM a NATURAL JOIN b",0),
		referenceExec("CREATE VIEW repeated(left_id,label,day,right_id) AS SELECT l.*,r.id FROM a AS l JOIN a AS r ON l.id<r.id",0),
		referenceExec("CREATE VIEW chain AS SELECT l.id,l.x,r.y FROM (a AS l JOIN b AS r ON l.id=r.id AND l.d='2000-02-01') JOIN b AS z ON l.id=z.id AND z.y='u'",0),
		referenceExec("CREATE VIEW alias_columns AS SELECT q.key AS id,q.label AS x,q.value AS y FROM (a AS l(key,label,day) JOIN b AS r(other,value) ON l.key=r.other) AS q(key,label,day,other,value)",0),
		referenceExec("CREATE VIEW converted AS SELECT id FROM a JOIN c USING(id)",0),
		referenceExec("ALTER TABLE a RENAME COLUMN id TO aid",0),
		referenceExec("ALTER TABLE a RENAME TO renamed_a",0),
		referenceExec("ALTER TABLE b RENAME TO renamed_b",0),
		referenceExec("CREATE TABLE a(id INT)",0),
		referenceExec("INSERT INTO a VALUES(99)",1),
	} { p.step(t,step) }
	for _, name := range []string{"joined","merged","natural_view","repeated","chain","alias_columns","converted"} {
		for _, db := range []*sql.DB{p.memory,p.postgres} {
			var definition string
			if err := db.QueryRowContext(p.ctx,"SELECT definition FROM pg_catalog.pg_views WHERE viewname=$1",name).Scan(&definition); err != nil { t.Fatal(err) }
			if _, err := db.ExecContext(p.ctx,"CREATE VIEW "+name+"_copy AS "+definition); err != nil { t.Fatal(err,definition) }
		}
		for _, suffix := range []string{"","_copy"} {
			var step referenceStep
			if name == "repeated" {
				step = referenceSelect("SELECT left_id,label,right_id FROM "+name+suffix+" ORDER BY left_id",[]string{"left_id","label","right_id"},[]string{"INT4","TEXT","INT4"},[][]any{{int64(1),"a",int64(2)},{int64(1),"a",int64(3)},{int64(2),"b",int64(3)}})
			} else if name == "converted" {
				step = referenceSelect("SELECT id FROM "+name+suffix+" ORDER BY id",[]string{"id"},[]string{"NUMERIC"},[][]any{{"1"},{"3"}})
			} else {
				typ := "INT4"; if name == "merged" || name == "natural_view" { typ = "INT8" }
				want := [][]any{{int64(1),"a","u"},{int64(1),"a","v"},{int64(3),"c",nil}}
				if name == "chain" { want = want[:2] }
				step = referenceSelect("SELECT id,x,y FROM "+name+suffix+" ORDER BY id",[]string{"id","x","y"},[]string{typ,"TEXT","TEXT"},want)
			}
			p.step(t,step); p.step(t,referencePrepared(step))
		}
	}
	// ADD COLUMN is an external PostgreSQL-only control, not native SQL support.
	// A new shared label must not broaden the creation-time NATURAL key set.
	run := func(step referenceStep) { referenceRunStep(t,p.ctx,p.postgres,p.postgresRecord,step) }
	run(referenceExec("ALTER TABLE renamed_a ADD COLUMN y TEXT DEFAULT 'different'",0))
	var definition string
	if err := p.postgres.QueryRowContext(p.ctx,"SELECT definition FROM pg_catalog.pg_views WHERE viewname='natural_view'").Scan(&definition); err != nil { t.Fatal(err) }
	run(referenceExec("CREATE VIEW frozen_keys AS "+definition,0))
	step := referenceSelect("SELECT id,x,y FROM frozen_keys ORDER BY id",[]string{"id","x","y"},[]string{"INT8","TEXT","TEXT"},[][]any{{int64(1),"a","u"},{int64(1),"a","v"},{int64(3),"c",nil}})
	run(step); run(referencePrepared(step))
}

func TestReferenceViewJoinedNamespaceReplay(t *testing.T) {
	p := newReferencePair(t)
	for _, step := range []referenceStep{
		referenceExec("CREATE TABLE a(unused INT,id INT)",0),
		referenceExec("CREATE TABLE c(id NUMERIC(5,2))",0),
		referenceExec("INSERT INTO a VALUES(0,3),(0,1),(0,2)",3),
		referenceExec("INSERT INTO c VALUES(3.00),(1.00)",2),
		referenceExec("CREATE VIEW self_sorted AS SELECT id FROM a AS l JOIN a AS r USING(id) ORDER BY id",0),
		referenceExec("CREATE VIEW converted_sorted AS SELECT id FROM a JOIN c USING(id) ORDER BY id",0),
		referenceExec("CREATE VIEW alias_sorted AS SELECT q.key AS id FROM (a AS l(discard,key) JOIN a AS r(discard,key) USING(key)) AS q(key,left_discard,right_discard) ORDER BY key",0),
		referenceExec("ALTER TABLE a RENAME COLUMN id TO aid",0),
		referenceExec("ALTER TABLE a DROP COLUMN unused",0),
	} { p.step(t,step) }
	for _, name := range []string{"self_sorted","converted_sorted","alias_sorted"} {
		for _, db := range []*sql.DB{p.memory,p.postgres} {
			var definition string
			if err := db.QueryRowContext(p.ctx,"SELECT definition FROM pg_catalog.pg_views WHERE viewname=$1",name).Scan(&definition); err != nil { t.Fatal(err) }
			if _, err := db.ExecContext(p.ctx,"CREATE VIEW "+name+"_copy AS "+definition); err != nil { t.Fatal(err,definition) }
		}
		typ, want := "INT4", [][]any{{int64(1)},{int64(2)},{int64(3)}}
		if name == "converted_sorted" { typ, want = "NUMERIC", [][]any{{"1"},{"3"}} }
		for _, suffix := range []string{"","_copy"} {
			step := referenceSelect("SELECT id FROM "+name+suffix,[]string{"id"},[]string{typ},want)
			p.step(t,step); p.step(t,referencePrepared(step))
		}
	}
}
