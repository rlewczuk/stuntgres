package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func explainAnalyzeMutationFrontiers(t *testing.T) {
	cases:=[]struct{
		name string
		setup []string
		query,inspect string
		rows float64
		want [][]any
	}{
		{"defaults",[]string{"CREATE SEQUENCE ea_seq START 100","CREATE TABLE ea_t(id integer DEFAULT nextval('ea_seq'),v integer DEFAULT 10)"},"INSERT INTO ea_t DEFAULT VALUES RETURNING nextval('ea_seq')","SELECT id,v FROM ea_t",1,[][]any{{int64(100),int64(10)}}},
		{"trigger",[]string{"CREATE TABLE ea_t(id integer,v integer)","CREATE FUNCTION ea_before() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v+100; RETURN NEW; END$$","CREATE TRIGGER ea_before BEFORE INSERT ON ea_t FOR EACH ROW EXECUTE FUNCTION ea_before()"},"INSERT INTO ea_t VALUES(3,30) RETURNING id","SELECT id,v FROM ea_t",1,[][]any{{int64(3),int64(130)}}},
		{"instead-trigger",[]string{"CREATE TABLE ea_t(id integer,v integer)","CREATE VIEW ea_v AS SELECT id,v FROM ea_t","CREATE FUNCTION ea_instead() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO ea_t VALUES(NEW.id,NEW.v); RETURN NEW; END$$","CREATE TRIGGER ea_instead INSTEAD OF INSERT ON ea_v FOR EACH ROW EXECUTE FUNCTION ea_instead()"},"INSERT INTO ea_v VALUES(3,30) RETURNING id","SELECT id,v FROM ea_t",1,[][]any{{int64(3),int64(30)}}},
		{"rule-multiple",[]string{"CREATE TABLE ea_t(id integer,v integer)","CREATE TABLE ea_log(id integer,v integer)","CREATE RULE ea_copy AS ON INSERT TO ea_t DO ALSO INSERT INTO ea_log VALUES(NEW.id,NEW.v)"},"INSERT INTO ea_t VALUES(3,30)","SELECT id,v FROM ea_t UNION ALL SELECT id,v FROM ea_log ORDER BY id,v",0,[][]any{{int64(3),int64(30)},{int64(3),int64(30)}}},
		{"inheritance",[]string{"CREATE TABLE ea_t(id integer,v integer)","CREATE TABLE ea_child() INHERITS(ea_t)","INSERT INTO ea_t VALUES(1,10)","INSERT INTO ea_child VALUES(2,20)"},"UPDATE ea_t SET v=v+1 RETURNING id","SELECT id,v FROM ea_t ORDER BY id",2,[][]any{{int64(1),int64(11)},{int64(2),int64(21)}}},
		{"partition",[]string{"CREATE TABLE ea_t(id integer,v integer) PARTITION BY RANGE(id)","CREATE TABLE ea_low PARTITION OF ea_t FOR VALUES FROM(0) TO(10)"},"INSERT INTO ea_t VALUES(3,30) RETURNING id","SELECT id,v FROM ea_t",1,[][]any{{int64(3),int64(30)}}},
		{"merge-actions",[]string{"CREATE TABLE ea_t(id integer,v integer)","INSERT INTO ea_t VALUES(1,10),(2,20)"},"MERGE INTO ea_t t USING (VALUES(1,0),(2,22),(3,30)) s(id,v) ON t.id=s.id WHEN MATCHED AND s.v=0 THEN DELETE WHEN MATCHED THEN UPDATE SET v=s.v WHEN NOT MATCHED THEN INSERT VALUES(s.id,s.v) RETURNING s.id,old.v,new.v","SELECT id,v FROM ea_t ORDER BY id",3,[][]any{{int64(2),int64(22)},{int64(3),int64(30)}}},
	}
	for _,c:=range cases {
		for _,prepared:=range []bool{false,true} {
			for _,rollback:=range []bool{false,true} {
				t.Run(fmt.Sprintf("%s/prepared=%t/rollback=%t",c.name,prepared,rollback),func(t *testing.T){
					p:=fixture.NewScenario(t)
					for _,b:=range p.Backends(){
						t.Run(b.Name,func(t *testing.T){
							for _,q:=range c.setup {if _,err:=b.DB.ExecContext(p.Ctx,q);err!=nil {t.Fatal(err)}}
							if rollback {
								tx,err:=b.DB.BeginTx(p.Ctx,nil)
								if err!=nil {t.Fatal(err)}
								defer tx.Rollback()
								analyzeRoot(t,analyzeQuery(t,p,tx,"EXPLAIN ANALYZE "+c.query,prepared),c.rows)
								fixture.RunStep(t,p.Ctx,tx,b.Recorder,fixture.Select(c.inspect,[]string{"id","v"},[]string{"INT4","INT4"},c.want))
								if err:=tx.Rollback();err!=nil {t.Fatal(err)}
								original:=[][]any(nil)
								if c.name=="inheritance" || c.name=="merge-actions" {original=[][]any{{int64(1),int64(10)},{int64(2),int64(20)}}}
								fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Select(c.inspect,[]string{"id","v"},[]string{"INT4","INT4"},original))
							} else {
								analyzeRoot(t,analyzeQuery(t,p,b.DB,"EXPLAIN ANALYZE "+c.query,prepared),c.rows)
								fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Select(c.inspect,[]string{"id","v"},[]string{"INT4","INT4"},c.want))
							}
							if c.name=="defaults" {
								fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Select("SELECT last_value FROM ea_seq",[]string{"last_value"},[]string{"INT8"},[][]any{{int64(101)}}))
							}
						})
					}
				})
			}
		}
	}
}
