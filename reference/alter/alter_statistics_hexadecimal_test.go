package reference_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAlterStatisticsHexadecimal(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE statistics_hex(a integer)", 0))
	values := make([]string, 20)
	for i := range values {
		values[i] = fmt.Sprintf("(%d)", i+1)
	}
	p.Step(t, fixture.Exec("INSERT INTO statistics_hex VALUES"+strings.Join(values, ","), 20))
	p.Step(t, fixture.Exec("ANALYZE statistics_hex", 0))
	canonical := statisticsMetadata("statistics_hex", []any{"a", nil, "{n_distinct=0x2.0,n_distinct_inherited=0x4.0}"})
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			query := "EXPLAIN SELECT a FROM statistics_hex WHERE a=1"
			statement, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer statement.Close()
			held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}
			statisticsEstimate(t, p, held, query, 1)
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct{ local, inherited string }{
					{"0x2.0", "0x4.0"}, {"0X2", "0X4"}, {"+0x2.0", "-0x0.8"}, {"0x2p0", "0x4p0"},
				} {
					step := fixture.Exec("ALTER TABLE statistics_hex ALTER a SET(n_distinct='"+tc.local+"',n_distinct_inherited='"+tc.inherited+"')", 0)
					step.Prepared = prepared
					run(step)
					run(statisticsMetadata("statistics_hex", []any{"a", nil, "{n_distinct=" + tc.local + ",n_distinct_inherited=" + tc.inherited + "}"}))
					run(fixture.Exec("ANALYZE statistics_hex", 0))
					// Every local spelling is exactly2, so20 unique non-MCV rows estimate10.
					// The original lexical text, not the conversion-only p0 suffix, persists.
					statisticsEstimate(t, p, held, query, 10)
					run(fixture.Prepared(fixture.Select("SELECT a FROM statistics_hex WHERE a=1 ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}})))
				}
			}
			run(fixture.Exec("ALTER TABLE statistics_hex ALTER a SET(n_distinct='0x2.0',n_distinct_inherited='0x4.0')", 0))
			run(canonical)
			for _, name := range []string{"n_distinct", "n_distinct_inherited"} {
				for _, tc := range []struct{ text, message, detail string }{
					{"0x2.0junk", fmt.Sprintf(`invalid value for floating point option "%s": 0x2.0junk`, name), ""},
					{"-0x2.0", fmt.Sprintf(`value -0x2.0 out of bounds for option "%s"`, name), statisticsBounds},
				} {
					for _, prepared := range []bool{false, true} {
						query := "ALTER TABLE statistics_hex ALTER a SET(n_distinct='0x4.0'), ALTER a SET(" + name + "='" + tc.text + "')"
						step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22023", Message: tc.message, Detail: tc.detail})
						step.Prepared = prepared
						if prepared {
							step.ErrorStage = "execute"
						}
						run(step)
						run(canonical)
					}
				}
			}
		})
	}
	p.Step(t, canonical)
	// Native logical replay must preserve the same accepted spelling through
	// candidate/provider validation, not replace it with a decimal or p0 form.
	before := storageDump(t, p)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(before)); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, p.Memory, p.Backends()[0].Recorder, canonical)
	if !bytes.Equal(storageDump(t, p), before) {
		t.Fatal("hexadecimal configuration redump changed lexical text or data")
	}
	fixture.RunStep(t, p.Ctx, p.Memory, p.Backends()[0].Recorder, fixture.Exec("ANALYZE statistics_hex", 0))
	statisticsEstimate(t, p, p.Memory, "EXPLAIN SELECT a FROM statistics_hex WHERE a=1", 10)
	fixture.RunStep(t, p.Ctx, p.Memory, p.Backends()[0].Recorder, fixture.Select("SELECT count(*) AS actual FROM statistics_hex", []string{"actual"}, []string{"INT8"}, [][]any{{int64(20)}}))
}
