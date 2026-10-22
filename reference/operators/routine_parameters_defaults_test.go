package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineParametersDefaultEvaluation(t *testing.T) {
	t.Run("default lifetime", func(t *testing.T) { parametersScenario(t, parametersDefaultEvaluation) })
	t.Run("written actuals exactly once", func(t *testing.T) {
		parametersScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE SEQUENCE rp_once_seq START 1",
				"CREATE FUNCTION rp_once(a bigint,b bigint DEFAULT nextval('rp_once_seq')) RETURNS bigint LANGUAGE SQL AS 'SELECT $1+$2'")
			q := "SELECT rp_once(b=>nextval('rp_once_seq'),a=>nextval('rp_once_seq')) AS v"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT8"}, [][]any{{int64(3)}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT nextval('rp_once_seq') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(3)}}))
			parametersHeld(t, p, b, c, q, "INT8", [][]any{{}, {}}, []any{int64(9), int64(13)})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT nextval('rp_once_seq') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(8)}}))
			// Sum avoids imposing a portable order between distinct operands.
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Select("SELECT rp_once(a=>nextval('rp_once_seq')) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(19)}})))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT nextval('rp_once_seq') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(11)}}))
		})
	})
}

func parametersDefaultEvaluation(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	userOperatorExec(t, p, b, c,
		"CREATE SEQUENCE rp_seq START 1",
		"CREATE FUNCTION rp_volatile() RETURNS bigint LANGUAGE SQL AS $$SELECT nextval('rp_seq')$$",
		"CREATE FUNCTION rp_tick(a bigint DEFAULT rp_volatile()) RETURNS bigint LANGUAGE SQL AS 'SELECT $1'",
		"CREATE FUNCTION rp_loser(a int,b bigint DEFAULT nextval('rp_seq')) RETURNS bigint LANGUAGE SQL AS 'SELECT $2'",
		"CREATE FUNCTION rp_loser(a int,b text DEFAULT 'unused') RETURNS bigint LANGUAGE SQL AS 'SELECT 0'",
		"CREATE FUNCTION rp_winner(a bigint,b int DEFAULT 1/0) RETURNS int LANGUAGE SQL AS 'SELECT $2'",
		"CREATE FUNCTION rp_winner(a int) RETURNS int LANGUAGE SQL AS 'SELECT $1'")
	// A held zero-actual statement never exposes a phantom bind parameter.
	// First result1 proves neither CREATE nor Prepare evaluated the recipe.
	q := "SELECT rp_tick() AS v"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for _, n := range []int64{1, 2} {
		s := fixture.Select(q, []string{"v"}, []string{"INT8"}, [][]any{{n}})
		s.CheckMetadata = parametersMetadata(b, "v", "INT8")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	parametersValue(t, p, b, c, "SELECT rp_tick(42) AS v", "v", "INT8", int64(42))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT8"}, [][]any{{int64(3)}}))
	parametersValue(t, p, b, c, "SELECT rp_winner(7) AS v", "v", "INT4", int64(7))
	parametersError(t, p, c, "SELECT rp_loser(1)", true, "42725", "function rp_loser(integer) is not unique", parametersAmbiguousHint, "8")
	// Both failed direct/Prepare probes must leave the losing nextval untouched.
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT nextval('rp_seq') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(4)}}))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT8"}, [][]any{{int64(5)}}))
	// Fresh prepared execution also evaluates exactly once, not at Prepare.
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Select(q, []string{"v"}, []string{"INT8"}, [][]any{{int64(6)}})))
	parametersHeld(t, p, b, c, "SELECT rp_tick(a=>$1::bigint) AS v", "INT8", [][]any{{42}, {nil}, {43}}, []any{int64(42), nil, int64(43)})
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT nextval('rp_seq') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(7)}}))
}

func TestReferenceRoutineParametersDefaultIdentity(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "rp_default")
	parametersScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA rp_default",
			"CREATE FUNCTION rp_default.helper(bigint) RETURNS bigint LANGUAGE SQL AS 'SELECT $1+1000'",
			"CREATE FUNCTION rp_default.outer_call(a bigint DEFAULT rp_default.helper(1)) RETURNS bigint LANGUAGE SQL AS 'SELECT $1'",
			"CREATE FUNCTION rp_default.helper(integer) RETURNS bigint LANGUAGE SQL AS 'SELECT $1+2000'",
			"CREATE FUNCTION rp_default.assignment(a integer DEFAULT 1::bigint) RETURNS integer LANGUAGE SQL AS 'SELECT $1'",
			"CREATE DOMAIN rp_default.positive AS integer CHECK(VALUE>0)",
			"CREATE FUNCTION rp_default.domain_arg(a rp_default.positive DEFAULT 3) RETURNS integer LANGUAGE SQL AS 'SELECT $1::integer'")
		check := func(conn *sql.Conn, backend fixture.Backend) {
			parametersValue(t, p, backend, conn, "SELECT rp_default.outer_call() AS v", "v", "INT8", int64(1001))
			parametersValue(t, p, backend, conn, "SELECT rp_default.helper(1) AS v", "v", "INT8", int64(2001))
			parametersValue(t, p, backend, conn, "SELECT rp_default.assignment() AS v", "v", "INT4", int64(1))
			parametersValue(t, p, backend, conn, "SELECT rp_default.domain_arg() AS v", "v", "INT4", int64(3))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select(
				"SELECT refobjid='rp_default.helper(bigint)'::regprocedure::oid AS selected,deptype FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='rp_default.outer_call(bigint)'::regprocedure AND refclassid='pg_proc'::regclass",
				[]string{"selected", "deptype"}, []string{"BOOL", "CHAR"}, [][]any{{true, "n"}}))
			// Complete dependency diagnostic observed on pinned18.6 in tests-3.md.
			want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop function rp_default.helper(bigint) because other objects depend on it")
			want.Detail = "function rp_default.outer_call(bigint) depends on function rp_default.helper(bigint)"
			want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			fixture.EnumReferenceErrorChecked(t, p, conn, "DROP FUNCTION rp_default.helper(bigint)", false, "execute", func(t *testing.T, got fixture.Diagnostic) {
				if got != want {
					t.Errorf("parameter default dependency: got %#v want %#v", got, want)
				}
			})
		}
		parametersReplay(t, p, b, c, "rp_default", check)
	})
}

func TestReferenceRoutineParametersDefaultDeclarationVisibility(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "rp_visibility")
	parametersScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c, "CREATE SCHEMA rp_visibility")
		t.Run("existing overload", func(t *testing.T) {
			userOperatorExec(t, p, b, c,
				"CREATE FUNCTION rp_visibility.rp_self(bigint) RETURNS integer LANGUAGE SQL AS 'SELECT 1000'",
				"CREATE FUNCTION rp_visibility.rp_self(a integer DEFAULT rp_visibility.rp_self(1)) RETURNS integer LANGUAGE SQL AS 'SELECT $1'",
				"CREATE VIEW rp_visibility.captured AS SELECT rp_visibility.rp_self() AS v",
				"CREATE FUNCTION rp_visibility.frozen() RETURNS integer RETURN rp_visibility.rp_self()")
			check := func(conn *sql.Conn, backend fixture.Backend) {
				// Defaults bind before the new exact overload is published.
				parametersValue(t, p, backend, conn, "SELECT rp_visibility.rp_self() AS v", "v", "INT4", int64(1000))
				parametersValue(t, p, backend, conn, "SELECT rp_visibility.rp_self(1) AS v", "v", "INT4", int64(1))
				parametersValue(t, p, backend, conn, "SELECT rp_visibility.rp_self(42) AS v", "v", "INT4", int64(42))
				parametersHeld(t, p, backend, conn, "SELECT rp_visibility.rp_self() AS v", "INT4", [][]any{nil, nil}, []any{int64(1000), int64(1000)})
				parametersValue(t, p, backend, conn, "SELECT rp_visibility.frozen() AS v", "v", "INT4", int64(1000))
				userOperatorValue(t, p, backend, conn, "SELECT v FROM rp_visibility.captured", "INT4", int64(1000))
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select(
					"SELECT refobjid='rp_visibility.rp_self(bigint)'::regprocedure::oid AS selected,deptype FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='rp_visibility.rp_self(integer)'::regprocedure AND refclassid='pg_proc'::regclass",
					[]string{"selected", "deptype"}, []string{"BOOL", "CHAR"}, [][]any{{true, "n"}}))
			}
			parametersReplay(t, p, b, c, "rp_visibility", check)
		})
		t.Run("absent first declaration", func(t *testing.T) {
			// Full diagnostic and direct/prepared execution stages observed
			// in tests-47; Position56 identifies the default call.
			parametersError(t, p, c,
				"CREATE FUNCTION rp_visibility.absent(a integer DEFAULT rp_visibility.absent(1)) RETURNS integer LANGUAGE SQL AS 'SELECT $1'",
				false, "42883", "function rp_visibility.absent(integer) does not exist", parametersNoMatchHint, "56")
			userOperatorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='rp_visibility'::regnamespace AND proname='absent'", "INT8", int64(0))
			userOperatorExec(t, p, b, c, "CREATE FUNCTION rp_visibility.absent(a integer DEFAULT 7) RETURNS integer LANGUAGE SQL AS 'SELECT $1'")
			parametersValue(t, p, b, c, "SELECT rp_visibility.absent() AS v", "v", "INT4", int64(7))
			userOperatorExec(t, p, b, c, "DROP FUNCTION rp_visibility.absent(integer)")
		})
	})
}
