package reference_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAlterStatisticsUnderflow(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE statistics_underflow(a integer)", 0))
	p.Step(t, fixture.Exec("INSERT INTO statistics_underflow VALUES(1),(2),(NULL)", 3))
	p.Step(t, fixture.Exec("ALTER TABLE statistics_underflow ALTER a SET STATISTICS 3", 0))
	data := fixture.Select("SELECT a FROM statistics_underflow ORDER BY a NULLS LAST", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {nil}})
	// Binary64 powers of two and the largest subnormal are exact conversions,
	// not ERANGE merely because they fall below the smallest normal value.
	for _, text := range []string{
		"0x1p-1023", "0x1p-1074", "-0x1p-1023", "-0x1p-1074",
		"0x0.fffffffffffffp-1022", "0x1p-1022",
		"0x0." + strings.Repeat("0", 255) + "2", // 2*16^-256 = 2^-1023, no p exponent.
	} {
		for _, prepared := range []bool{false, true} {
			step := fixture.Exec("ALTER TABLE statistics_underflow ALTER a SET(n_distinct='"+text+"',n_distinct_inherited='"+text+"')", 0)
			step.Prepared = prepared
			p.Step(t, step)
			p.Step(t, statisticsMetadata("statistics_underflow", []any{"a", int64(3), "{n_distinct=" + text + ",n_distinct_inherited=" + text + "}"}))
			selected := data
			selected.Prepared = prepared
			p.Step(t, selected)
		}
	}
	p.Step(t, fixture.Exec("ALTER TABLE statistics_underflow ALTER a SET(n_distinct='0x1p-1023',n_distinct_inherited='-0x1p-1074')", 0))
	sentinel := statisticsMetadata("statistics_underflow", []any{"a", int64(3), "{n_distinct=0x1p-1023,n_distinct_inherited=-0x1p-1074}"})
	p.Step(t, sentinel)
	for _, name := range []string{"n_distinct", "n_distinct_inherited"} {
		for _, text := range []string{"0x1.0000000000001p-1023", "-0x1.0000000000001p-1023", "5e-324", "-5e-324", "0x1p-1075", "-0x1p-1075", "1e-400", "-1e-400"} {
			for _, prepared := range []bool{false, true} {
				query := "ALTER TABLE statistics_underflow ALTER a SET STATISTICS 7, ALTER a SET(n_distinct=4), ALTER a SET(" + name + "='" + text + "')"
				step := statisticsError(query, "22023", fmt.Sprintf(`invalid value for floating point option "%s": %s`, name, text))
				step.Prepared = prepared
				if prepared {
					step.ErrorStage = "execute"
				}
				p.Step(t, step)
				p.Step(t, sentinel)
				p.Step(t, data)
			}
		}
	}
	// No tiny-value EXPLAIN golden is inferred here: PostgreSQL's collected
	// stadistinct has its separate FLOAT4 representation. This witness is exact
	// real-option input/configuration ownership, lexical replay, and data invariance.
	before := storageDump(t, p)
	for _, name := range []string{"n_distinct", "n_distinct_inherited"} {
		bad := `CREATE TABLE public.statistics_restore(a integer); ALTER TABLE ONLY public.statistics_restore ALTER a SET(n_distinct='0x1p-1023'); ALTER TABLE ONLY public.statistics_restore ALTER a SET(` + name + `='5e-324');`
		err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
		want := fixture.ControlsReferenceDiagnostic("22023", fmt.Sprintf(`invalid value for floating point option "%s": 5e-324`, name))
		fixture.AssertDiagnostic(t, err, &want)
		if !bytes.Equal(storageDump(t, p), before) {
			t.Fatal("failed later underflow replay published its valid prefix", name)
		}
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(before)); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, p.Memory, p.Backends()[0].Recorder, sentinel)
	fixture.RunStep(t, p.Ctx, p.Memory, p.Backends()[0].Recorder, data)
	if !bytes.Equal(storageDump(t, p), before) {
		t.Fatal("exact subnormal configuration/data changed on replay")
	}
}
