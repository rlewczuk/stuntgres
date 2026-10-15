package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func queryWindowOffsetViews(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE wo_arg(fallback INT)", 0),
		fixture.Exec("INSERT INTO wo_arg VALUES(77)", 1),
		fixture.Exec("CREATE DOMAIN wo_literal AS INTEGER", 0),
		fixture.Exec("CREATE VIEW wo_named AS SELECT id,lead(v,100,(SELECT fallback FROM wo_arg)) OVER w AS x,lag(v,100,'88'::wo_literal::int) OVER w AS y FROM wo WINDOW base AS(PARTITION BY p ORDER BY id),w AS(base ROWS CURRENT ROW EXCLUDE CURRENT ROW)", 0),
		fixture.Exec("CREATE VIEW wo_regclass AS SELECT id,lead('live'::text,100,'wo_arg'::regclass::text) OVER(ORDER BY id) AS x FROM wo", 0),
		fixture.Exec("CREATE TYPE wo_owned AS ENUM('z','a')", 0),
		fixture.Exec("CREATE DOMAIN wo_wrapped AS wo_owned", 0),
		fixture.Exec("CREATE VIEW wo_owned_view AS SELECT id,lead(NULL::wo_wrapped,100,'z'::wo_wrapped) OVER(ORDER BY id)::text AS x FROM wo", 0),
		fixture.Exec("ALTER TABLE wo_arg RENAME COLUMN fallback TO default_only", 0),
	} {
		queryWindowRun(t, p, b, s)
	}
	var steps []fixture.Step
	for _, tc := range []struct {
		name           string
		columns, types []string
		rows           [][]any
	}{
		{"wo_named", []string{"id", "x", "y"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(77), int64(88)}, {int64(2), int64(77), int64(88)}, {int64(3), int64(77), int64(88)}, {int64(4), int64(77), int64(88)}, {int64(5), int64(77), int64(88)}, {int64(6), int64(77), int64(88)}}},
		{"wo_regclass", []string{"id", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "wo_arg"}, {int64(2), "wo_arg"}, {int64(3), "wo_arg"}, {int64(4), "wo_arg"}, {int64(5), "wo_arg"}, {int64(6), "wo_arg"}}},
		{"wo_owned_view", []string{"id", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "z"}, {int64(2), "z"}, {int64(3), "z"}, {int64(4), "z"}, {int64(5), "z"}, {int64(6), "z"}}},
	} {
		s := fixture.Select("SELECT * FROM "+tc.name+" ORDER BY id", tc.columns, tc.types, tc.rows)
		queryWindowRoutes(t, p, b, s)
		steps = append(steps, s)
		steps = append(steps, queryWindowReviewReplay(t, p, b, tc.name, s)...)
		var definition string
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('"+tc.name+"'::regclass,true)").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		queryWindowRun(t, p, b, fixture.Exec("CREATE VIEW "+tc.name+"_getdef AS "+definition, 0))
		s.Query = "SELECT * FROM " + tc.name + "_getdef ORDER BY id"
		queryWindowRoutes(t, p, b, s)
		steps = append(steps, s)
	}
	if !b.PG {
		queryWindowOffsetFailedRestore(t, p, steps[0])
		queryWindowValueControls(t, p, steps)
	}
}

func queryWindowOffsetFailedRestore(t *testing.T, p *fixture.Pair, step fixture.Step) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	fresh := fixture.NewFixture(t, false, true)
	for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE wo_keep(v INT)", 0), fixture.Exec("INSERT INTO wo_keep VALUES(91)", 1)} {
		fixture.RunStep(t, fresh.Ctx, fresh.Memory, fresh.MemoryRecord, s)
	}
	bad := append([]byte(nil), dump.Bytes()...)
	bad = append(bad, []byte("\nINSERT INTO wo_arg SELECT lead(1,0) OVER();\n")...)
	err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(bad))
	fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "restore INSERT query sources are unsupported"})
	fixture.RunStep(t, fresh.Ctx, fresh.Memory, fresh.MemoryRecord, fixture.Select("SELECT v FROM wo_keep", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
	absent := fixture.ImplicitError("SELECT * FROM wo_named", fixture.Diagnostic{Code: "42P01", Message: `relation "wo_named" does not exist`, Position: "15"})
	absent.SelectRows = true
	absent.ErrorStage = "execute"
	fixture.RunStep(t, fresh.Ctx, fresh.Memory, fresh.MemoryRecord, absent)
	if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	for _, prepared := range []bool{false, true} {
		step.Prepared = prepared
		fixture.RunStep(t, fresh.Ctx, fresh.Memory, fresh.MemoryRecord, step)
	}
}

func TestReferenceQueryWindowOffsetViews(t *testing.T) {
	t.Run("persistence", func(t *testing.T) {
		queryWindowOffsetScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			queryWindowOffsetSeed(t, p, b)
			queryWindowOffsetViews(t, p, b)
		})
	})
	t.Run("dependencies", func(t *testing.T) {
		queryWindowOffsetScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE TABLE wo_dep(id INT,hidden INT)", 0),
				fixture.Exec("CREATE VIEW wo_dep_default AS SELECT lead(1,0,hidden) OVER(ORDER BY id) FROM wo_dep", 0),
				fixture.Exec("CREATE TABLE wo_dep_scalar(v INT)", 0),
				fixture.Exec("CREATE VIEW wo_dep_subquery AS SELECT lag(1,NULL::int,(SELECT v FROM wo_dep_scalar)) OVER()", 0),
				fixture.Exec("CREATE DOMAIN wo_dep_type AS INTEGER", 0),
				fixture.Exec("CREATE VIEW wo_dep_typed AS SELECT lead(1,0,'3'::wo_dep_type::int) OVER()", 0),
			} {
				queryWindowRun(t, p, b, s)
			}
			for _, tc := range []struct{ q, message, detail string }{
				{"ALTER TABLE wo_dep DROP COLUMN hidden", "cannot drop column hidden of table wo_dep because other objects depend on it", "view wo_dep_default depends on column hidden of table wo_dep"},
				{"ALTER TABLE wo_dep_scalar DROP COLUMN v", "cannot drop column v of table wo_dep_scalar because other objects depend on it", "view wo_dep_subquery depends on column v of table wo_dep_scalar"},
				{"DROP DOMAIN wo_dep_type", "cannot drop type wo_dep_type because other objects depend on it", "view wo_dep_typed depends on type wo_dep_type"},
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "2BP01", Message: tc.message, Detail: tc.detail, Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					queryWindowRun(t, p, b, s)
				}
			}
		})
	})
	t.Run("authorization", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"wo_reader"})
		queryWindowOffsetScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) { queryWindowOffsetAuthorization(t, p, b) })
	})
}

func queryWindowOffsetAuthorization(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE ROLE wo_reader", 0), fixture.Exec("CREATE TABLE wo_secret(id INT,hidden INT)", 0), fixture.Exec("INSERT INTO wo_secret VALUES(1,77)", 1), fixture.Exec("GRANT SELECT(id) ON wo_secret TO wo_reader", 0),
	} {
		queryWindowRun(t, p, b, s)
	}
	if b.PG {
		queryWindowRun(t, p, b, fixture.Exec("GRANT USAGE ON SCHEMA public TO wo_reader", 0))
	}
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET SESSION AUTHORIZATION wo_reader", 0))
	for _, tc := range []struct {
		q     string
		value any
	}{
		{"SELECT lead(id,0,hidden) OVER(ORDER BY id) AS x FROM public.wo_secret", int64(1)},
		{"SELECT lag(id,NULL::int,hidden) OVER(ORDER BY id) AS x FROM public.wo_secret", nil},
		{"SELECT DISTINCT lead(1,0,(SELECT hidden FROM public.wo_secret)) OVER() AS x ORDER BY lead(1,0,(SELECT hidden FROM public.wo_secret)) OVER()", int64(1)},
	} {
		suffixes := []string{"", " WHERE FALSE", " LIMIT 0"}
		if tc.q[7:15] == "DISTINCT" {
			suffixes = []string{""}
		}
		for _, suffix := range suffixes {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q+suffix, fixture.Diagnostic{Code: "42501", Message: "permission denied for table wo_secret"})
				s.Prepared = prepared
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
			}
		}
		queryWindowRun(t, p, b, fixture.Exec("GRANT SELECT(hidden) ON wo_secret TO wo_reader", 0))
		stmt, err := conn.PrepareContext(p.Ctx, tc.q)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: tc.q}
			success := fixture.Select(tc.q, []string{"x"}, []string{"INT4"}, [][]any{{tc.value}})
			fixture.RunStep(t, p.Ctx, held, b.Recorder, success)
			queryWindowRun(t, p, b, fixture.Exec("REVOKE SELECT(hidden) ON wo_secret FROM wo_reader", 0))
			failure := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table wo_secret"})
			failure.SelectRows = true
			failure.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, failure)
			queryWindowRun(t, p, b, fixture.Exec("GRANT SELECT(hidden) ON wo_secret TO wo_reader", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, success)
			queryWindowRun(t, p, b, fixture.Exec("REVOKE SELECT(hidden) ON wo_secret FROM wo_reader", 0))
		}()
	}
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
}
