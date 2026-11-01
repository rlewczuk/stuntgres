package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceXMLXPathAtomicity(t *testing.T) {
	fixture.ArrayReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{fixture.Exec("CREATE SEQUENCE xpath_atomic_seq", 0), fixture.Exec("CREATE TABLE xpath_atomic(id integer,v xml[])", 0), fixture.Exec("INSERT INTO xpath_atomic VALUES(10,'{<old/>}'),(20,'{<old/>}')", 2)} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		want := fixture.Select("SELECT id,v FROM xpath_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "_XML"}, [][]any{{int64(10), "{<old/>}"}, {int64(20), "{<old/>}"}})
		diagnostic := func(code, message, detail string) fixture.Diagnostic {
			d := fixture.ControlsReferenceDiagnostic(code, message)
			d.Detail = detail
			return d
		}
		for _, tc := range []struct {
			call       string
			diagnostic fixture.Diagnostic
		}{
			{"xpath(CASE WHEN nextval('xpath_atomic_seq')=1 THEN '/r' ELSE '[' END,'<r/>'::xml,'{}'::text[])", diagnostic("10608", "invalid XPath expression", "Invalid expression")},
			{"xpath('/r',(CASE WHEN nextval('xpath_atomic_seq')=1 THEN '<r/>' ELSE '<a/><b/>' END)::xml,'{}'::text[])", diagnostic("2200M", "could not parse XML document", "line 1: Extra content at the end of the document\n<a/><b/>\n    ^")},
			{"xpath('/r','<r/>'::xml,ARRAY[ARRAY[CASE WHEN nextval('xpath_atomic_seq')=1 THEN 'q' ELSE NULL END,'urn:x']])", diagnostic("22004", "neither namespace name nor URI may be null", "")},
		} {
			for _, prepared := range []bool{false, true} {
				for _, q := range []string{"INSERT INTO xpath_atomic SELECT id," + tc.call + " FROM (VALUES(1),(2)) t(id)", "UPDATE xpath_atomic SET v=" + tc.call} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER SEQUENCE xpath_atomic_seq RESTART WITH 1", 0))
					failure := fixture.ErrorDiagnostic(q, tc.diagnostic)
					failure.Prepared = prepared
					failure.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, failure)
					fixture.SQLJSONBoth(t, p, b, c, fixture.Select("SELECT last_value AS v FROM xpath_atomic_seq", []string{"v"}, []string{"INT8"}, [][]any{{int64(2)}}))
					fixture.SQLJSONBoth(t, p, b, c, want)
				}
			}
		}
	})
}
