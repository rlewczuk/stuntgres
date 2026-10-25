package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGAggregateFeatureSmoke(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pga_smoke_addon", "pga_store")
	fixture.RoleReferenceCleanup(t, []string{"pga_smoke_reader"})
	var emitted []byte
	pgAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		for _, q := range []string{"CREATE TABLE public.pga_smoke_values(v INT)", "CREATE TABLE public.pga_smoke_sentinel(v INT)", "CREATE ROLE pga_smoke_reader", "GRANT SELECT ON public.pga_smoke_sentinel TO pga_smoke_reader"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.pga_smoke_values VALUES(1),(2),(3)", 3))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.pga_smoke_sentinel VALUES(7)", 1))
		pgAggregateBoth(t, p, b, c, fixture.Select("SELECT count(*),array_agg(v ORDER BY v),percentile_disc(0.5) WITHIN GROUP (ORDER BY v),rank(2) WITHIN GROUP (ORDER BY v) FROM public.pga_smoke_values", []string{"count", "array_agg", "percentile_disc", "rank"}, []string{"INT8", "_INT4", "INT4", "INT8"}, [][]any{{int64(3), "{1,2,3}", int64(2), int64(2)}}))
		pgAggregateBoth(t, p, b, c, pgAggregateRawStep(b, "SELECT * FROM pg_aggregate WHERE FALSE", [][]any{}))
		projection, _ := pgAggregateNumericProjection()
		var anchors [][]any
		for _, r := range pgAggregateReleased {
			switch r[0].(int64) {
			case 2803, 2335, 3972, 3986:
				anchors = append(anchors, r)
			}
		}
		s := pgAggregateDefinitionStep("SELECT "+projection+" FROM pg_aggregate WHERE aggfnoid::oid IN (2803,2335,3972,3986)", anchors)
		s.Bag = true
		pgAggregateBoth(t, p, b, c, s)
		pgAggregateInstall(t, p, b, c, "pga_smoke_addon")
		pgAggregateIntaggCheck(t, p, b, c, "pga_smoke_addon")
		pgAggregateBoth(t, p, b, c, fixture.Select("SELECT pga_smoke_addon.int_array_aggregate(v ORDER BY v) AS a FROM public.pga_smoke_values", []string{"a"}, []string{"_INT4"}, [][]any{{"{1,2,3}"}}))
		statement, err := c.PrepareContext(p.Ctx, pgAggregateProbe)
		if err != nil {
			t.Fatal(err)
		}
		defer statement.Close()
		held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: pgAggregateProbe}
		if b.PG {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("GRANT USAGE ON SCHEMA public TO pga_smoke_reader", 0))
		} else {
			// Expose the schema, not ownership of the bootstrap catalog or
			// sentinel table. Native schema SQL grants remain unsupported.
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER SCHEMA public OWNER TO pga_smoke_reader", 0))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE pga_smoke_reader", 0))
		defer c.ExecContext(context.Background(), "RESET ROLE")
		fixture.PgDescriptionExecutorRows(t, p, b, held, pgAggregateRawStep(b, pgAggregateProbe, pgAggregateCountRaw(), "2803"))
		pgAggregateIntaggCheck(t, p, b, c, "pga_smoke_addon")
		const mutation = "UPDATE pg_catalog.pg_aggregate SET aggtransspace=0 WHERE FALSE"
		pgAggregateError(t, p, b, c, mutation, "42501", "permission denied for table pg_aggregate", false)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM public.pga_smoke_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		fixture.PgDescriptionExecutorRows(t, p, b, held, pgAggregateRawStep(b, pgAggregateProbe, pgAggregateCountRaw(), "2803"))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
		if !b.PG {
			// Do not carry this visibility-only setup into the control dump.
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER SCHEMA public OWNER TO postgres", 0))
		}
		if b.PG {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(mutation, 0))
		} else {
			pgAggregateError(t, p, b, c, mutation, "0A000", "catalog source mutation is unsupported", false)
		}
		if err := statement.Close(); err != nil {
			t.Fatal(err)
		}
		// Release the smoke SQL objects before replaying the actual native stream.
		// Persistence below retains real helper data/defaults and eager catalog rows.
		for _, q := range []string{"DROP SCHEMA pga_smoke_addon CASCADE", "DROP TABLE public.pga_smoke_values", "DROP TABLE public.pga_smoke_sentinel"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		pgAggregatePersistence(t, p, b, &emitted)
	})
}
