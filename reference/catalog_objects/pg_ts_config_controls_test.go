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
		seedRows := func() [][]any {
			var english int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='english' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&english); err != nil || english <= 0 || english == 3748 {
				t.Fatal("actual English configuration identity", english, err)
			}
			var arabic int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='arabic' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&arabic); err != nil || arabic <= 0 || arabic == 3748 || arabic == english {
				t.Fatal("actual distinct Arabic configuration identity", arabic, err)
			}
			var armenian int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='armenian' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&armenian); err != nil || armenian <= 0 || armenian == 3748 || armenian == english || armenian == arabic {
				t.Fatal("actual distinct Armenian configuration identity", armenian, err)
			}
			var basque int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='basque' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&basque); err != nil || basque <= 0 || basque == 3748 || basque == english || basque == arabic || basque == armenian {
				t.Fatal("actual distinct Basque configuration identity", basque, err)
			}
			var danish int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='danish' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&danish); err != nil || danish <= 0 || danish == 3748 || danish == english || danish == arabic || danish == armenian || danish == basque {
				t.Fatal("actual distinct Danish configuration identity", danish, err)
			}
			var dutch int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='dutch' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&dutch); err != nil || dutch <= 0 || dutch == 3748 || dutch == english || dutch == arabic || dutch == armenian || dutch == basque || dutch == danish {
				t.Fatal("actual distinct Dutch configuration identity", dutch, err)
			}
			var estonian int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='estonian' AND cfgnamespace='pg_catalog'::regnamespace").Scan(&estonian); err != nil || estonian <= 0 || estonian == 3748 || estonian == english || estonian == arabic || estonian == armenian || estonian == basque || estonian == danish || estonian == dutch {
				t.Fatal("actual distinct Estonian configuration identity", estonian, err)
			}
			return append([][]any{{arabic, "arabic", int64(11), int64(10), int64(3722)}, {armenian, "armenian", int64(11), int64(10), int64(3722)}, {basque, "basque", int64(11), int64(10), int64(3722)}, {danish, "danish", int64(11), int64(10), int64(3722)}, {dutch, "dutch", int64(11), int64(10), int64(3722)}, {english, "english", int64(11), int64(10), int64(3722)}, {estonian, "estonian", int64(11), int64(10), int64(3722)}}, pgTSConfigSimple(b)...)
		}
		catalog := func() {
			pgTSConfigInventory(t, p, b, b.DB)
			fixture.PgDescriptionExecutorRows(t, p, b, held, pgTSConfigStep(b, pgTSConfigNameProbe, pgTSConfigSimple(b), "simple"))
		}
		check := func() {
			catalog()
			q := "SELECT * FROM public.ptsc_control_view WHERE cfgname IN ('arabic','armenian','basque','danish','dutch','english','estonian','simple') ORDER BY cfgname"
			pgTSConfigBoth(t, p, b, b.DB, fixture.Select(q, fixture.PGTSConfigColumns, pgTSConfigTypes, seedRows()))
			s := fixture.Select("SELECT v FROM public.ptsc_control_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
		}
		check()
		if b.PG {
			return // Native controls have no PostgreSQL API equivalent.
		}
		retainedRows := seedRows()
		old, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM pg_catalog.pg_ts_config ORDER BY cfgname")
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
		for _, expected := range retainedRows {
			if !old.Next() {
				t.Fatal("retained configuration cursor lost seed row", old.Err())
			}
			var oid, namespace, owner, parser int64
			var name string
			if err := old.Scan(&oid, &name, &namespace, &owner, &parser); err != nil || oid != expected[0] || name != expected[1] || namespace != expected[2] || owner != expected[3] || parser != expected[4] {
				t.Fatal("retained configuration seed row", oid, name, namespace, owner, parser, err)
			}
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
