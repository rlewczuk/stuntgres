package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func explainAnalyzeMutationAdmission(t *testing.T) {
	t.Run("row-security",func(t *testing.T){
		for _,prepared:=range []bool{false,true}{
			t.Run(fmt.Sprint(prepared),func(t *testing.T){
				fixture.RoleReferenceCleanup(t,[]string{"ea_actor"})
				p:=fixture.NewScenario(t)
				for _,b:=range p.Backends(){
					t.Run(b.Name,func(t *testing.T){
						conn,err:=b.DB.Conn(p.Ctx)
						if err!=nil {t.Fatal(err)}
						defer conn.Close()
						for _,q:=range []string{"CREATE ROLE ea_actor","GRANT USAGE ON SCHEMA public TO ea_actor","CREATE TABLE ea_t(id integer,v integer)","INSERT INTO ea_t VALUES(1,10),(2,20)","ALTER TABLE ea_t ENABLE ROW LEVEL SECURITY","CREATE POLICY ea_visible ON ea_t USING(id=1) WITH CHECK(TRUE)","GRANT SELECT,UPDATE ON ea_t TO ea_actor"}{
							if _,err:=conn.ExecContext(p.Ctx,q);err!=nil {t.Fatal(err)}
						}
						tx,err:=conn.BeginTx(p.Ctx,nil)
						if err!=nil {t.Fatal(err)}
						defer tx.Rollback()
						if _,err:=tx.ExecContext(p.Ctx,"SET LOCAL ROLE ea_actor");err!=nil {t.Fatal(err)}
						analyzeRoot(t,analyzeQuery(t,p,tx,"EXPLAIN ANALYZE UPDATE ea_t SET v=v+1 RETURNING id",prepared),1)
						if _,err:=tx.ExecContext(p.Ctx,"RESET ROLE");err!=nil {t.Fatal(err)}
						fixture.RunStep(t,p.Ctx,tx,b.Recorder,fixture.Select("SELECT id,v FROM ea_t ORDER BY id",[]string{"id","v"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(11)},{int64(2),int64(20)}}))
						if err:=tx.Rollback();err!=nil {t.Fatal(err)}
						fixture.RunStep(t,p.Ctx,conn,b.Recorder,fixture.Select("SELECT id,v FROM ea_t ORDER BY id",[]string{"id","v"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(10)},{int64(2),int64(20)}}))
					})
				}
			})
		}
	})
	t.Run("positioned",func(t *testing.T){
		for _,prepared:=range []bool{false,true}{
			for _,remove:=range []bool{false,true}{
				t.Run(fmt.Sprintf("prepared=%t/delete=%t",prepared,remove),func(t *testing.T){
					p:=fixture.NewScenario(t)
					for _,b:=range p.Backends(){
						t.Run(b.Name,func(t *testing.T){
							for _,q:=range []string{"CREATE TABLE ea_t(id integer,v integer)","INSERT INTO ea_t VALUES(1,10),(2,20)"}{if _,err:=b.DB.ExecContext(p.Ctx,q);err!=nil {t.Fatal(err)}}
							tx,err:=b.DB.BeginTx(p.Ctx,nil)
							if err!=nil {t.Fatal(err)}
							defer tx.Rollback()
							if _,err:=tx.ExecContext(p.Ctx,"DECLARE ea_current CURSOR FOR SELECT id FROM ea_t ORDER BY id FOR UPDATE");err!=nil {t.Fatal(err)}
							fixture.RunStep(t,p.Ctx,tx,b.Recorder,fixture.Select("FETCH NEXT FROM ea_current",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)}}))
							query:="EXPLAIN ANALYZE UPDATE ea_t SET v=11 WHERE CURRENT OF ea_current RETURNING id"
							want:=[][]any{{int64(1),int64(11)},{int64(2),int64(20)}}
							if remove {query="EXPLAIN ANALYZE DELETE FROM ea_t WHERE CURRENT OF ea_current RETURNING id";want=[][]any{{int64(2),int64(20)}}}
							analyzeRoot(t,analyzeQuery(t,p,tx,query,prepared),1)
							fixture.RunStep(t,p.Ctx,tx,b.Recorder,fixture.Select("SELECT id,v FROM ea_t ORDER BY id",[]string{"id","v"},[]string{"INT4","INT4"},want))
							if err:=tx.Rollback();err!=nil {t.Fatal(err)}
							fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Select("SELECT id,v FROM ea_t ORDER BY id",[]string{"id","v"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(10)},{int64(2),int64(20)}}))
						})
					}
				})
			}
		}
	})
}
