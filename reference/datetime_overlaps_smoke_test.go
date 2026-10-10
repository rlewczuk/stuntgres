package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
)

func TestReferenceDatetimeOverlapsFeatureSmoke(t *testing.T) {
	datetimeOverlapsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		q := "SELECT (DATE '2001-01-01',DATE '2001-01-03') OVERLAPS (DATE '2001-01-02',DATE '2001-01-04') AS date_hit,(TIMESTAMP '2001-01-01',TIMESTAMP '2001-01-02') OVERLAPS (TIMESTAMP '2001-01-02',TIMESTAMP '2001-01-03') AS touching,(TIME '02:00',TIME '02:00') OVERLAPS (TIME '02:00',TIME '02:00') AS instant,(NULL::timestamp,TIMESTAMP '2001-01-02') OVERLAPS (TIMESTAMP '2001-01-01',TIMESTAMP '2001-01-03') AS interior,(NULL::timestamp,TIMESTAMP '2001-01-04') OVERLAPS (TIMESTAMP '2001-01-01',TIMESTAMP '2001-01-03') AS exterior"
		datetimeOverlapsBoth(t, p, b, c, referenceSelect(q, []string{"date_hit", "touching", "instant", "interior", "exterior"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, false, true, true, nil}}))
		datetimeOverlapsValue(t, p, b, c, "(TIMESTAMP '2001-01-03',INTERVAL '-2 days') OVERLAPS (TIMESTAMP '2001-01-02',TIMESTAMP '2001-01-02')", true)
		datetimeOverlapsValue(t, p, b, c, "(TIMESTAMP '2001-01-01',TIME '01:00') OVERLAPS (TIMESTAMP '2001-01-01',TIME '02:00')", true)
		heldQuery := "SELECT ($1,TIMESTAMP '2001-01-03') OVERLAPS (TIMESTAMP '2001-01-01',TIMESTAMP '2001-01-04')"
		stmt, err := c.PrepareContext(p.ctx, heldQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: heldQuery}
		for _, arg := range []any{"2001-01-02", nil, "2001-01-02"} {
			s := referenceSelect(heldQuery, []string{"overlaps"}, []string{"BOOL"}, [][]any{{true}}, arg)
			s.checkMetadata = datetimeOverlapsMetadata
			referenceRunStep(t, p.ctx, held, b.recorder, s)
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET TIME ZONE 'America/Denver'", 0))
		datetimeOverlapsBoth(t, p, b, c, referenceSelect("SELECT (TIMESTAMPTZ '2005-04-02 12:00-07',INTERVAL '1 day') OVERLAPS (TIMESTAMPTZ '2005-04-03 12:30-06',TIMESTAMPTZ '2005-04-03 12:30-06') AS calendar,(TIMESTAMPTZ '2005-04-02 12:00-07',INTERVAL '24 hours') OVERLAPS (TIMESTAMPTZ '2005-04-03 12:30-06',TIMESTAMPTZ '2005-04-03 12:30-06') AS elapsed", []string{"calendar", "elapsed"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}))
		invalid := "SELECT (1,2) OVERLAPS (3,4)"
		d := datetimeOverlapsDiagnostic("42883", "function pg_catalog.overlaps(integer, integer, integer, integer) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", invalid, "OVERLAPS")
		datetimeOverlapsFailure(t, p, b, c, invalid, d)
		datetimeOverlapsValue(t, p, b, c, "(TIME '01:00',TIME '03:00') OVERLAPS (TIME '02:00',TIME '04:00')", true)
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE VIEW ov_smoke AS SELECT (TIMESTAMP '2001-01-01',TIME '01:00') OVERLAPS (TIMESTAMP '2001-01-01',TIME '02:00') AS hit", 0))
		view := referenceSelect("SELECT hit FROM ov_smoke", []string{"hit"}, []string{"BOOL"}, [][]any{{true}})
		view.checkMetadata = datetimeOverlapsStoredMetadata(b.pg)
		datetimeOverlapsBoth(t, p, b, c, view)
		if !b.pg {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			datetimeOverlapsBoth(t, p, b, c, view)
		}
	})
}
