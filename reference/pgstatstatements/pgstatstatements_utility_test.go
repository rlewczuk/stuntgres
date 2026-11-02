package reference_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsUtility(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared_%v", prepared), func(t *testing.T) {
			pgssCase(t, true, func(s *pgssScenario) {
				suffix := "direct"
				if prepared {
					suffix = "prepared"
				}
				prefix := "pgss_part.pgss_utility_" + suffix
				work := func(step fixture.Step) {
					step.Prepared = prepared
					s.work(step)
				}
				off := "CREATE TABLE " + prefix + "_off(k integer)"
				s.counts(off, 0, 0, 0)
				work(fixture.Exec(off, 0))
				s.counts(off, 0, 0, 0)
				s.work(fixture.Exec("SET pg_stat_statements.track_utility = on", 0))
				create := "CREATE TABLE " + prefix + "_source(k integer)"
				s.counts(create, 0, 0, 0)
				work(fixture.Exec(create, 0))
				s.counts(create, 1, 1, 0)
				work(fixture.ImplicitError(create, fixture.Diagnostic{Code: "42P07", Message: `relation "pgss_utility_` + suffix + `_source" already exists`}))
				s.counts(create, 1, 1, 0)
				s.run(fixture.Exec("INSERT INTO "+prefix+"_source VALUES(1),(2),(3)", 3))
				for _, c := range []struct {
					q              string
					affected, rows int64
				}{
					{"CREATE TABLE " + prefix + "_ctas AS SELECT k FROM " + prefix + "_source", 3, 3},
					{"SELECT k INTO " + prefix + "_into FROM " + prefix + "_source", 3, 3},
					{"CREATE MATERIALIZED VIEW " + prefix + "_mv AS SELECT k FROM " + prefix + "_source", 3, 3},
					{"REFRESH MATERIALIZED VIEW " + prefix + "_mv", 0, 3},
				} {
					s.counts(c.q, 0, 0, 0)
					work(fixture.Exec(c.q, c.affected))
					s.counts(c.q, 1, 1, c.rows)
				}
				set := "SET application_name = 'pgss_utility_" + suffix + "'"
				normalizedSet := "SET application_name = $1"
				var setGroups, setCalls, setRows int64
				baseline := "SELECT count(*),COALESCE(sum(calls),0)::bigint,COALESCE(sum(rows),0)::bigint FROM pgss_part.pg_stat_statements WHERE query=$1 AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())"
				if err := s.observer.QueryRowContext(s.p.Ctx, baseline, normalizedSet).Scan(&setGroups, &setCalls, &setRows); err != nil {
					t.Fatal(err)
				}
				// SET values share the released normalized representative, and
				// history survives schema/extension replacement between cases.
				if setGroups < 0 || setGroups > 1 || setCalls < setGroups || setRows != 0 || (setGroups == 0 && setCalls != 0) {
					t.Fatalf("incoherent SET baseline groups=%d calls=%d rows=%d", setGroups, setCalls, setRows)
				}
				s.counts(normalizedSet, setGroups, setCalls, 0)
				work(fixture.Exec(set, 0))
				s.counts(normalizedSet, 1, setCalls+1, 0)
				s.entry(normalizedSet, setCalls+1, 0)
				// SQL PREPARE/EXECUTE are charged to the prepared body, not
				// duplicate utility executions. Named DEALLOCATE is tracked.
				prepare := "PREPARE pgss_utility_body(integer) AS SELECT k AS pgss_utility_prepared FROM " + prefix + "_source WHERE k=$1"
				s.work(fixture.Exec(prepare, 0))
				s.work(fixture.Select("EXECUTE pgss_utility_body(1)", []string{"pgss_utility_prepared"}, []string{"INT4"}, [][]any{{int64(1)}}))
				s.work(fixture.Select("EXECUTE pgss_utility_body(2)", []string{"pgss_utility_prepared"}, []string{"INT4"}, [][]any{{int64(2)}}))
				s.counts(prepare, 1, 2, 2)
				s.counts("EXECUTE pgss_utility_body%", 0, 0, 0)
				normalizedDeallocate := "DEALLOCATE $1"
				var deallocateGroups, deallocateCalls, deallocateRows int64
				if err := s.observer.QueryRowContext(s.p.Ctx, baseline, normalizedDeallocate).Scan(&deallocateGroups, &deallocateCalls, &deallocateRows); err != nil {
					t.Fatal(err)
				}
				if deallocateGroups < 0 || deallocateGroups > 1 || deallocateCalls < deallocateGroups || deallocateRows != 0 || (deallocateGroups == 0 && deallocateCalls != 0) {
					t.Fatalf("incoherent DEALLOCATE baseline groups=%d calls=%d rows=%d", deallocateGroups, deallocateCalls, deallocateRows)
				}
				s.counts(normalizedDeallocate, deallocateGroups, deallocateCalls, 0)
				s.work(fixture.Exec("DEALLOCATE pgss_utility_body", 0))
				s.counts(normalizedDeallocate, 1, deallocateCalls+1, 0)
				s.entry(normalizedDeallocate, deallocateCalls+1, 0)
				s.counts("DEALLOCATE pgss_utility_body", 0, 0, 0)
				s.work(fixture.Exec("BEGIN", 0))
				defer s.worker.ExecContext(s.p.Ctx, "ROLLBACK")
				declare := "DECLARE pgss_utility_cursor CURSOR FOR SELECT k FROM " + prefix + "_source ORDER BY k"
				work(fixture.Exec(declare, 0))
				fetch := "FETCH FORWARD 1 FROM pgss_utility_cursor"
				var forwardGroups, forwardCalls, forwardRows int64
				if err := s.observer.QueryRowContext(s.p.Ctx, baseline, fetch).Scan(&forwardGroups, &forwardCalls, &forwardRows); err != nil {
					t.Fatal(err)
				}
				if forwardGroups < 0 || forwardGroups > 1 || forwardCalls < forwardGroups || forwardRows != forwardCalls || (forwardGroups == 0 && forwardCalls != 0) {
					t.Fatalf("incoherent FETCH FORWARD baseline groups=%d calls=%d rows=%d", forwardGroups, forwardCalls, forwardRows)
				}
				s.counts(fetch, forwardGroups, forwardCalls, forwardRows)
				for i := int64(1); i <= 2; i++ {
					work(fixture.Select(fetch, []string{"k"}, []string{"INT4"}, [][]any{{i}}))
					s.counts(fetch, 1, forwardCalls+i, forwardRows+i)
					s.entry(fetch, forwardCalls+i, forwardRows+i)
				}
				fetchAll := "FETCH ALL FROM pgss_utility_cursor"
				var allGroups, allCalls, allRows int64
				if err := s.observer.QueryRowContext(s.p.Ctx, baseline, fetchAll).Scan(&allGroups, &allCalls, &allRows); err != nil {
					t.Fatal(err)
				}
				if allGroups < 0 || allGroups > 1 || allCalls < allGroups || allRows != allCalls || (allGroups == 0 && allCalls != 0) {
					t.Fatalf("incoherent FETCH ALL baseline groups=%d calls=%d rows=%d", allGroups, allCalls, allRows)
				}
				s.counts(fetchAll, allGroups, allCalls, allRows)
				work(fixture.Select(fetchAll, []string{"k"}, []string{"INT4"}, [][]any{{int64(3)}}))
				s.counts(fetchAll, 1, allCalls+1, allRows+1)
				s.entry(fetchAll, allCalls+1, allRows+1)
				s.work(fixture.Exec("COMMIT", 0))
			})
		})
	}
	t.Run("copy_success_and_failure", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			s.exec("CREATE TABLE pgss_part.pgss_utility_copy(k integer)")
			pool := fixture.CopyScenarioPool(t, s.p, s.b.PG, "", "")
			c, err := pool.Conn(s.p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, q := range []string{"SET pg_stat_statements.track = top", "SET pg_stat_statements.track_utility = on", "SET pg_stat_statements.track_planning = off"} {
				if _, err := c.ExecContext(s.p.Ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			q := "COPY pgss_part.pgss_utility_copy FROM STDIN"
			s.counts(q, 0, 0, 0)
			result, err := fixture.CopyFrom(s.p.Ctx, c, s.b.PG, q, strings.NewReader("1\n2\n3\n"))
			if err != nil || result != (stuntgres.CopyResult{Count: 3, CommandTag: "COPY 3"}) {
				t.Fatalf("COPY result=%+v error=%v", result, err)
			}
			s.counts(q, 1, 1, 3)
			result, err = fixture.CopyFrom(s.p.Ctx, c, s.b.PG, q, strings.NewReader("4\nbad\n"))
			want := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
			want.Context = `COPY pgss_utility_copy, line 2, column k: "bad"`
			fixture.AssertDiagnostic(t, err, &want)
			if result != (stuntgres.CopyResult{}) {
				t.Fatalf("failed COPY returned %+v", result)
			}
			s.counts(q, 1, 1, 3)
			s.both(fixture.Select("SELECT k FROM pgss_part.pgss_utility_copy ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
			var output bytes.Buffer
			q = "COPY pgss_part.pgss_utility_copy TO STDOUT"
			result, err = fixture.CopyTo(s.p.Ctx, c, s.b.PG, q, &output)
			if err != nil || result != (stuntgres.CopyResult{Count: 3, CommandTag: "COPY 3"}) || output.String() != "1\n2\n3\n" {
				t.Fatalf("COPY output=%q result=%+v error=%v", output.String(), result, err)
			}
			s.counts(q, 1, 1, 3)
		})
	})
	for _, mode := range []struct {
		name, track, utility string
		wrapper, child       bool
	}{
		{"all", "all", "on", true, true},
		{"top", "top", "on", true, false},
		{"all_utility_off", "all", "off", false, true},
		{"top_utility_off", "top", "off", false, false},
	} {
		t.Run("copy_query_"+mode.name, func(t *testing.T) {
			pgssCase(t, true, func(s *pgssScenario) {
				table := "pgss_part.pgss_utility_copy_query_" + mode.name
				s.exec("CREATE TABLE " + table + "(k integer)")
				s.run(fixture.Exec("INSERT INTO "+table+" VALUES(1),(2),(3)", 3))
				pool := fixture.CopyScenarioPool(t, s.p, s.b.PG, "", "")
				c, err := pool.Conn(s.p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				for _, q := range []string{"SET pg_stat_statements.track = '" + mode.track + "'", "SET pg_stat_statements.track_utility = " + mode.utility, "SET pg_stat_statements.track_planning = off"} {
					if _, err := c.ExecContext(s.p.Ctx, q); err != nil {
						t.Fatal(err)
					}
				}
				q := "COPY (SELECT k FROM " + table + " ORDER BY k) TO STDOUT"
				s.counts(q, 0, 0, 0)
				var output bytes.Buffer
				result, err := fixture.CopyTo(s.p.Ctx, c, s.b.PG, q, &output)
				if err != nil || result != (stuntgres.CopyResult{Count: 3, CommandTag: "COPY 3"}) || output.String() != "1\n2\n3\n" {
					t.Fatalf("COPY query output=%q result=%+v error=%v", output.String(), result, err)
				}
				var entries [][]any
				if mode.child {
					entries = append(entries, []any{true, false, int64(1), int64(3)})
				}
				if mode.wrapper {
					entries = append(entries, []any{true, true, int64(1), int64(3)})
				}
				groups := int64(len(entries))
				s.counts(q, groups, groups, 3*groups)
				// Released COPY independently analyzes its child but gives it the
				// whole COPY source span. Both real frames therefore share text,
				// not identity or toplevel; no extra SELECT-text entry is expected.
				read := " FROM pgss_part.pg_stat_statements WHERE query=$1 AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())"
				s.both(fixture.Select("SELECT queryid<>0 AS identified,toplevel,calls,rows"+read+" ORDER BY toplevel", []string{"identified", "toplevel", "calls", "rows"}, []string{"BOOL", "BOOL", "INT8", "INT8"}, entries, q))
				s.both(fixture.Select("SELECT count(DISTINCT queryid) AS identities"+read, []string{"identities"}, []string{"INT8"}, [][]any{{groups}}, q))
				s.counts("SELECT k FROM "+table+"%", 0, 0, 0)
				failed := "COPY (SELECT 12/(k-2) FROM " + table + " ORDER BY k) TO STDOUT"
				failedPattern := "COPY (SELECT %/(k-%) FROM " + table + " ORDER BY k) TO STDOUT"
				s.counts(failedPattern, 0, 0, 0)
				output.Reset()
				result, err = fixture.CopyTo(s.p.Ctx, c, s.b.PG, failed, &output)
				want := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
				fixture.AssertDiagnostic(t, err, &want)
				if result != (stuntgres.CopyResult{}) {
					t.Fatalf("failed COPY query returned %+v", result)
				}
				s.counts(failedPattern, 0, 0, 0)
				s.counts(q, groups, groups, 3*groups)
			})
		})
	}
}
