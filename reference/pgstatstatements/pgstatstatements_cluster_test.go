package reference_test

import (
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsCluster(t *testing.T) {
	var databases []string
	fixture.DatabaseReferenceCleanup(t, &databases)
	pgssCase(t, true, func(s *pgssScenario) {
		other := "pgss_cluster_peer"
		if s.b.PG {
			databases = append(databases, other)
		}
		s.exec("CREATE DATABASE " + other)
		pool, _, recorder := fixture.DatabaseScenarioPool(t, s.p, s.b.PG, other, "")
		peer, err := pool.Conn(s.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		step := func(step fixture.Step) { fixture.RunStep(t, s.p.Ctx, peer, recorder, step) }
		step(fixture.Exec("SET pg_stat_statements.track = none", 0))
		step(fixture.Exec("SET pg_stat_statements.track_utility = off", 0))
		step(fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='pg_stat_statements'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		s.exec("CREATE TABLE public.pgss_cluster_shape(k integer)")
		s.run(fixture.Exec("INSERT INTO public.pgss_cluster_shape VALUES(11)", 1))
		step(fixture.Exec("CREATE TABLE public.pgss_cluster_shape(k integer)", 0))
		step(fixture.Exec("INSERT INTO public.pgss_cluster_shape VALUES(22)", 1))
		q := "SELECT k AS pgss_cluster_value FROM public.pgss_cluster_shape"
		s.both(fixture.Select("SELECT count(*) AS n FROM pgss_part.pg_stat_statements WHERE query=$1", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, q))
		s.work(fixture.Select(q, []string{"pgss_cluster_value"}, []string{"INT4"}, [][]any{{int64(11)}}))
		step(fixture.Exec("SET pg_stat_statements.track = top", 0))
		step(fixture.Select(q, []string{"pgss_cluster_value"}, []string{"INT4"}, [][]any{{int64(22)}}))
		step(fixture.Prepared(fixture.Select(q, []string{"pgss_cluster_value"}, []string{"INT4"}, [][]any{{int64(22)}})))
		assertShared := func() {
			s.both(fixture.Select("SELECT dbid=(SELECT oid FROM pg_database WHERE datname=current_database()) AS local,toplevel,calls,rows FROM pgss_part.pg_stat_statements WHERE query=$1 ORDER BY local DESC", []string{"local", "toplevel", "calls", "rows"}, []string{"BOOL", "BOOL", "INT8", "INT8"}, [][]any{{true, true, int64(1), int64(1)}, {false, true, int64(2), int64(2)}}, q))
			s.both(fixture.Select("SELECT count(DISTINCT queryid) AS identities,count(DISTINCT dbid) AS databases,count(DISTINCT userid) AS users FROM pgss_part.pg_stat_statements WHERE query=$1", []string{"identities", "databases", "users"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(1), int64(2), int64(1)}}, q))
		}
		assertShared()
		if err := peer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := pool.Close(); err != nil {
			t.Fatal(err)
		}
		assertShared()
		if !s.b.PG {
			// Independent public Driver instances do not share the cluster or
			// installed provider, even with exactly the same opaque DSN.
			connector, err := stuntgres.NewDriver().OpenConnector(s.p.NativeDSN)
			if err != nil {
				t.Fatal(err)
			}
			isolated, record := fixture.RecordedNativePool(t, connector)
			fixture.RunStep(t, s.p.Ctx, isolated, record, fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='pg_stat_statements'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			if _, err := isolated.ExecContext(s.p.Ctx, "CREATE EXTENSION pg_stat_statements"); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, s.p.Ctx, isolated, record, fixture.Select("SELECT count(*) AS n FROM pg_stat_statements WHERE query=$1", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, q))
			assertShared()
		}
	})
}
