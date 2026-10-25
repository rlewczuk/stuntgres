package reference_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func pgAggregateDump(t *testing.T, p *fixture.Pair) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 {
		t.Fatal("empty aggregate Dump")
	}
	return bytes.Clone(out.Bytes())
}
func pgAggregateBuiltinCount(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	q := "SELECT count(*) FROM pg_aggregate"
	if b.PG {
		q += " WHERE aggfnoid::oid IN (" + pgAggregateAllowlist() + ")"
	}
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select(q, []string{"count"}, []string{"INT8"}, [][]any{{int64(144)}}))
}
func pgAggregateControlFailures(t *testing.T, p *fixture.Pair, e fixture.Executor, schema string, dump []byte, check func()) {
	t.Helper()
	originalIDs := pgAggregateIntaggIDs(t, p, e, schema)
	unchanged := func() {
		check()
		if current := pgAggregateIntaggIDs(t, p, e, schema); current != originalIDs {
			t.Fatal("failed/canceled controls replaced aggregate/helper identities", originalIDs, current)
		}
	}
	for _, bad := range []string{string(dump) + " SELECT missing_pga_restore_column;", "CREATE TABLE pga_bad_restore(x regproc); INSERT INTO pga_bad_restore VALUES('pga_missing_routine');"} {
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
			t.Fatal("invalid Restore succeeded")
		}
		unchanged()
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump)); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled Restore", err)
	}
	if err := stuntgres.Reset(ctx, p.Native); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled Reset", err)
	}
	unchanged()
}
func TestReferencePGAggregateLifecycle(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pga_lifecycle_addon")
	var emitted []byte
	pgAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		pgAggregateInstall(t, p, b, c, "pga_lifecycle_addon")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE public.pga_lifecycle_sentinel(v INT)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.pga_lifecycle_sentinel VALUES(7)", 1))
		ids := pgAggregateIntaggCheck(t, p, b, c, "pga_lifecycle_addon")
		check := func() {
			pgAggregateIntaggCheck(t, p, b, c, "pga_lifecycle_addon")
			pgAggregateBoth(t, p, b, c, fixture.Select("SELECT v FROM public.pga_lifecycle_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("PostgreSQL replay requires the actual nonempty native Dump")
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP SCHEMA pga_lifecycle_addon CASCADE", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP TABLE public.pga_lifecycle_sentinel", 0))
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual native Dump PostgreSQL replay", err)
			}
			check()
			pgAggregateBuiltinCount(t, p, b, c)
			return
		}
		old, err := b.DB.QueryContext(p.Ctx, pgAggregateProbe, fmt.Sprint(ids[0]))
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		pgAggregateMetadata(t, old, false)
		meta, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		stmt, err := b.DB.PrepareContext(p.Ctx, pgAggregateProbe)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgAggregateProbe}
		emitted = pgAggregateDump(t, p)
		pgAggregateControlFailures(t, p, c, "pga_lifecycle_addon", emitted, check)
		// Real admitted owner removal must retire metadata-only aliases as well.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP SCHEMA pga_lifecycle_addon CASCADE", 0))
		fixture.PgDescriptionExecutorRows(t, p, b, held, pgAggregateRawStep(b, pgAggregateProbe, [][]any{}, fmt.Sprint(ids[0])))
		pgAggregateBuiltinCount(t, p, b, c)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		pgAggregateBuiltinCount(t, p, b, c)
		fixture.PgDescriptionExecutorRows(t, p, b, held, pgAggregateRawStep(b, pgAggregateProbe, [][]any{}, fmt.Sprint(ids[0])))
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		current := pgAggregateIntaggCheck(t, p, b, c, "pga_lifecycle_addon")
		check()
		want := [][]any{{"pga_lifecycle_addon.int_array_aggregate", "n", int64(0), "pga_lifecycle_addon.int_agg_state", "pga_lifecycle_addon.int_agg_final_array", "-", "-", "-", "-", "-", "-", false, false, "r", "r", int64(0), int64(2281), int64(0), int64(0), int64(0), nil, nil}}
		fixture.PgDescriptionExecutorRows(t, p, b, held, pgAggregateRawStep(b, pgAggregateProbe, want, fmt.Sprint(current[0])))
		pgAggregateRetained(t, old, want, pgAggregateTypes)
		pgAggregateSavedMetadata(t, meta, false)
		redump := pgAggregateDump(t, p)
		driver := stuntgres.NewDriver()
		attached := fixture.CatalogControlPool(t, driver, "pga_lifecycle_shared")
		same := fixture.CatalogControlPool(t, driver, "pga_lifecycle_shared")
		isolated := fixture.CatalogControlPool(t, stuntgres.NewDriver(), "pga_lifecycle_shared")
		if err := stuntgres.Restore(p.Ctx, attached, bytes.NewReader(redump)); err != nil {
			t.Fatal(err)
		}
		// A shared opaque DSN attaches to one owner only within its Driver.
		rows, err := same.QueryContext(p.Ctx, "SELECT v FROM public.pga_lifecycle_sentinel")
		if err != nil {
			t.Fatal(err)
		}
		pgAggregateRetained(t, rows, [][]any{{int64(7)}}, []string{"INT4"})
		receivingIDs := pgAggregateIntaggIDs(t, p, same, "pga_lifecycle_addon")
		rows, err = same.QueryContext(p.Ctx, "SELECT aggfnoid,aggtransfn,aggfinalfn FROM pg_aggregate WHERE aggfnoid::oid=$1::oid", receivingIDs[0])
		if err != nil {
			t.Fatal(err)
		}
		pgAggregateRetained(t, rows, [][]any{{"pga_lifecycle_addon.int_array_aggregate", "pga_lifecycle_addon.int_agg_state", "pga_lifecycle_addon.int_agg_final_array"}}, []string{"REGPROC", "REGPROC", "REGPROC"})
		rows, err = isolated.QueryContext(p.Ctx, "SELECT count(*) FROM pg_aggregate")
		if err != nil {
			t.Fatal(err)
		}
		pgAggregateRetained(t, rows, [][]any{{int64(144)}}, []string{"INT8"})
	})
}
