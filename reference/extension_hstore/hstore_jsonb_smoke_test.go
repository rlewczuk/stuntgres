package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHstoreJSONBFeatureSmoke(t *testing.T) {
	t.Run("conversion_metadata_and_atomicity", func(t *testing.T) {
		hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			hstoreJSONBControlSeed(t, p, b, c)
			hstoreJSONBControlVerify(t, p, b, c)
			q := hstoreJSONBQuery("v") + " FROM hs_jsonb_controls WHERE id=0"
			hstoreRun(t, p, b, c, hstoreJSONBPair(b, q, [][]any{}))
			hstoreJSONBAtomicity(t, p, b, c, "hstore_to_jsonb_loose")
			hstoreJSONBWhere(t, p, b, c)
			hstoreRun(t, p, b, c, hstoreJSONBPair(b, hstoreJSONBQuery("NULL::hstore"), [][]any{{nil, nil}}))
			hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_jsonb_smoke_assign(v jsonb)", 0))
			want := fixture.ControlsReferenceDiagnostic("42804", `column "v" is of type jsonb but expression is of type hstore`)
			want.Hint = "You will need to rewrite or cast the expression."
			want.Position = "43"
			hstoreError(t, p, b, c, "INSERT INTO hs_jsonb_smoke_assign VALUES ('a=>1'::hstore)", false, false, false, want)
			hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_jsonb_smoke_assign VALUES (('a=>1'::hstore)::jsonb)", 1))
			hstoreRun(t, p, b, c, hstoreJSONBSelect(b, "SELECT v FROM hs_jsonb_smoke_assign", []string{"v"}, [][]any{{`{"a": "1"}`}}, true))
			q = "SELECT hstore_to_jsonb_loose($1::hstore) AS j"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil { t.Fatal(err) }
			defer stmt.Close()
			hstoreRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, hstoreJSONBSelect(b, q, []string{"j"}, [][]any{{`{"a": 23450}`}}, false, "a=>2.345e+4"))
			hstoreRun(t, p, b, c, hstoreJSONBSelect(b, "SELECT ('a=>1e131072'::hstore)::jsonb AS j", []string{"j"}, [][]any{{`{"a": "1e131072"}`}}, false))
		})
	})
	t.Run("numeric_overflow_atomicity", func(t *testing.T) {
		hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			hstoreInstall(t, p, b, c)
			hstoreJSONBOverflowAtomicity(t, p, b, c)
		})
	})
	t.Run("same_restore_and_actual_oracle_replay", func(t *testing.T) { hstoreJSONBControlPhase(t, "same") })
	t.Run("fresh_authorized_restore", func(t *testing.T) { hstoreJSONBControlPhase(t, "fresh") })
}
