package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func explainAnalyzePreparedLifetime(t *testing.T,p *fixture.Pair,b fixture.Backend) {
	t.Helper()
	for _,query:=range []string{"CREATE TABLE ea_lifetime(id integer)","INSERT INTO ea_lifetime VALUES(1),(2)"}{
		if _,err:=b.DB.ExecContext(p.Ctx,query);err!=nil {t.Fatal(err)}
	}
	statement,err:=b.DB.PrepareContext(p.Ctx,"EXPLAIN ANALYZE SELECT * FROM ea_lifetime WHERE id=$1")
	if err!=nil {t.Fatal(err)}
	defer statement.Close()
	for _,value:=range []int{1,0,2}{
		if value==2 {
			if _,err:=b.DB.ExecContext(p.Ctx,"ALTER TABLE ea_lifetime ADD COLUMN v integer DEFAULT 9");err!=nil {t.Fatal(err)}
		}
		rows,err:=statement.QueryContext(p.Ctx,value)
		if err!=nil {t.Fatal(err)}
		want:=float64(1)
		if value==0 {want=0}
		analyzeRoot(t,explainPlanRows(t,rows),want)
	}
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Select("SELECT id,v FROM ea_lifetime ORDER BY id",[]string{"id","v"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(9)},{int64(2),int64(9)}}))
}
