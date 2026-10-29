package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONPathNumericMethodsFeatureSmoke(t *testing.T) {
	jsonpathNavigateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		for _,tc:=range []struct{doc,path,want string}{
			{`[0,1,-2,-3.4,5.6]`,`$[*].abs()`,`[0, 1, 2, 3.4, 5.6]`},
			{`[0,1,-2,-3.4,5.6]`,`$[*].floor()`,`[0, 1, -2, -4, 5]`},
			{`[0,1,-2,-3.4,5.6]`,`$[*].ceiling()`,`[0, 1, -2, -3, 6]`},
			{`[9007199254740993,"9007199254740993",1.2300,"1.2300"]`,`$[*].number()`,`[9007199254740993, 9007199254740993, 1.2300, 1.2300]`},
			{`[9007199254740993,"9007199254740993",1.2300,"1.2300"]`,`$[*].double()`,`[9007199254740993, 9007199254740990, 1.2300, 1.23]`},
			{`"0x1.8p1"`,`$.double()`,`[3]`},
			{`["0x10","1_000"]`,`$[*].number()`,`[16, 1000]`},
		} {
			fixture.JSONPathNavigateBoth(t,p,b,c,fixture.JSONBSetMetadataStep(jsonpathNavigateSelect(`SELECT jsonb_path_query_array($1,$2) AS changed`,tc.want,tc.doc,tc.path),b.PG))
		}
		fixture.JSONPathNavigateBoth(t,p,b,c,jsonpathNavigateError(`SELECT jsonb_path_query_array('"1_000"','$.double()')`,"22036",numericMethodsInvalid("double","1_000")))
		fixture.JSONPathNavigateBoth(t,p,b,c,jsonpathNavigateError(`SELECT jsonb_path_query_array('"NaN(payload)"','$.double()')`,"22036",`NaN or Infinity is not allowed for jsonpath item method .double()`))
		q:=`SELECT jsonb_path_query_array($1,$2,'{}',$3) AS changed`
		stmt,err:=c.PrepareContext(p.Ctx,q)
		if err!=nil {t.Fatal(err)}
		defer stmt.Close()
		held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
		for _,s:=range []fixture.Step{
			jsonpathNavigateSelect(q,`[9007199254740993, 9007199254740990]`,`[9007199254740993,"9007199254740993"]`,`$[*].double()`,false),
			jsonpathNavigateError(q,"22036",numericMethodsInvalid("double","bad"),`[1,"bad",2]`,`$[*].double()`,false),
			jsonpathNavigateSelect(q,`[1]`,`[1,"bad",2]`,`$[*].double()`,true),
			jsonpathNavigateSelect(q,`[1.2300]`,`["1.2300"]`,`$[*].number()`,false),
		} {fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.JSONBSetMetadataStep(s,b.PG))}
		check:=numericMethodsStored(t,p,b,c)
		future:=[]fixture.Step{
			fixture.Exec(`UPDATE jpnm_stored SET doc='["3.4560"]' WHERE id=1`,1),
			jsonpathNavigateSelect(`SELECT changed FROM jpnm_view WHERE id=1`,`[3.4560]`),
		}
		fixture.JSONPathNavigateReplay(t,p,b,c,check,future...)
		for _,s:=range future {fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
	})
}
