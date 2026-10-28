package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func persistenceSelfCacheCases(t *testing.T) {
	for _,mode:=range []string{"commit","rollback-no-demand","rollback-demand"} {
		t.Run(mode,func(t *testing.T) {
			persistenceScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
				persistenceExec(t,p,b,c,"CREATE SEQUENCE self_seq CACHE 5")
				persistenceValue(t,p,b,c,"nextval('self_seq')",1)
				tx,err:=c.BeginTx(p.Ctx,nil);if err!=nil {t.Fatal(err)};defer tx.Rollback()
				persistenceRun(t,p,b,tx,fixture.Prepared(fixture.Exec("ALTER TABLE self_seq SET UNLOGGED",0)))
				persistenceProperty(t,p,b,tx,"self_seq","u")
				if mode=="rollback-demand" {persistenceValue(t,p,b,tx,"nextval('self_seq')",6)}
				if mode=="commit" {
					if err=tx.Commit();err!=nil {t.Fatal(err)}
					persistenceProperty(t,p,b,c,"self_seq","u")
					persistenceValue(t,p,b,c,"currval('self_seq')",1)
					persistenceValue(t,p,b,c,"nextval('self_seq')",6)
				} else {
					if err=tx.Rollback();err!=nil {t.Fatal(err)}
					persistenceProperty(t,p,b,c,"self_seq","p")
					if mode=="rollback-no-demand" {persistenceValue(t,p,b,c,"nextval('self_seq')",2)} else {persistenceValue(t,p,b,c,"currval('self_seq')",6);persistenceValue(t,p,b,c,"lastval()",6);persistenceValue(t,p,b,c,"nextval('self_seq')",6)}
				}
			})
		})
	}
}
