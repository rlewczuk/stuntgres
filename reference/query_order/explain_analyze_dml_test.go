package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExplainAnalyzeDML(t *testing.T) {
	cases := []struct {
		name, query string
		returning int
		want [][]any
	}{
		{"insert", "INSERT INTO ea_t VALUES(3,30)", 1, [][]any{{int64(1),int64(10)},{int64(2),int64(20)},{int64(3),int64(30)}}},
		{"update", "UPDATE ea_t SET v=11 WHERE id=1", 1, [][]any{{int64(1),int64(11)},{int64(2),int64(20)}}},
		{"delete", "DELETE FROM ea_t WHERE id=2", 1, [][]any{{int64(1),int64(10)}}},
		{"merge", "MERGE INTO ea_t t USING (VALUES(1,5),(3,30)) s(id,v) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=t.v+s.v WHEN NOT MATCHED THEN INSERT VALUES(s.id,s.v)", 2, [][]any{{int64(1),int64(15)},{int64(2),int64(20)},{int64(3),int64(30)}}},
	}
	for _, c := range cases {
		for _, prepared := range []bool{false,true} {
			for _, returning := range []bool{false,true} {
				for _, rollback := range []bool{false,true} {
					t.Run(fmt.Sprintf("%s/prepared=%t/returning=%t/rollback=%t",c.name,prepared,returning,rollback),func(t *testing.T) {
						p := fixture.NewScenario(t)
						for _, b := range p.Backends() {
							t.Run(b.Name,func(t *testing.T) {
								run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s) }
								run(fixture.Exec("CREATE TABLE ea_t(id integer,v integer)",0))
								run(fixture.Exec("INSERT INTO ea_t VALUES(1,10),(2,20)",2))
								query := "EXPLAIN ANALYZE "+c.query
								wantRows := float64(0)
								if returning {
									if c.name == "merge" {
										query += " RETURNING t.id"
									} else {
										query += " RETURNING id"
									}
									wantRows=float64(c.returning)
								}
								if rollback {
									tx,err := b.DB.BeginTx(p.Ctx,nil)
									if err != nil { t.Fatal(err) }
									defer tx.Rollback()
									analyzeRoot(t,analyzeQuery(t,p,tx,query,prepared),wantRows)
									fixture.RunStep(t,p.Ctx,tx,b.Recorder,fixture.Select("SELECT id,v FROM ea_t ORDER BY id",[]string{"id","v"},[]string{"INT4","INT4"},c.want))
									if err := tx.Rollback(); err != nil { t.Fatal(err) }
									run(fixture.Select("SELECT id,v FROM ea_t ORDER BY id",[]string{"id","v"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(10)},{int64(2),int64(20)}}))
								} else {
									analyzeRoot(t,analyzeQuery(t,p,b.DB,query,prepared),wantRows)
									run(fixture.Select("SELECT id,v FROM ea_t ORDER BY id",[]string{"id","v"},[]string{"INT4","INT4"},c.want))
								}
							})
						}
					})
				}
			}
		}
	}
}

func TestReferenceExplainAnalyzeMutationPaths(t *testing.T) {
	t.Run("dispatch-frontiers", explainAnalyzeMutationFrontiers)
	t.Run("admission", explainAnalyzeMutationAdmission)
	cases := []struct {
		name string
		setup []string
		query string
		rows float64
		want [][]any
	}{
		{"insert-select",nil,"INSERT INTO ea_t SELECT 3,30",0,[][]any{{int64(1),int64(10)},{int64(2),int64(20)},{int64(3),int64(30)}}},
		{"conflict",nil,"INSERT INTO ea_t VALUES(1,7) ON CONFLICT(id) DO UPDATE SET v=ea_t.v+excluded.v RETURNING id",1,[][]any{{int64(1),int64(17)},{int64(2),int64(20)}}},
		{"conflict-nothing",nil,"INSERT INTO ea_t VALUES(1,7) ON CONFLICT DO NOTHING RETURNING id",0,[][]any{{int64(1),int64(10)},{int64(2),int64(20)}}},
		{"update-from",nil,"UPDATE ea_t t SET v=s.v FROM (VALUES(1,40)) s(id,v) WHERE t.id=s.id RETURNING t.id",1,[][]any{{int64(1),int64(40)},{int64(2),int64(20)}}},
		{"delete-using",nil,"DELETE FROM ea_t t USING (VALUES(2)) s(id) WHERE t.id=s.id RETURNING t.id",1,[][]any{{int64(1),int64(10)}}},
		{"cte-select",nil,"WITH x AS (UPDATE ea_t SET v=v+1 WHERE id=1 RETURNING id) SELECT id FROM x",1,[][]any{{int64(1),int64(11)},{int64(2),int64(20)}}},
		{"cte-primary",nil,"WITH x AS (INSERT INTO ea_t VALUES(3,30) RETURNING id) UPDATE ea_t SET v=11 WHERE id=1 RETURNING id",1,[][]any{{int64(1),int64(11)},{int64(2),int64(20)},{int64(3),int64(30)}}},
		{"view",[]string{"CREATE VIEW ea_v AS SELECT id,v FROM ea_t"},"UPDATE ea_v SET v=11 WHERE id=1 RETURNING id",1,[][]any{{int64(1),int64(11)},{int64(2),int64(20)}}},
		{"rule-nothing",[]string{"CREATE RULE ea_no AS ON INSERT TO ea_t DO INSTEAD NOTHING"},"INSERT INTO ea_t VALUES(3,30)",0,[][]any{{int64(1),int64(10)},{int64(2),int64(20)}}},
		{"merge-nothing",nil,"MERGE INTO ea_t t USING (VALUES(1),(3)) s(id) ON t.id=s.id WHEN MATCHED THEN DO NOTHING WHEN NOT MATCHED THEN INSERT VALUES(s.id,30) RETURNING t.id",1,[][]any{{int64(1),int64(10)},{int64(2),int64(20)},{int64(3),int64(30)}}},
	}
	for _, c := range cases {
		for _, prepared := range []bool{false,true} {
			t.Run(fmt.Sprintf("%s/prepared=%t",c.name,prepared),func(t *testing.T) {
				p:=fixture.NewScenario(t)
				for _,b:=range p.Backends() {
					t.Run(b.Name,func(t *testing.T) {
						for _,q:=range append([]string{"CREATE TABLE ea_t(id integer PRIMARY KEY,v integer)","INSERT INTO ea_t VALUES(1,10),(2,20)"},c.setup...) {
							if _,err:=b.DB.ExecContext(p.Ctx,q);err!=nil {t.Fatal(err)}
						}
						lines:=analyzeQuery(t,p,b.DB,"EXPLAIN ANALYZE "+c.query,prepared)
						if c.name!="rule-nothing" {analyzeRoot(t,lines,c.rows)}
						fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Select("SELECT id,v FROM ea_t ORDER BY id",[]string{"id","v"},[]string{"INT4","INT4"},c.want))
					})
				}
			})
		}
	}
}

func TestReferenceExplainAnalyzeFeatureSmoke(t *testing.T) {
	for _,prepared:=range []bool{false,true} {
		t.Run(fmt.Sprint(prepared),func(t *testing.T) {
			p:=fixture.NewScenario(t)
			for _,b:=range p.Backends() {
				t.Run(b.Name,func(t *testing.T) {
					run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)}
					run(fixture.Exec("CREATE TABLE ea_smoke(id integer)",0))
					run(fixture.Exec("INSERT INTO ea_smoke VALUES(1),(2),(3)",3))
					for _,c:=range []struct{predicate string; rows float64}{{"id<0",0},{"id=2",1},{"id>0",3}} {
						analyzeRoot(t,analyzeQuery(t,p,b.DB,"EXPLAIN (ANALYZE,TIMING OFF) SELECT id FROM ea_smoke WHERE "+c.predicate,prepared),c.rows)
					}
					tx,err:=b.DB.BeginTx(p.Ctx,nil)
					if err!=nil {t.Fatal(err)}
					defer tx.Rollback()
					analyzeRoot(t,analyzeQuery(t,p,tx,"EXPLAIN ANALYZE UPDATE ea_smoke SET id=11 WHERE id=1 RETURNING id",prepared),1)
					fixture.RunStep(t,p.Ctx,tx,b.Recorder,fixture.Select("SELECT id FROM ea_smoke ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(2)},{int64(3)},{int64(11)}}))
					if err:=tx.Rollback();err!=nil {t.Fatal(err)}
					run(fixture.Select("SELECT id FROM ea_smoke ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)},{int64(3)}}))
					analyzeRoot(t,analyzeQuery(t,p,b.DB,"EXPLAIN ANALYZE INSERT INTO ea_smoke VALUES(4)",prepared),0)
					run(fixture.Select("SELECT id FROM ea_smoke ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)},{int64(3)},{int64(4)}}))
					s:=fixture.ErrorDiagnostic("EXPLAIN ANALYZE SELECT 10/(id-id) FROM ea_smoke",fixture.ControlsReferenceDiagnostic("22012","division by zero"))
					s.Prepared,s.SelectRows,s.ErrorStage=prepared,true,"execute"
					run(s)
				})
			}
		})
	}
}
