package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHstoreRowsFeatureSmoke(t *testing.T) {
	t.Run("extraction_named_null_recovery",func(t *testing.T){
		hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
			hstoreRowsSeed(t,p,b,c)
			for _,name:=range []string{"skeys","svals","each"}{
				q:="SELECT * FROM "+name+"($1::hstore)"
				hstoreRowsBoth(t,p,b,c,hstoreRowsSelect(q,hstoreRowsNames(name),hstoreRowsValues(name),hstoreRowsInput))
				for _,empty:=range []any{"",nil}{hstoreRowsBoth(t,p,b,c,hstoreRowsSelect(q,hstoreRowsNames(name),[][]any{},empty))}
			}
			hstoreRowsBoth(t,p,b,c,hstoreRowsSelect(`SELECT key FROM each(hs => $1::hstore) WHERE value IS NULL`,[]string{"key"},[][]any{{"b"}},hstoreRowsInput))
			q:=`SELECT * FROM svals($1::hstore)`
			stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
			held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
			hstoreError(t,p,b,held,q,false,true,false,hstoreParameterDiagnostic("42601",`syntax error in hstore, near "b" at position 2`,1),"a=b")
			hstoreRun(t,p,b,held,hstoreRowsSelect(q,[]string{"svals"},hstoreRowsValues("svals"),hstoreRowsInput))
		})
	})
	// A fresh bounded scenario exercises actual current-name stored consumers,
	// displaced native Restore and the identical emitted SQL on the real oracle.
	t.Run("stored_restore_emitted_replay",func(t *testing.T){hstoreRowsReplayPhase(t,"fresh")})
}
