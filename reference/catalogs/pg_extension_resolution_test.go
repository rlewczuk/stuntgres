package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func pgExtensionHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, oid int64) {
	t.Helper()
	for _, name := range []string{pgExtensionAbsent, "plpgsql", pgExtensionAbsent} {
		want := [][]any{}
		if b.PG && name == "plpgsql" {
			want = [][]any{{oid, "plpgsql", int64(10), int64(11), false, "1.0", nil, nil}}
		}
		s := fixture.Select(pgExtensionProbe, pgExtensionColumns, pgExtensionTypes, want, name)
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgExtensionMetadata(t, rows, b.PG) }
		fixture.PgDescriptionExecutorRows(t, p, b, e, s)
	}
}

func TestReferencePGExtensionResolutionAndPrepared(t *testing.T) {
	pgExtensionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		defer conn.ExecContext(p.Ctx, "RESET search_path")
		oid := pgExtensionInstalled(t, p, b)
		stmt, err := conn.PrepareContext(p.Ctx, pgExtensionProbe)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: pgExtensionProbe}
		pgExtensionHeld(t, p, b, held, oid)
		for _, spelling := range []string{"pg_catalog.pg_extension", "pg_extension"} {
			s := fixture.Select("SELECT '"+spelling+"'::regclass::oid AS id", []string{"id"}, []string{"OID"}, [][]any{{int64(3079)}})
			fixture.PgDescriptionExecutorRows(t, p, b, conn, s)
			fixture.PgDescriptionExecutorRows(t, p, b, conn, fixture.Prepared(s))
		}
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE TABLE public.pg_extension(id INT)", 0))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("INSERT INTO public.pg_extension VALUES(7)", 1))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET search_path=public,pg_catalog", 0))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT * FROM pg_extension", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		var userID int64
		if err := conn.QueryRowContext(p.Ctx, "SELECT 'public.pg_extension'::regclass::oid").Scan(&userID); err != nil {
			t.Fatal(err)
		}
		if userID <= 0 || userID == 3079 {
			t.Fatal("shadow identity", userID)
		}
		fixture.PgDescriptionExecutorRows(t, p, b, conn, fixture.Select("SELECT 'pg_extension'::regclass::oid AS id", []string{"id"}, []string{"OID"}, [][]any{{userID}}))
		pgExtensionHeld(t, p, b, held, oid)
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET search_path=pg_catalog,public", 0))
		fixture.PgDescriptionExecutorRows(t, p, b, conn, fixture.Select("SELECT 'pg_extension'::regclass::oid AS id", []string{"id"}, []string{"OID"}, [][]any{{int64(3079)}}))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE VIEW public.pgext_resolution_view AS SELECT extname,extversion FROM pg_catalog.pg_extension WHERE extname='"+pgExtensionAbsent+"'", 0))
		s := fixture.Select("SELECT * FROM public.pgext_resolution_view", []string{"extname", "extversion"}, []string{"NAME", "TEXT"}, [][]any{})
		fixture.PgDescriptionExecutorRows(t, p, b, conn, s)
		fixture.PgDescriptionExecutorRows(t, p, b, conn, fixture.Prepared(s))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("DROP VIEW public.pgext_resolution_view", 0))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("DROP TABLE public.pg_extension", 0))
		pgExtensionHeld(t, p, b, held, oid)
	})
}
