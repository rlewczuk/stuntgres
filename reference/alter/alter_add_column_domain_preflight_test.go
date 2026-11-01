package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// The deliberately declared-immutable index function makes a physical rebuild
// observable without replacing any stored routine declaration. Its row argument
// prevents constant folding from substituting for actual index-key evaluation.
func TestReferenceAlterAddColumnDomainPreflight(t *testing.T) {
	for _, volatile := range []bool{false, true} {
		for _, held := range []bool{false, true} {
			t.Run(fmt.Sprintf("volatile-%t/held-%t", volatile, held), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						conn, err := backend.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						run := func(s fixture.Step) {
							fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
						}
						run(fixture.Exec("CREATE FUNCTION add_guard() RETURNS TEXT LANGUAGE SQL IMMUTABLE RETURN 'ok'::text", 0))
						run(fixture.Exec("CREATE DOMAIN add_guarded AS TEXT CONSTRAINT add_guarded_check CHECK(add_guard() IS NOT NULL)", 0))
						run(fixture.Exec("CREATE SEQUENCE add_index_steps START 1 CACHE 1", 0))
						run(fixture.Exec("CREATE FUNCTION add_index_key(integer) RETURNS BIGINT LANGUAGE SQL IMMUTABLE RETURN nextval('add_index_steps'::regclass)+$1", 0))
						run(fixture.Exec("CREATE TABLE add_preflight(id INT)", 0))
						run(fixture.Exec("INSERT INTO add_preflight VALUES(1),(2)", 2))
						run(fixture.Exec("CREATE INDEX add_preflight_index ON add_preflight(add_index_key(id))", 0))
						locators := func() [2][2]int64 {
							t.Helper()
							var result [2][2]int64
							for i, name := range []string{"add_preflight", "add_preflight_index"} {
								if err := conn.QueryRowContext(p.Ctx, "SELECT oid,relfilenode FROM pg_class WHERE oid=$1::regclass", name).Scan(&result[i][0], &result[i][1]); err != nil {
									t.Fatal(err)
								}
								if result[i][0] <= 0 || result[i][1] <= 0 {
									t.Fatal("fixture lacks an actual heap/index incarnation", name, result[i])
								}
							}
							return result
						}
						beforeLocators := locators()
						// Discard setup's legitimate index demands. No later query uses
						// the index expression, so only ALTER can consume this marker.
						run(fixture.Select("SELECT setval('add_index_steps',100,false) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(100)}}))
						query := "ALTER TABLE add_preflight ADD COLUMN note TEXT DEFAULT ('seed'::text::add_guarded)::text"
						if volatile {
							run(fixture.Exec("CREATE SEQUENCE add_default_steps START 1 CACHE 1", 0))
							query = "ALTER TABLE add_preflight ADD COLUMN note TEXT DEFAULT (nextval('add_default_steps'::regclass)::text::add_guarded)::text"
						}
						var statement *sql.Stmt
						if held {
							statement, err = conn.PrepareContext(p.Ctx, query)
							if err != nil {
								t.Fatal(err)
							}
							defer statement.Close()
						}
						step := fixture.Exec(query, 0)
						if held {
							step.Prepared = true
							fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: statement, SQL: query}, backend.Recorder, step)
						} else {
							run(step)
						}
						// Missing-value metadata preserves both physical incarnations;
						// the volatile row rewrite replaces both, without changing OIDs.
						for i, after := range locators() {
							before := beforeLocators[i]
							if after[0] != before[0] || (after[1] != before[1]) != volatile {
								t.Fatal("ADD preflight misclassified heap/index replacement", before, after, volatile)
							}
						}
						wantIndexNext := int64(100)
						if volatile {
							// A genuine volatile default rewrites both rows and rebuilds
							// the populated index; this path must not be bypassed.
							wantIndexNext = 102
						}
						run(fixture.Select("SELECT nextval('add_index_steps') AS v", []string{"v"}, []string{"INT8"}, [][]any{{wantIndexNext}}))
						if volatile {
							values := fixture.Select("SELECT note FROM add_preflight", []string{"note"}, []string{"TEXT"}, [][]any{{"1"}, {"2"}})
							values.Bag = true
							run(values)
							run(fixture.Select("SELECT nextval('add_default_steps') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(3)}}))
						} else {
							run(fixture.Select("SELECT id,note FROM add_preflight ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "seed"}, {int64(2), "seed"}}))
						}
						// Future omission consumes the retained definition, not an
						// execution-private ADD fill or a stale prepared value.
						run(fixture.Prepared(fixture.Exec("INSERT INTO add_preflight(id) VALUES(3)", 1)))
						want := "seed"
						if volatile {
							want = "4"
						}
						run(fixture.Select("SELECT note FROM add_preflight WHERE id=3", []string{"note"}, []string{"TEXT"}, [][]any{{want}}))
					})
				}
			})
		}
	}
}

func TestReferenceAlterAddColumnDomainPreflightFailure(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, populated := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared-%t/populated-%t", prepared, populated), func(t *testing.T) {
				alterAddReferenceScenario(t, func(p *fixture.Pair, backend fixture.Backend) {
					run := func(s fixture.Step) {
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					}
					run(fixture.Exec("CREATE DOMAIN add_reject AS TEXT CONSTRAINT add_reject_check CHECK(VALUE <> 'bad')", 0))
					run(fixture.Exec("CREATE TABLE add_atomic(id INT)", 0))
					if populated {
						run(fixture.Exec("INSERT INTO add_atomic VALUES(1)", 1))
					}
					run(fixture.Exec("CREATE SEQUENCE add_failure_index_steps START 1 CACHE 1", 0))
					run(fixture.Exec("CREATE FUNCTION add_failure_index_key(integer) RETURNS BIGINT LANGUAGE SQL IMMUTABLE RETURN nextval('add_failure_index_steps'::regclass)+$1", 0))
					run(fixture.Exec("CREATE INDEX add_atomic_index ON add_atomic(add_failure_index_key(id))", 0))
					run(fixture.Select("SELECT setval('add_failure_index_steps',100,false) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(100)}}))
					// The destination is base TEXT, so its inner domain conversion
					// is demanded once even when there are no existing rows.
					diagnostic := fixture.ControlsReferenceDiagnostic("23514", `value for domain add_reject violates check constraint "add_reject_check"`)
					diagnostic.Schema, diagnostic.DataType, diagnostic.Constraint = "public", "add_reject", "add_reject_check"
					failure := fixture.ErrorDiagnostic("ALTER TABLE add_atomic ADD note TEXT DEFAULT ('bad'::text::add_reject)::text", diagnostic)
					failure.Prepared, failure.ErrorStage = prepared, "execute"
					run(failure)
					// Observe before retry: sequence effects would survive a failed
					// statement if it incorrectly demanded the unrelated index.
					run(fixture.Select("SELECT nextval('add_failure_index_steps') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(100)}}))
					var rows [][]any
					if populated {
						rows = [][]any{{int64(1)}}
					}
					run(fixture.Select("SELECT * FROM add_atomic", []string{"id"}, []string{"INT4"}, rows))
					retry := fixture.Exec("ALTER TABLE add_atomic ADD note TEXT DEFAULT ('good'::text::add_reject)::text", 0)
					retry.Prepared = prepared
					run(retry)
					if populated {
						rows = [][]any{{int64(1), "good"}}
					}
					run(fixture.Select("SELECT * FROM add_atomic", []string{"id", "note"}, []string{"INT4", "TEXT"}, rows))
				})
			})
		}
	}
}

func TestReferenceAlterAddColumnDomainPreflightCompanionRewrite(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("held-%t", held), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					conn, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
					run(fixture.Exec("CREATE SEQUENCE add_companion_checks START 1 CACHE 1", 0))
					run(fixture.Exec("CREATE SEQUENCE add_companion_rows START 1 CACHE 1", 0))
					run(fixture.Exec("CREATE SEQUENCE add_companion_indexes START 1 CACHE 1", 0))
					run(fixture.Exec("CREATE FUNCTION add_companion_guard() RETURNS TEXT LANGUAGE SQL IMMUTABLE RETURN 'ok'::text", 0))
					run(fixture.Exec("CREATE DOMAIN add_companion_domain AS TEXT CHECK(add_companion_guard() IS NOT NULL) CHECK(nextval('add_companion_checks'::regclass)>0)", 0))
					run(fixture.Exec("CREATE FUNCTION add_companion_key(integer) RETURNS BIGINT LANGUAGE SQL IMMUTABLE RETURN nextval('add_companion_indexes'::regclass)+$1", 0))
					run(fixture.Exec("CREATE TABLE add_companion(id INT)", 0))
					run(fixture.Exec("INSERT INTO add_companion VALUES(1),(2)", 2))
					run(fixture.Exec("CREATE INDEX add_companion_index ON add_companion(add_companion_key(id))", 0))
					run(fixture.Select("SELECT setval('add_companion_indexes',100,false) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(100)}}))
					query := "ALTER TABLE add_companion ADD note TEXT DEFAULT ('seed'::text::add_companion_domain)::text, ADD draw BIGINT DEFAULT nextval('add_companion_rows'::regclass)"
					step := fixture.Exec(query, 0)
					if held {
						statement, err := conn.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer statement.Close()
						step.Prepared = true
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: statement, SQL: query}, backend.Recorder, step)
					} else {
						run(step)
					}
					// PostgreSQL demands the initial missing-value conversion once,
					// then converts again per live row during a genuine companion
					// rewrite. It must also rebuild the unrelated populated index.
					run(fixture.Select("SELECT nextval('add_companion_checks') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(4)}}))
					run(fixture.Select("SELECT nextval('add_companion_indexes') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(102)}}))
					run(fixture.Select("SELECT nextval('add_companion_rows') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(3)}}))
					values := fixture.Select("SELECT note,draw FROM add_companion", []string{"note", "draw"}, []string{"TEXT", "INT8"}, [][]any{{"seed", int64(1)}, {"seed", int64(2)}})
					values.Bag = true
					run(values)
					run(fixture.Select("SELECT id,note FROM add_companion ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "seed"}, {int64(2), "seed"}}))
				})
			}
		})
	}
}
