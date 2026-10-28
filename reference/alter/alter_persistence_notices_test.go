package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func persistenceNoticeCases(t *testing.T) {
	p:=fixture.NewScenario(t)
	for _,b:=range p.Backends() {
		t.Run(b.Name,func(t *testing.T) {
			var notices []fixture.Diagnostic
			_,c,rec:=fixture.NoticeReferencePool(t,p,b.PG,&notices)
			b.Recorder=rec
			persistenceExec(t,p,b,c,"CREATE UNLOGGED TABLE conditional(id int)")
			for _,prepared:=range []bool{false,true} {
				for _,query:=range []string{"CREATE TABLE IF NOT EXISTS conditional(id int)","CREATE UNLOGGED TABLE IF NOT EXISTS conditional(id int) PARTITION BY RANGE(id)"} {
					notices=nil
					step:=fixture.Exec(query,0);if prepared {step=fixture.Prepared(step)};persistenceRun(t,p,b,c,step)
					want:=fixture.Diagnostic{Severity:"NOTICE",SeverityUnlocalized:"NOTICE",Code:"42P07",Message:`relation "conditional" already exists, skipping`}
					if len(notices)!=1 || notices[0]!=want {t.Fatal(notices,want)}
					persistenceProperty(t,p,b,c,"conditional","u")
				}
				notices=nil
				step:=fixture.Exec("ALTER TABLE IF EXISTS absent_persistence SET UNLOGGED",0);if prepared {step=fixture.Prepared(step)};persistenceRun(t,p,b,c,step)
				want:=fixture.Diagnostic{Severity:"NOTICE",SeverityUnlocalized:"NOTICE",Code:"00000",Message:`relation "absent_persistence" does not exist, skipping`}
				if len(notices)!=1 || notices[0]!=want {t.Fatal(notices,want)}
			}
		})
	}
}
