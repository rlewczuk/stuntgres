package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

const strictNullNextval = "pg_catalog.nextval(regclass)"
const strictNullSample = "pg_catalog.array_sample(anyarray,integer)"

func strictNullValue(q string, value any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"v"}, []string{"_INT4"}, [][]any{{value}}, args...)
}

func strictNullFailure(q, code, message string, args ...any) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message}, args...)
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}

func strictNullSetup(run func(fixture.Step)) {
	run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
	run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.currval(regclass),pg_catalog.lastval() TO og_object_reader", 0))
	run(fixture.Exec("CREATE SEQUENCE og_strict_control START 41 CACHE 1", 0))
	run(fixture.Exec("GRANT USAGE,SELECT ON SEQUENCE og_strict_control TO og_object_reader", 0))
	fixture.RoleGrantActor(run, "og_object_reader", func() {
		run(fixture.Select("SELECT nextval('og_strict_control') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(41)}}))
	})
}

func strictNullSequence(run func(fixture.Step), name string) {
	run(fixture.Exec("CREATE SEQUENCE "+name+" START 1 CACHE 1", 0))
	run(fixture.Exec("GRANT USAGE,SELECT ON SEQUENCE "+name+" TO og_object_reader", 0))
}

type strictNullSequenceState struct {
	last, log int64
	called    bool
}

// Inspect all physical fields with typed Scan, then independently assert metadata
// and the exact snapshot. PostgreSQL's WAL lookahead is physical, not logical.
func strictNullState(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), name string, last int64, called bool) strictNullSequenceState {
	t.Helper()
	var state strictNullSequenceState
	q := "SELECT last_value,log_cnt,is_called FROM " + name
	if err := c.QueryRowContext(p.Ctx, q).Scan(&state.last, &state.log, &state.called); err != nil {
		t.Fatal(err)
	}
	if state.last != last || state.called != called || state.log < 0 || state.log > 32 || (!called || !b.PG) && state.log != 0 {
		t.Fatalf("%s state %#v; want last=%d called=%v, native/unused log=0 or PostgreSQL used log in [0,32]", name, state, last, called)
	}
	run(fixture.Select(q, []string{"last_value", "log_cnt", "is_called"}, []string{"INT8", "INT8", "BOOL"}, [][]any{{state.last, state.log, state.called}}))
	return state
}

func strictNullUnchanged(run func(fixture.Step), name string, state strictNullSequenceState) {
	run(fixture.Select("SELECT last_value,log_cnt,is_called FROM "+name, []string{"last_value", "log_cnt", "is_called"}, []string{"INT8", "INT8", "BOOL"}, [][]any{{state.last, state.log, state.called}}))
}

func strictNullSession(run func(fixture.Step), name string, called bool, last int64) {
	if called {
		run(fixture.Select("SELECT currval('"+name+"') AS v", []string{"v"}, []string{"INT8"}, [][]any{{last}}))
	} else {
		run(strictNullFailure("SELECT currval('"+name+"') AS v", "55000", fmt.Sprintf("currval of sequence %q is not yet defined in this session", name)))
	}
	run(fixture.Select("SELECT currval('og_strict_control') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(41)}}))
	run(fixture.Select("SELECT lastval() AS v", []string{"v"}, []string{"INT8"}, [][]any{{last}}))
}

func TestReferenceObjectGrantStrictNullDemand(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer objectGrantRoutineCleanup(t, p, c, []string{strictNullNextval, strictNullSample, "pg_catalog.currval(regclass)", "pg_catalog.lastval()"})
		strictNullSetup(run)
		run(fixture.Exec("CREATE TABLE og_strict_rows(id INT,a INT[])", 0))
		run(fixture.Exec("INSERT INTO og_strict_rows VALUES(1,NULL),(2,'{7}')", 2))
		run(fixture.Exec("GRANT SELECT ON og_strict_rows TO og_object_reader", 0))
		for i, phase := range []string{"allowed", "child_denied", "both_denied"} {
			name := fmt.Sprintf("og_strict_literal_%d", i)
			strictNullSequence(run, name)
			if i == 1 {
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+strictNullNextval+" FROM PUBLIC,og_object_reader", 0))
			}
			if i == 2 {
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+strictNullSample+" FROM PUBLIC,og_object_reader", 0))
			}
			t.Logf("literal demand phase %s", phase)
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				before := strictNullState(t, p, b, c, run, name, 1, false)
				strictNullSession(run, name, false, 41)
				run(strictNullValue("SELECT array_sample(NULL::integer[],nextval('"+name+"')::integer) AS v", nil))
				run(strictNullValue("SELECT array_sample(ARRAY[nextval('"+name+"')::integer],NULL::integer) AS v", nil))
				strictNullUnchanged(run, name, before)
				strictNullSession(run, name, false, 41)
			})
		}
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+strictNullNextval+","+strictNullSample+" TO PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			for i := range 3 {
				name := fmt.Sprintf("og_strict_literal_%d", i)
				run(fixture.Select("SELECT nextval('"+name+"') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
				strictNullState(t, p, b, c, run, name, 1, true)
				strictNullSession(run, name, true, 1)
			}
		})
		for i, expr := range []string{"array_sample(a,nextval('%s')::integer) FROM og_strict_rows WHERE id=1", "array_sample(ARRAY[7],nextval('%s')::integer)"} {
			name := fmt.Sprintf("og_strict_live_%d", i)
			strictNullSequence(run, name)
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				var value any
				if i == 1 {
					value = "{7}"
				}
				// Keep the alias before FROM, not after the predicate.
				q := "SELECT " + fmt.Sprintf(expr, name)
				if i == 0 {
					q = "SELECT array_sample(a,nextval('" + name + "')::integer) AS v FROM og_strict_rows WHERE id=1"
				} else {
					q += " AS v"
				}
				run(strictNullValue(q, value))
				strictNullState(t, p, b, c, run, name, 1, true)
				strictNullSession(run, name, true, 1)
			})
		}
		for i, target := range []string{strictNullNextval, strictNullSample} {
			name := fmt.Sprintf("og_strict_denied_%d", i)
			strictNullSequence(run, name)
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC,og_object_reader", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				before := strictNullState(t, p, b, c, run, name, 1, false)
				fn := "nextval"
				if i == 1 {
					fn = "array_sample"
				}
				run(strictNullFailure("SELECT array_sample(a,nextval('"+name+"')::integer) AS v FROM og_strict_rows WHERE id=1", "42501", "permission denied for function "+fn))
				if i == 1 {
					run(strictNullFailure("SELECT array_sample(ARRAY[7],0) AS v", "42501", "permission denied for function array_sample"))
				}
				strictNullUnchanged(run, name, before)
				strictNullSession(run, name, false, 1)
			})
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO PUBLIC", 0))
		}
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(strictNullValue("SELECT array_sample(ARRAY[7],0) AS v", "{}"))
		})
		// A distinct, untouched physical session has no lastval at all.
		fresh, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fresh.Close()
		untouched := func(s fixture.Step) {
			s.Prepared = prepared
			objectGrantStep(t, p.Ctx, fresh, b.Recorder, s)
		}
		untouched(fixture.Exec("SET SESSION AUTHORIZATION og_object_reader", 0))
		untouched(strictNullFailure("SELECT lastval() AS v", "55000", "lastval is not yet defined in this session"))
		witnessState := strictNullState(t, p, b, fresh, untouched, "og_strict_literal_0", 1, true)
		untouched(strictNullFailure("SELECT currval('og_strict_literal_0') AS v", "55000", `currval of sequence "og_strict_literal_0" is not yet defined in this session`))
		untouched(strictNullValue("SELECT array_sample(NULL::integer[],nextval('og_strict_literal_0')::integer) AS v", nil))
		untouched(strictNullFailure("SELECT lastval() AS v", "55000", "lastval is not yet defined in this session"))
		strictNullUnchanged(untouched, "og_strict_literal_0", witnessState)
		untouched(strictNullFailure("SELECT currval('og_strict_literal_0') AS v", "55000", `currval of sequence "og_strict_literal_0" is not yet defined in this session`))
		untouched(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferenceObjectGrantStrictNullPrecedence(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer objectGrantRoutineCleanup(t, p, c, []string{strictNullSample, "pg_catalog.abs(integer)", "pg_catalog.currval(regclass)", "pg_catalog.lastval()"})
		strictNullSetup(run)
		strictNullSequence(run, "og_strict_precedence")
		run(fixture.Exec("CREATE TABLE og_strict_sink(v INT[])", 0))
		run(fixture.Exec("GRANT SELECT,INSERT ON og_strict_sink TO og_object_reader", 0))
		for _, denyOuter := range []bool{false, true} {
			if denyOuter {
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+strictNullSample+" FROM PUBLIC,og_object_reader", 0))
			}
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				run(strictNullFailure("SELECT array_sample(NULL::integer[],1/0) AS v", "22012", "division by zero"))
				s := strictNullFailure("INSERT INTO og_strict_sink SELECT array_sample(NULL::integer[],1/0)", "22012", "division by zero")
				s.SelectRows = false
				run(s)
				run(fixture.Select("SELECT count(*) AS n FROM og_strict_sink", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			})
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM PUBLIC,og_object_reader", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				run(strictNullFailure("SELECT array_sample(NULL::integer[],abs(-1)) AS v", "42501", "permission denied for function abs"))
			})
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO PUBLIC", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				run(strictNullValue("SELECT array_sample(NULL::integer[],abs(-1)) AS v", nil))
				run(strictNullValue("SELECT array_sample(NULL::integer[],nextval('og_strict_precedence')::integer) AS v", nil))
				strictNullState(t, p, b, c, run, "og_strict_precedence", 1, false)
				strictNullSession(run, "og_strict_precedence", false, 41)
			})
		}
	})
}
