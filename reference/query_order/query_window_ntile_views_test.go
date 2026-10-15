package reference_test

import (
	"bytes"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func queryWindowNtileViews(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE VIEW nt_view AS SELECT id,ntile(b) OVER w AS n FROM nt WINDOW w AS(PARTITION BY p ORDER BY id ROWS CURRENT ROW EXCLUDE CURRENT ROW)", 0),
		fixture.Exec("CREATE TABLE nt_arg(b INT)", 0),
		fixture.Exec("INSERT INTO nt_arg VALUES(2)", 1),
		fixture.Exec("CREATE DOMAIN nt_bucket_domain AS INTEGER", 0),
		fixture.Exec("CREATE VIEW nt_child AS SELECT id,ntile((SELECT b FROM nt_arg)) OVER(ORDER BY id) AS n FROM nt", 0),
		fixture.Exec("CREATE VIEW nt_literal AS SELECT id,ntile('2'::nt_bucket_domain::int) OVER(ORDER BY id) AS n FROM nt", 0),
	} {
		queryWindowRun(t, p, b, s)
	}
	view := queryWindowNtileLabels("SELECT id,n FROM nt_view ORDER BY id", nil, int64(1), int64(1), int64(1), int64(2), int64(1), int64(2))
	queryWindowRoutes(t, p, b, view)
	d := fixture.Diagnostic{Code: "2BP01", Message: "cannot drop column b of table nt because other objects depend on it", Detail: "view nt_view depends on column b of table nt", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError("ALTER TABLE nt DROP COLUMN b RESTRICT", d)
		s.Prepared, s.ErrorStage = prepared, "execute"
		queryWindowRun(t, p, b, s)
		queryWindowRoutes(t, p, b, view)
	}
	queryWindowRun(t, p, b, fixture.Exec("ALTER TABLE nt RENAME COLUMN b TO bucket_only", 0))
	queryWindowRun(t, p, b, fixture.Exec("ALTER TABLE nt_arg RENAME COLUMN b TO scalar_only", 0))
	steps := []fixture.Step{view,
		queryWindowNtileLabels("SELECT id,n FROM nt_child ORDER BY id", int64(1), int64(1), int64(1), int64(1), int64(2), int64(2), int64(2)),
		queryWindowNtileLabels("SELECT id,n FROM nt_literal ORDER BY id", int64(1), int64(1), int64(1), int64(1), int64(2), int64(2), int64(2)),
	}
	for i, name := range []string{"nt_view", "nt_child", "nt_literal"} {
		s := steps[i]
		queryWindowRoutes(t, p, b, s)
		var definition string
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('"+name+"'::regclass,true)").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		if name == "nt_view" && !strings.Contains(definition, "bucket_only") {
			t.Fatal("renamed bucket missing from deparse", definition)
		}
		if name == "nt_child" && !strings.Contains(definition, "scalar_only") {
			t.Fatal("renamed scalar child missing from deparse", definition)
		}
		queryWindowRun(t, p, b, fixture.Exec("CREATE VIEW "+name+"_replay AS "+definition, 0))
		s.Query = "SELECT id,n FROM " + name + "_replay ORDER BY id"
		queryWindowRoutes(t, p, b, s)
		steps = append(steps, s)
	}
	if !b.PG {
		queryWindowNtileFailedRestore(t, p, steps[0])
		queryWindowValueControls(t, p, steps)
	}
}

func queryWindowNtileFailedRestore(t *testing.T, p *fixture.Pair, step fixture.Step) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	fresh := fixture.NewFixture(t, false, true)
	for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE nt_keep(v INT)", 0), fixture.Exec("INSERT INTO nt_keep VALUES(91)", 1)} {
		fixture.RunStep(t, fresh.Ctx, fresh.Memory, fresh.MemoryRecord, s)
	}
	bad := append([]byte(nil), dump.Bytes()...)
	// Restore deliberately excludes INSERT query sources. Reject this staged
	// replacement before evaluating ntile; live INSERT SELECT exercises22014.
	bad = append(bad, []byte("\nINSERT INTO nt_arg SELECT ntile(0) OVER();\n")...)
	err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(bad))
	fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "restore INSERT query sources are unsupported"})
	fixture.RunStep(t, fresh.Ctx, fresh.Memory, fresh.MemoryRecord, fixture.Select("SELECT v FROM nt_keep", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
	// Failed replacement preserved the destination catalog as well as its row.
	absent := fixture.ImplicitError("SELECT * FROM nt_view", fixture.Diagnostic{Code: "42P01", Message: `relation "nt_view" does not exist`, Position: "15"})
	absent.SelectRows, absent.ErrorStage = true, "execute"
	fixture.RunStep(t, fresh.Ctx, fresh.Memory, fresh.MemoryRecord, absent)
	if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	for _, prepared := range []bool{false, true} {
		step.Prepared = prepared
		fixture.RunStep(t, fresh.Ctx, fresh.Memory, fresh.MemoryRecord, step)
	}
}

func TestReferenceQueryWindowNtileViews(t *testing.T) {
	t.Run("persistence", func(t *testing.T) {
		queryWindowValueScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			queryWindowNtileSeed(t, p, b)
			queryWindowNtileViews(t, p, b)
		})
	})
	t.Run("bucket-authorization", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"nt_reader"})
		queryWindowValueScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) { queryWindowNtileAuthorization(t, p, b) })
	})
}

func queryWindowNtileAuthorization(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE ROLE nt_reader", 0),
		fixture.Exec("CREATE TABLE nt_secret(id INT,b INT)", 0),
		fixture.Exec("INSERT INTO nt_secret VALUES(1,2)", 1),
		fixture.Exec("GRANT SELECT(id) ON nt_secret TO nt_reader", 0),
	} {
		queryWindowRun(t, p, b, s)
	}
	if b.PG {
		queryWindowRun(t, p, b, fixture.Exec("GRANT USAGE ON SCHEMA public TO nt_reader", 0))
	}
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET SESSION AUTHORIZATION nt_reader", 0))
	q := "SELECT ntile(b) OVER(ORDER BY id) AS n FROM public.nt_secret"
	for _, suffix := range []string{"", " WHERE FALSE", " LIMIT 0"} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(q+suffix, fixture.Diagnostic{Code: "42501", Message: "permission denied for table nt_secret"})
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
		}
	}
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError("SELECT ntile((SELECT b FROM public.nt_secret)) OVER() AS n", fixture.Diagnostic{Code: "42501", Message: "permission denied for table nt_secret"})
		s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
	}
	queryWindowRun(t, p, b, fixture.Exec("GRANT SELECT(b) ON nt_secret TO nt_reader", 0))
	stmt, err := conn.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
	success := fixture.Select(q, []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}})
	fixture.RunStep(t, p.Ctx, held, b.Recorder, success)
	queryWindowRun(t, p, b, fixture.Exec("REVOKE SELECT(b) ON nt_secret FROM nt_reader", 0))
	failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table nt_secret"})
	failure.SelectRows, failure.ErrorStage = true, "execute"
	fixture.RunStep(t, p.Ctx, held, b.Recorder, failure)
	queryWindowRun(t, p, b, fixture.Exec("GRANT SELECT(b) ON nt_secret TO nt_reader", 0))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, success)
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
}
