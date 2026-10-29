package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterStatisticsMCV(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE statistics_mcv(a integer)", 0))
	values := []string{"(1)", "(1)", "(1)", "(1)"}
	for n := 2; n <= 17; n++ {
		values = append(values, fmt.Sprintf("(%d)", n))
	}
	p.Step(t, fixture.Exec("INSERT INTO statistics_mcv VALUES"+strings.Join(values, ","), 20))
	p.Step(t, fixture.Exec("ALTER TABLE statistics_mcv ALTER a SET STATISTICS 1", 0))
	p.Step(t, fixture.Exec("ANALYZE statistics_mcv", 0))
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			query := "EXPLAIN SELECT a FROM statistics_mcv WHERE a=2"
			statement, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer statement.Close()
			held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}
			statisticsEstimate(t, p, held, query, 1)
			for i, override := range []string{"2", "2.5", "-0.1"} {
				action := fixture.Exec("ALTER TABLE statistics_mcv ALTER a SET(n_distinct="+override+")", 0)
				action.Prepared = i%2 == 1
				run(action)
				run(statisticsMetadata("statistics_mcv", []any{"a", int64(1), "{n_distinct=" + override + "}"}))
				run(fixture.Exec("ANALYZE statistics_mcv", 0))
				// The real MCV has four of twenty rows. The sixteen residual rows
				// divided by effective distinct2 minus one MCV would yield16, but
				// PostgreSQL caps an unlisted value at the least-common MCV count4.
				statisticsEstimate(t, p, held, query, 4)
				statisticsEstimate(t, p, c, "EXPLAIN SELECT a FROM statistics_mcv WHERE a=1", 4)
				statisticsEstimate(t, p, c, "EXPLAIN SELECT a FROM statistics_mcv WHERE a<>2", 16)
				run(fixture.Prepared(fixture.Select("SELECT a FROM statistics_mcv WHERE a=2 ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}})))
				run(fixture.Select("SELECT a FROM statistics_mcv WHERE a=1 ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}}))
				run(fixture.Select("SELECT count(*) AS actual FROM statistics_mcv WHERE a<>2", []string{"actual"}, []string{"INT8"}, [][]any{{int64(19)}}))
			}
		})
	}
}

func TestReferenceAlterStatisticsInherited(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, query := range []string{
		"CREATE TABLE statistics_ip(k integer,c integer) PARTITION BY RANGE(k)",
		"CREATE TABLE statistics_ip0 PARTITION OF statistics_ip FOR VALUES FROM(0) TO(1)",
		"CREATE TABLE statistics_ip1 PARTITION OF statistics_ip FOR VALUES FROM(1) TO(2)",
		"ALTER TABLE statistics_ip ALTER c SET STATISTICS 4, ALTER c SET(n_distinct=2,n_distinct_inherited=4)",
		"ALTER TABLE statistics_ip0 ALTER c SET(n_distinct=4)",
		"ALTER TABLE statistics_ip1 ALTER c SET(n_distinct=4)",
	} {
		p.Step(t, fixture.Exec(query, 0))
	}
	values := make([]string, 24)
	for i := range values {
		values[i] = fmt.Sprintf("(%d,%d)", i/12, i+1)
	}
	p.Step(t, fixture.Exec("INSERT INTO statistics_ip VALUES"+strings.Join(values, ","), 24))
	for _, name := range []string{"statistics_ip0", "statistics_ip1", "statistics_ip"} {
		p.Step(t, fixture.Exec("ANALYZE "+name, 0))
	}
	p.Step(t, statisticsMetadata("statistics_ip", []any{"k", nil, nil}, []any{"c", int64(4), "{n_distinct=2,n_distinct_inherited=4}"}))
	for _, name := range []string{"statistics_ip0", "statistics_ip1"} {
		p.Step(t, statisticsMetadata(name, []any{"k", nil, nil}, []any{"c", int64(4), "{n_distinct=4}"}))
	}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			query := "EXPLAIN SELECT c FROM statistics_ip WHERE c=1"
			statement, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer statement.Close()
			held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}
			read := func(total, leaf int64) {
				t.Helper()
				lines := statisticsEstimate(t, p, held, query, total)
				if !b.PG {
					return
				} // Native owns one logical inherited source, not an Append.
				for _, name := range []string{"statistics_ip0", "statistics_ip1"} {
					found := 0
					for _, line := range lines {
						if !strings.Contains(line, " on "+name+" ") {
							continue
						}
						found++
						match := statisticsRowsPattern.FindStringSubmatch(line)
						if len(match) != 2 || match[1] != fmt.Sprint(leaf) {
							t.Fatal("PostgreSQL physical child estimate", name, leaf, line)
						}
					}
					if found != 1 {
						t.Fatal("missing/distorted PostgreSQL child source", name, lines)
					}
				}
			}
			// c is not the partition key, so both populations contribute. Native
			// consumes the parent's actual inherited fact:24/4=6. PostgreSQL's
			// physical Append has two local12/4 estimates:3+3=6. This does not
			// claim PostgreSQL uses the parent fact for its child scan estimates.
			read(6, 3)
			run(fixture.Select("SELECT c FROM statistics_ip WHERE c=1 ORDER BY c", []string{"c"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(fixture.Prepared(fixture.Exec("ALTER TABLE statistics_ip ALTER c SET(n_distinct_inherited=-0.25)", 0)))
			for _, name := range []string{"statistics_ip0", "statistics_ip1"} {
				run(fixture.Exec("ALTER TABLE "+name+" ALTER c SET(n_distinct=-0.5)", 0))
			}
			read(6, 3) // Configuration alone leaves collected facts stale.
			for _, name := range []string{"statistics_ip0", "statistics_ip1", "statistics_ip"} {
				run(fixture.Exec("ANALYZE "+name, 0))
			}
			run(statisticsMetadata("statistics_ip", []any{"k", nil, nil}, []any{"c", int64(4), "{n_distinct=2,n_distinct_inherited=-0.25}"}))
			for _, name := range []string{"statistics_ip0", "statistics_ip1"} {
				run(statisticsMetadata(name, []any{"k", nil, nil}, []any{"c", int64(4), "{n_distinct=-0.5}"}))
			}
			// Inherited native effective distinctness is .25*24=6, not .25*0
			// from the heapless parent. PostgreSQL's two physical estimates use
			// .5*12=6 each, producing2+2=4. Each expectation is independently derived.
			read(4, 2)
			run(fixture.Prepared(fixture.Select("SELECT c FROM statistics_ip WHERE c=1 ORDER BY c", []string{"c"}, []string{"INT4"}, [][]any{{int64(1)}})))
			// Hexadecimal spellings without p/P must reach this same actual
			// inherited consumer, not merely survive a metadata projection.
			for _, phase := range []struct {
				inherited, local string
				total, leaf      int64
			}{
				{"0x4.0", "0x4.0", 6, 3},
				{"-0x0.4", "-0x0.8", 4, 2},
			} {
				run(fixture.Prepared(fixture.Exec("ALTER TABLE statistics_ip ALTER c SET(n_distinct_inherited='"+phase.inherited+"')", 0)))
				for _, name := range []string{"statistics_ip0", "statistics_ip1"} {
					run(fixture.Exec("ALTER TABLE "+name+" ALTER c SET(n_distinct='"+phase.local+"')", 0))
				}
				for _, name := range []string{"statistics_ip0", "statistics_ip1", "statistics_ip"} {
					run(fixture.Exec("ANALYZE "+name, 0))
				}
				run(statisticsMetadata("statistics_ip", []any{"k", nil, nil}, []any{"c", int64(4), "{n_distinct=2,n_distinct_inherited=" + phase.inherited + "}"}))
				for _, name := range []string{"statistics_ip0", "statistics_ip1"} {
					run(statisticsMetadata(name, []any{"k", nil, nil}, []any{"c", int64(4), "{n_distinct=" + phase.local + "}"}))
				}
				read(phase.total, phase.leaf)
				run(fixture.Prepared(fixture.Select("SELECT c FROM statistics_ip WHERE c=1 ORDER BY c", []string{"c"}, []string{"INT4"}, [][]any{{int64(1)}})))
			}
		})
	}
}
