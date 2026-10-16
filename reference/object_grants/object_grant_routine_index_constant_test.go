package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"net/url"
	"strings"
	"testing"

	"github.com/lib/pq"
	"github.com/ngrok/sqlmw"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Unlike the variable-key witnesses, these recipes can disappear completely
// from a permanently cooked executable tree. RelationGetIndexExpressions and
// RelationGetIndexPredicate in REL_18_0 relcache.c reconstruct and simplify the
// original recipes; index.c FormIndexDatum initializes the demanded key program.
func TestReferenceObjectGrantRoutineIndexConstant(t *testing.T) {
	objectGrantScenario(t, []string{"og_index_constant"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.json_array_length(json)"})
		run(fixture.Exec("CREATE SCHEMA og_index_constant", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA og_index_constant TO og_object_reader", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO og_object_reader", 0))
		for _, name := range []string{"key_t", "predicate_t", "null_t", "dead_t", "false_t"} {
			run(fixture.Exec("CREATE TABLE og_index_constant."+name+"(n INT)", 0))
			run(fixture.Exec("GRANT SELECT,INSERT ON og_index_constant."+name+" TO og_object_reader", 0))
		}
		run(fixture.Exec("CREATE INDEX og_constant_key ON og_index_constant.key_t(json_array_length('[1,2]'::json))", 0))
		run(fixture.Exec("CREATE INDEX og_constant_predicate ON og_index_constant.predicate_t(n) WHERE json_array_length('[1,2]'::json)>0", 0))
		run(fixture.Exec("CREATE INDEX og_constant_null ON og_index_constant.null_t(json_array_length(NULL::json))", 0))
		run(fixture.Exec("CREATE INDEX og_constant_dead ON og_index_constant.dead_t((CASE WHEN FALSE THEN json_array_length('[1,2]'::json) ELSE 7 END))", 0))
		run(fixture.Exec("CREATE INDEX og_constant_false ON og_index_constant.false_t(json_array_length('[1,2]'::json)) WHERE FALSE", 0))
		run(fixture.Exec("ALTER ROLE og_object_reader LOGIN", 0))
		if b.PG {
			result, err := fixture.Admin.ExecContext(p.Ctx, "ALTER ROLE og_object_reader PASSWORD 'role-core-fixture'")
			fixture.DeleteReferenceCount(t, result, err, 0)
		}
		dsn := "reference/" + strings.TrimSuffix(t.Name(), "/"+b.Name)
		// Match DatabaseAdmission's recordedConnector, not a borrower from an
		// administrative pool. Every OpenDB below owns a new physical backend.
		recordedConnector := func() driver.Connector {
			var base driver.Connector
			var err error
			if b.PG {
				u, parseErr := url.Parse(fixture.DSN)
				if parseErr != nil {
					t.Fatal(parseErr)
				}
				u.User = url.UserPassword("og_object_reader", "role-core-fixture")
				base, err = pq.NewConnector(u.String())
			} else {
				base, err = p.NativeConnector.Driver().(*stuntgres.Driver).OpenConnectorAs(dsn, "og_object_reader")
			}
			if err != nil {
				t.Fatal(err)
			}
			wrapped := sqlmw.Driver(fixture.NoticeReferenceDriver{base}, fixture.Interceptor{})
			configured, err := wrapped.(driver.DriverContext).OpenConnector("")
			if err != nil {
				t.Fatal(err)
			}
			return configured
		}
		pool := sql.OpenDB(recordedConnector())
		defer pool.Close()
		warm, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer warm.Close()
		record := &fixture.Recorder{RawBackend: b.Recorder.RawBackend}
		fixture.RunStep(t, p.Ctx, warm, record, fixture.Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{"og_object_reader", "og_object_reader"}}))
		fresh := func(step fixture.Step) {
			t.Helper()
			freshPool := sql.OpenDB(recordedConnector())
			defer freshPool.Close()
			conn, err := freshPool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			step.Prepared = prepared
			fixture.RunStep(t, p.Ctx, conn, &fixture.Recorder{RawBackend: b.Recorder.RawBackend}, step)
		}
		denied := func(query string, args ...any) fixture.Step {
			step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function json_array_length"}, args...)
			step.SelectRows, step.ErrorStage = true, "execute"
			return step
		}
		var held []fixture.HeldStatement
		for _, name := range []string{"key_t", "predicate_t"} {
			query := "INSERT INTO og_index_constant." + name + " VALUES($1) RETURNING n"
			stmt, err := warm.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			h := fixture.HeldStatement{Executor: warm, Statement: stmt, SQL: query}
			held = append(held, h)
			// Actually execute, not merely Prepare: the warmed physical backend
			// has demanded and simplified this exact index recipe while allowed.
			fixture.RunStep(t, p.Ctx, h, record, fixture.Select(query, []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}, int64(1)))
		}
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM PUBLIC", 0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM og_object_reader", 0))
		for phase, id := range []int64{2, 4} {
			if phase == 1 {
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM og_object_reader", 0))
			}
			for _, h := range held {
				// Fresh backend authorization is not inferred from PostgreSQL's
				// previously cooked relcache or held executable program.
				fresh(denied(h.SQL, id))
				// Resolve warmed keys and predicates independently: PostgreSQL
				// can retain a cooked key while rebuilding its predicate.
				warmStep := denied(h.SQL, id)
				if b.PG && strings.Contains(h.SQL, ".key_t ") {
					warmStep = fixture.Select(h.SQL, []string{"n"}, []string{"INT4"}, [][]any{{id}}, id)
				}
				if !t.Run("warm/"+h.SQL, func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, h, record, warmStep)
				}) {
					t.FailNow()
				}
			}
			for _, name := range []string{"key_t", "predicate_t"} {
				rows := [][]any{{int64(1)}}
				if phase == 1 {
					rows = append(rows, []any{int64(3)}, []any{int64(13)})
				}
				if b.PG && name == "key_t" {
					rows = [][]any{{int64(1)}, {int64(2)}}
					if phase == 1 {
						rows = append(rows, []any{int64(3)}, []any{int64(4)}, []any{int64(13)})
					}
				}
				// Read as administrator: this does not demand index maintenance.
				run(fixture.Select("SELECT n FROM og_index_constant."+name+" ORDER BY n", []string{"n"}, []string{"INT4"}, rows))
				fresh(fixture.Exec("INSERT INTO og_index_constant."+name+" SELECT 90 WHERE FALSE", 0))
				constant := fixture.ImplicitError("INSERT INTO og_index_constant."+name+" VALUES(91) RETURNING 1/0", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				constant.SelectRows, constant.ErrorStage = true, "execute"
				// Constant RETURNING planning is upstream of demanded indexes.
				fresh(constant)
				fresh(denied("INSERT INTO og_index_constant." + name + " VALUES(92) RETURNING n/(n-n)"))
				// None of the zero-row or RETURNING failures published a row.
				run(fixture.Select("SELECT n FROM og_index_constant."+name+" ORDER BY n", []string{"n"}, []string{"INT4"}, rows))
			}
			for _, name := range []string{"null_t", "dead_t", "false_t"} {
				if name == "false_t" {
					// Constant key planning precedes predicate evaluation; unlike
					// a nonconstant unused key, this non-NULL call is demanded.
					fresh(denied("INSERT INTO og_index_constant."+name+" VALUES($1) RETURNING n", id))
					run(fixture.Select("SELECT n FROM og_index_constant."+name+" ORDER BY n", []string{"n"}, []string{"INT4"}, nil))
					continue
				}
				fresh(fixture.Select("INSERT INTO og_index_constant."+name+" VALUES($1) RETURNING n", []string{"n"}, []string{"INT4"}, [][]any{{id}}, id))
				rows := [][]any{{int64(2)}}
				if phase == 1 {
					rows = append(rows, []any{int64(4)})
				}
				run(fixture.Select("SELECT n FROM og_index_constant."+name+" ORDER BY n", []string{"n"}, []string{"INT4"}, rows))
			}
			if phase == 0 {
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO og_object_reader", 0))
				for _, h := range held {
					fixture.RunStep(t, p.Ctx, h, record, fixture.Select(h.SQL, []string{"n"}, []string{"INT4"}, [][]any{{int64(3)}}, int64(3)))
					fresh(fixture.Select(h.SQL, []string{"n"}, []string{"INT4"}, [][]any{{int64(13)}}, int64(13)))
				}
			}
		}
	})
}
