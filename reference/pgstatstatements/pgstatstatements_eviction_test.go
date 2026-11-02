package reference_test

import (
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsEviction(t *testing.T) {
	pgssCase(t, true, func(s *pgssScenario) {
		s.both(fixture.Select("SHOW pg_stat_statements.max", []string{"pg_stat_statements.max"}, []string{"TEXT"}, [][]any{{"5000"}}))
		s.exec("CREATE TABLE pgss_part.pgss_eviction_source(k integer)")
		s.run(fixture.Exec("INSERT INTO pgss_part.pgss_eviction_source VALUES(7)", 1))
		var before int64
		var reset time.Time
		if err := s.observer.QueryRowContext(s.p.Ctx, "SELECT dealloc,stats_reset FROM pgss_part.pg_stat_statements_info").Scan(&before, &reset); err != nil {
			t.Fatal(err)
		}
		hot := "SELECT pgss_hot.k AS pgss_eviction_hot FROM pgss_part.pgss_eviction_source pgss_hot"
		s.counts(hot, 0, 0, 0)
		hotCalls := int64(0)
		heat := func(n int) {
			for range n {
				s.work(fixture.Select(hot, []string{"pgss_eviction_hot"}, []string{"INT4"}, [][]any{{int64(7)}}))
				hotCalls++
			}
		}
		heat(100)
		// Effective FROM aliases, unlike constants or output aliases, are
		// structural PG18 jumble fields. These are 5100 genuinely distinct
		// cold identities, all with exactly one completed call/row.
		const shapes = 5100
		for i := range shapes {
			alias := fmt.Sprintf("pgss_cold_%04d", i)
			q := "SELECT " + alias + ".k AS pgss_eviction_cold FROM pgss_part.pgss_eviction_source " + alias
			s.work(fixture.Select(q, []string{"pgss_eviction_cold"}, []string{"INT4"}, [][]any{{int64(7)}}))
			if i%100 == 99 {
				heat(1)
			}
			if i%1000 == 999 {
				s.both(fixture.Select("SELECT count(*)<=5000 AS bounded FROM pgss_part.pg_stat_statements", []string{"bounded"}, []string{"BOOL"}, [][]any{{true}}))
			}
		}
		s.entry(hot, hotCalls, hotCalls)
		q := "SELECT count(*) AS retained,count(DISTINCT queryid) AS identities,COALESCE(bool_and(calls=1 AND rows=1),false) AS exact FROM pgss_part.pg_stat_statements WHERE query LIKE 'SELECT pgss_cold_% AS pgss_eviction_cold FROM pgss_part.pgss_eviction_source%' AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())"
		var retained, identities int64
		var exact bool
		if err := s.observer.QueryRowContext(s.p.Ctx, q).Scan(&retained, &identities, &exact); err != nil {
			t.Fatal(err)
		}
		if retained <= 0 || retained >= shapes || retained != identities || !exact {
			t.Fatalf("retained=%d identities=%d exact=%v, want actual distinct bounded one-call survivors", retained, identities, exact)
		}
		s.both(fixture.Select(q, []string{"retained", "identities", "exact"}, []string{"INT8", "INT8", "BOOL"}, [][]any{{retained, retained, true}}))
		var after int64
		var afterReset time.Time
		if err := s.observer.QueryRowContext(s.p.Ctx, "SELECT dealloc,stats_reset FROM pgss_part.pg_stat_statements_info").Scan(&after, &afterReset); err != nil {
			t.Fatal(err)
		}
		// The number of evicted tied cold entries is not a portable constant.
		// dealloc is an event counter, not the count of removed entries.
		if after <= before || after-before >= shapes-retained || !afterReset.Equal(reset) {
			t.Fatalf("deallocation before=%d after=%d removed cold=%d reset=%v/%v", before, after, shapes-retained, reset, afterReset)
		}
		s.both(fixture.Select("SELECT count(*)<=5000 AS bounded FROM pgss_part.pg_stat_statements", []string{"bounded"}, []string{"BOOL"}, [][]any{{true}}))
	})
}
