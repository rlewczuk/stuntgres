package reference_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAlterStatisticsCoercion(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE statistics_coercion(a integer)", 0))
	values := make([]string, 20)
	for i := range values {
		values[i] = fmt.Sprintf("(%d)", i+1)
	}
	p.Step(t, fixture.Exec("INSERT INTO statistics_coercion VALUES"+strings.Join(values, ","), 20))
	p.Step(t, fixture.Exec("ALTER TABLE statistics_coercion ALTER a SET STATISTICS 3", 0))
	p.Step(t, fixture.Exec("ANALYZE statistics_coercion", 0))
	sentinel := statisticsMetadata("statistics_coercion", []any{"a", int64(3), "{n_distinct=2,n_distinct_inherited=2}"})
	saved := statisticsMetadata("statistics_coercion", []any{"a", int64(3), "{n_distinct=02,n_distinct_inherited=10}"})
	rawOptions := func(local, inherited string) fixture.Step {
		// Element projections avoid depending on array-output escaping of control
		// characters while still requiring the exact original stored text.
		return fixture.Select("SELECT attstattarget,attoptions[1] AS first_option,attoptions[2] AS second_option FROM pg_attribute WHERE attrelid='statistics_coercion'::regclass AND attnum=1", []string{"attstattarget", "first_option", "second_option"}, []string{"INT2", "TEXT", "TEXT"}, [][]any{{int64(3), "n_distinct=" + local, "n_distinct_inherited=" + inherited}})
	}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			query := "EXPLAIN SELECT a FROM statistics_coercion WHERE a=1"
			statement, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer statement.Close()
			held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}
			statisticsEstimate(t, p, held, query, 1)
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct {
					input, text string
					estimate    int64
				}{
					{"02", "2", 10}, {"+2", "2", 10}, {"1_0", "10", 2}, {"0o2", "2", 10},
					{"'02'", "02", 10}, {"'+2'", "+2", 10}, {"2.0", "2.0", 10}, {"'0x2.0'", "0x2.0", 10},
				} {
					action := fixture.Exec("ALTER TABLE statistics_coercion ALTER a SET(n_distinct="+tc.input+",n_distinct_inherited="+tc.input+")", 0)
					action.Prepared = prepared
					run(action)
					run(statisticsMetadata("statistics_coercion", []any{"a", int64(3), "{n_distinct=" + tc.text + ",n_distinct_inherited=" + tc.text + "}"}))
					run(fixture.Exec("ANALYZE statistics_coercion", 0))
					statisticsEstimate(t, p, held, query, tc.estimate)
					run(fixture.Prepared(fixture.Select("SELECT a FROM statistics_coercion WHERE a=1 ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}})))
				}
				for _, padding := range []string{" ", "\t", "\n", "\r", "\f", "\v"} {
					text := padding + "2" + padding
					action := fixture.Exec("ALTER TABLE statistics_coercion ALTER a SET(n_distinct='"+text+"',n_distinct_inherited='"+text+"')", 0)
					action.Prepared = prepared
					run(action)
					run(rawOptions(text, text))
					run(fixture.Exec("ANALYZE statistics_coercion", 0))
					statisticsEstimate(t, p, held, query, 10)
				}
			}
			run(fixture.Exec("ALTER TABLE statistics_coercion ALTER a SET(n_distinct=2,n_distinct_inherited=2)", 0))
			run(sentinel)
			for _, name := range []string{"n_distinct", "n_distinct_inherited"} {
				for _, tc := range []struct {
					input, text string
					bounds      bool
				}{
					{"'1_0'", "1_0", false}, {"'0o2'", "0o2", false}, {"-02", "-2", true}, {"'-02'", "-02", true},
					{"'\u00a02'", "\u00a02", false}, {"'2\u00a0'", "2\u00a0", false},
				} {
					message := fmt.Sprintf(`invalid value for floating point option "%s": %s`, name, tc.text)
					detail := ""
					if tc.bounds {
						message = fmt.Sprintf(`value %s out of bounds for option "%s"`, tc.text, name)
						detail = statisticsBounds
					}
					for _, prepared := range []bool{false, true} {
						query := "ALTER TABLE statistics_coercion ALTER a SET STATISTICS 7, ALTER a SET(n_distinct=4), ALTER a SET(" + name + "=" + tc.input + ")"
						action := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22023", Message: message, Detail: detail})
						action.Prepared = prepared
						if prepared {
							action.ErrorStage = "execute"
						}
						run(action)
						run(sentinel)
					}
				}
			}
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("ALTER TABLE statistics_coercion ALTER a SET STATISTICS 5", 0))
			failure := fixture.ImplicitError("ALTER TABLE statistics_coercion ALTER a SET STATISTICS 7, ALTER a SET(n_distinct='2\u00a0')", fixture.Diagnostic{Code: "22023", Message: "invalid value for floating point option \"n_distinct\": 2\u00a0"})
			failure.Prepared = true
			failure.ErrorStage = "execute"
			run(failure)
			run(fixture.Exec("ROLLBACK", 0))
			run(sentinel)
			run(fixture.Exec("ALTER TABLE statistics_coercion ALTER a SET(n_distinct='02',n_distinct_inherited=1_0)", 0))
			run(saved)
		})
	}
	p.Step(t, saved)
	// Logical replay must preserve quoted spelling while replaying the already
	// normalized semantic text of unquoted SQL integers.
	before := storageDump(t, p)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(before)); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, p.Memory, p.Backends()[0].Recorder, saved)
	if !bytes.Equal(storageDump(t, p), before) {
		t.Fatal("coerced option spelling or data changed on logical replay")
	}
	fixture.RunStep(t, p.Ctx, p.Memory, p.Backends()[0].Recorder, fixture.Exec("ANALYZE statistics_coercion", 0))
	statisticsEstimate(t, p, p.Memory, "EXPLAIN SELECT a FROM statistics_coercion WHERE a=1", 10)
	fixture.RunStep(t, p.Ctx, p.Memory, p.Backends()[0].Recorder, fixture.Select("SELECT count(*) AS actual FROM statistics_coercion", []string{"actual"}, []string{"INT8"}, [][]any{{int64(20)}}))
}
