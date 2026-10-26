package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGTSConfigLifecycle(t *testing.T) {
	pgTSConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, q := range []string{
			"CREATE TABLE public.ptsc_control_sentinel(v INT)",
			"CREATE VIEW public.ptsc_control_view AS SELECT oid,cfgname,cfgnamespace,cfgowner,cfgparser FROM pg_catalog.pg_ts_config",
		} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO public.ptsc_control_sentinel VALUES(7)", 1))
		stmt, err := b.DB.PrepareContext(p.Ctx, pgTSConfigNameProbe)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgTSConfigNameProbe}
		catalog := func() {
			pgTSConfigInventory(t, p, b, b.DB)
			fixture.PgDescriptionExecutorRows(t, p, b, held, pgTSConfigStep(b, pgTSConfigNameProbe, pgTSConfigSimple(b), "simple"))
		}
		check := func() {
			catalog()
			q := "SELECT * FROM public.ptsc_control_view"
			if b.PG {
				q += " WHERE cfgname='simple'"
			}
			pgTSConfigBoth(t, p, b, b.DB, fixture.Select(q, fixture.PGTSConfigColumns, pgTSConfigTypes, pgTSConfigSimple(b)))
			s := fixture.Select("SELECT v FROM public.ptsc_control_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
		}
		check()
		if b.PG {
			return // Native controls have no PostgreSQL API equivalent.
		}
		old, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM pg_catalog.pg_ts_config")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		saved := pgTSConfigMetadata(t, old, false)
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		catalog()
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check()
		if !old.Next() {
			t.Fatal("retained configuration cursor lost seed row", old.Err())
		}
		var oid, namespace, owner, parser int64
		var name string
		if err := old.Scan(&oid, &name, &namespace, &owner, &parser); err != nil || oid != 3748 || name != "simple" || namespace != 11 || owner != 10 || parser != 3722 {
			t.Fatal("retained configuration seed row", oid, name, namespace, owner, parser, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("retained configuration cursor trailing rows", old.Err())
		}
		if err := old.Close(); err != nil {
			t.Fatal(err)
		}
		pgTSConfigSavedMetadata(t, saved, false)
	})
}
