package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHstoreRecordFeatureSmoke(t *testing.T) {
	hstoreRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreRecordSetup(t, p, b, c)
		hstoreRecordBoth(t, p, b, c, hstoreSelect(b, "SELECT hstore(NULL::hs_record_pair) AS v", []string{"v"}, [][]any{{`"i"=>NULL, "n"=>NULL`}}, nil))
		hstoreRecordBoth(t, p, b, c, hstoreSelect(b, "SELECT hstore('(7,keep)'::hs_record_pair) AS v", []string{"v"}, [][]any{{`"i"=>"7", "n"=>"keep"`}}, nil))
		constructorQuery := "SELECT hstore($1::hs_record_pair) AS v"
		constructor, err := c.PrepareContext(p.Ctx, constructorQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer constructor.Close()
		constructorHeld := fixture.HeldStatement{Executor: c, Statement: constructor, SQL: constructorQuery}
		hstoreRun(t, p, b, constructorHeld, hstoreSelect(b, constructorQuery, []string{"v"}, [][]any{{`"i"=>"7", "n"=>"keep"`}}, nil, "(7,keep)"))
		hstoreRun(t, p, b, constructorHeld, hstoreSelect(b, constructorQuery, []string{"v"}, [][]any{{`"i"=>NULL, "n"=>NULL`}}, nil, nil))
		for _, operator := range []bool{false, true} {
			q := "SELECT " + hstoreRecordCall(operator, "$1::hs_record_pair", "$2::hstore") + " AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct{ base, source, value any }{
				{"(7,keep)", "i=>0042", "(42,keep)"},
				{"(7,keep)", "unknown=>oops", "(7,keep)"},
				{"(7,keep)", "n=>NULL", "(7,)"},
				{"(7,keep)", `n=>"NULL"`, "(7,NULL)"},
				{nil, "", "(,)"},
				{nil, nil, nil},
			} {
				s := hstoreRecordRaw(b, q, [][]any{{tc.value}}, false, tc.base, tc.source)
				hstoreRecordBoth(t, p, b, c, s)
				hstoreRun(t, p, b, held, s)
			}
			stmt.Close()
		}
		hstoreRun(t, p, b, c, fixture.Exec("CREATE TYPE hs_record_smoke_short AS (v varchar(2))", 0))
		fixture.CompositeReferenceError(t, p, c, "SELECT populate_record(NULL::hs_record_smoke_short,$1::hstore)", "execute", fixture.ControlsReferenceDiagnostic("22001", "value too long for type character varying(2)"), "v=>abc")
	})
	// Each component owns bounded scenarios; no scenario extends its context.
	t.Run("atomicity", TestReferenceHstoreRecordAtomicity)
	t.Run("controls", TestReferenceHstoreRecordControls)
}
