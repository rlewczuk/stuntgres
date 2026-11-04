package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceClusterProgressReadOnly(t *testing.T) {
	persistenceScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		for _,prepared := range []bool{false,true} {
			for i,q := range []string{
				"INSERT INTO pg_catalog.pg_stat_progress_cluster DEFAULT VALUES",
				"UPDATE pg_catalog.pg_stat_progress_cluster SET phase='x' WHERE false",
				"DELETE FROM pg_catalog.pg_stat_progress_cluster WHERE false",
				"TRUNCATE pg_catalog.pg_stat_progress_cluster",
			} {
				var want fixture.Diagnostic
				if i==3 { want = persistenceDiagnostic("42809",`"pg_stat_progress_cluster" is not a table`) } else {
					verb := []string{"insert into","update","delete from"}[i]
					want = persistenceDiagnostic("55000",fmt.Sprintf("cannot %s view \"pg_stat_progress_cluster\"",verb))
					want.Detail = "Views that do not select from a single table or view are not automatically updatable."
					operation := []string{"INSERT","UPDATE","DELETE"}[i]
					action := []string{"inserting into","updating","deleting from"}[i]
					want.Hint = fmt.Sprintf("To enable %s the view, provide an INSTEAD OF %s trigger or an unconditional ON %s DO INSTEAD rule.",action,operation,operation)
				}
				step := fixture.ErrorDiagnostic(q,want)
				step.ErrorStage = "execute"
				if prepared {
					step = fixture.Prepared(step)
					if i!=3 { step.ErrorStage = "prepare" }
				}
				persistenceRun(t,p,b,c,step)
			}
		}
		persistenceRun(t,p,b,c,clusterCommandProgressIdle())
		persistenceRun(t,p,b,c,fixture.Prepared(clusterCommandProgressIdle()))
	})
}
