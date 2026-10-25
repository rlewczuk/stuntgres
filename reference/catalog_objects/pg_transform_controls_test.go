package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGTransformLifecycle(t *testing.T) {
	pgTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, q := range []string{
			"CREATE TABLE public.ptr_control_sentinel(v INT)",
			"CREATE VIEW public.ptr_control_view AS SELECT oid,trftype,trflang,trffromsql,trftosql FROM pg_catalog.pg_transform",
		} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO public.ptr_control_sentinel VALUES(7)", 1))
		_, conn := fixture.LockConnections(t, p.Ctx, b.DB)
		stmt, err := conn.PrepareContext(p.Ctx, pgTransformPairProbe)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: pgTransformPairProbe}
		catalog := func() {
			pgTransformBoth(t, p, b, conn, pgTransformStep(b, "SELECT * FROM pg_catalog.pg_transform"))
			fixture.PgDescriptionExecutorRows(t, p, b, held, pgTransformStep(b, pgTransformPairProbe, int64(23), int64(12)))
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT t.typrelid::bigint AS typrelid,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid=3576", []string{"typrelid", "element_link", "array_link"}, []string{"INT8", "BOOL", "BOOL"}, [][]any{{int64(3576), true, true}}))
		}
		check := func() {
			catalog()
			// View expressions may have unknown nullability; their type identities
			// and complete empty result are still exact.
			pgTransformBoth(t, p, b, conn, fixture.Select("SELECT * FROM public.ptr_control_view", fixture.PGTransformColumns, pgTransformTypes, [][]any{}))
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT v FROM public.ptr_control_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
		check()
		if b.PG {
			return // Native controls have no PostgreSQL API equivalent.
		}
		old, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM pg_catalog.pg_transform")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		saved := pgTransformMetadata(t, old, false)
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
		if old.Next() || old.Err() != nil {
			t.Fatal("retained empty transform cursor", old.Err())
		}
		if err := old.Close(); err != nil {
			t.Fatal(err)
		}
		pgTransformSavedMetadata(t, saved, false)
	})
}
