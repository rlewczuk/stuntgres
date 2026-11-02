package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceOverexplainRangeFeatureSmoke(t *testing.T) {
	overexplainRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		overexplainRangeUnloaded(t, p, b, c)
		overexplainRangeSeed(t, p, b, c)
		overexplainRangeProviderSetup(t, p, b, c)
		overexplainExec(t, p, b, c, "LOAD 'pg_overexplain'")
		const child = "SELECT a.id,b.v FROM range_src a JOIN range_src b ON a.id=b.id JOIN (SELECT id FROM range_src OFFSET 0) d ON d.id=a.id"
		var retained overexplainResult
		var snapshot string
		var principal string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&principal); err != nil {
			t.Fatal(err)
		}
		for _, format := range []string{"TEXT", "XML", "JSON", "YAML"} {
			for _, prepared := range []bool{false, true} {
				q := overexplainSQL(format, "RANGE_TABLE, VERBOSE, COSTS FALSE", child)
				r := overexplainQuery(t, p, c, q, format, prepared)
				entries := overexplainRange(t, b, r, format, true)
				if format == "TEXT" {
					if b.PG {
						explainContains(t, []string{r.text}, "RTI ", "Eref: a (id, v)", "Eref: b (id, v)", "Eref: d (id)", "Permission Info Index:")
					} else {
						explainContains(t, []string{r.text}, "Range Name: a", "Range Name: b", "Range Name: d", "Required Permissions:")
					}
				} else {
					a := overexplainRangeNamed(t, b, entries, "relation", "a")
					bb := overexplainRangeNamed(t, b, entries, "relation", "b")
					d := overexplainRangeNamed(t, b, entries, "subquery", "d")
					if b.PG {
						if a["Relation"] != "public.range_src" || bb["Relation"] != "public.range_src" || a["RTI"] == bb["RTI"] || d["Eref"] != "d (id)" {
							t.Fatal("PG smoke actual RTEs", entries)
						}
						for _, e := range []map[string]any{a, bb} {
							if overexplainInteger(t, e["Permission Info Index"], format == "XML") <= 0 {
								t.Fatal("PG permission reference", e)
							}
						}
					} else {
						if a["Relation OID"] != bb["Relation OID"] || a["Entry ID"] == bb["Entry ID"] || a["Occurrence"] == bb["Occurrence"] || d["Child Scope ID"] == nil {
							t.Fatal("native actual occurrence/scope identity", entries)
						}
						if format == "JSON" {
							overexplainRangeRelation(t, p, c, a, "range_src", "JSON")
							overexplainRangeDemands(t, a, principal, []overexplainRangeDemand{{"SELECT", []string{"id"}, false}})
							overexplainRangeDemands(t, bb, principal, []overexplainRangeDemand{{"SELECT", []string{"id", "v"}, false}})
						}
					}
				}
				if format == "JSON" && !prepared {
					retained, snapshot = r, fmt.Sprint(r)
				}
				q = overexplainSQL(format, "RANGE_TABLE FALSE, DEBUG, COSTS FALSE", child)
				r = overexplainQuery(t, p, c, q, format, prepared)
				overexplainRange(t, b, r, format, false)
				overexplainDebug(t, b, r, format, true)
			}
		}
		q := overexplainSQL("JSON", "RANGE_TABLE 'bad', ANALYZE", "UPDATE range_src SET id=99")
		overexplainError(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("42601", "range_table requires a Boolean value"), "execute", true)
		overexplainRangeValues(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(child+" ORDER BY a.id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), nil}, {int64(3), "three"}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT nextval('range_seq')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
		overexplainProvider(t, p, c)
		overexplainProviderValues(t, p, b, c)
		if b.PG {
			return
		} // Only genuine native controls, never emulated SQL.
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		overexplainProviderValues(t, p, b, c)
		overexplainRangeProviderPlan(t, p, b, c, overexplainQuery(t, p, c, overexplainSQL("JSON", "RANGE_TABLE, VERBOSE", "SELECT id,v FROM overexplain_translated"), "JSON", true))
		overexplainRangeDestination(t, p, b, dump.Bytes(), false)
		if fmt.Sprint(retained) != snapshot {
			t.Fatal("detached smoke document mutated after native controls")
		}
	})
}
