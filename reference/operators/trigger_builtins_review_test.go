package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// These release-source expectations deliberately precede native remediation.
// The root owns pinned discovery, including all optional diagnostic fields.
func TestReferenceTriggerBuiltinsSystemAttributes(t *testing.T) {
	for _, tc := range []struct{ name, function, args, message string }{
		{"destination", "tsvector_update_trigger", "'tableoid','pg_catalog.simple','title'", `column "tableoid" is not of tsvector type`},
		{"source", "tsvector_update_trigger", "'v','pg_catalog.simple','tableoid'", `column "tableoid" is not of a character type`},
		{"configuration", "tsvector_update_trigger_column", "'v','ctid','title'", `column "ctid" is not of regconfig type`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE TABLE tb_system(id integer,title text,v tsvector)")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tb_system VALUES(1,'Rats',NULL)", 1))
				userOperatorExec(t, p, b, c, "CREATE TRIGGER a BEFORE UPDATE ON tb_system FOR EACH ROW EXECUTE FUNCTION pg_catalog."+tc.function+"("+tc.args+")")
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("UPDATE tb_system SET title=title", fixture.ControlsReferenceDiagnostic("42804", tc.message))
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, builtinVectorRows("SELECT id,v::text AS vector FROM tb_system", []any{int64(1), nil}))
			})
		})
	}
	t.Run("oid_configuration_lazy", func(t *testing.T) {
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE tb_system_cfg(id integer,title text,v tsvector)",
				"CREATE TRIGGER a BEFORE INSERT ON tb_system_cfg FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger_column('v','tableoid','title')")
			for i, prepared := range []bool{false, true} {
				s := builtinVectorRows("INSERT INTO tb_system_cfg VALUES($1,NULL,NULL) RETURNING id,v::text AS vector", []any{int64(i + 1), ""})
				s.Args, s.Prepared = []any{i + 1}, prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			for _, prepared := range []bool{false, true} {
				for _, source := range []string{"''", "'Rats'"} {
					s := fixture.ErrorDiagnostic("INSERT INTO tb_system_cfg VALUES(3,"+source+",NULL)", fixture.ControlsReferenceDiagnostic("XX000", "cache lookup failed for text search configuration 0"))
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			}
		})
	})
}

func TestReferenceTriggerBuiltinsDomainReplacement(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c, "CREATE DOMAIN tb_null_vector AS tsvector CHECK(VALUE IS NULL)",
			"CREATE TABLE tb_domain_replace(id integer,title text,v tb_null_vector)",
			"CREATE TRIGGER a BEFORE INSERT OR UPDATE ON tb_domain_replace FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger('v','pg_catalog.simple','title')")
		for i, prepared := range []bool{false, true} {
			s := builtinVectorRows("INSERT INTO tb_domain_replace VALUES($1,'Rats',NULL) RETURNING id,v::text AS vector", []any{int64(i + 1), "'rats':1"})
			s.Args, s.Prepared = []any{i + 1}, prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		// The builtin's tuple replacement is not a second domain input operation.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(builtinVectorRows("UPDATE tb_domain_replace SET title='Cats' WHERE id=1 RETURNING id,v::text AS vector", []any{int64(1), "'cats':1"})))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, builtinVectorRows("SELECT id,v::text AS vector FROM tb_domain_replace ORDER BY id", []any{int64(1), "'cats':1"}, []any{int64(2), "'rats':1"}))
	})
}

func TestReferenceTriggerBuiltinsRawReturning(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c, "CREATE TABLE tb_raw(id integer PRIMARY KEY,title text,v tsvector)",
			"CREATE TRIGGER a BEFORE INSERT OR UPDATE ON tb_raw FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger('v','pg_catalog.simple','title')",
			"CREATE TRIGGER z BEFORE UPDATE ON tb_raw FOR EACH ROW EXECUTE FUNCTION pg_catalog.suppress_redundant_updates_trigger()")
		step := func(q string, rows ...[]any) fixture.Step {
			s := fixture.Select(q, []string{"id", "raw_vector", "text_vector"}, []string{"INT4", "TSVECTOR", "TEXT"}, rows)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				meta, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				for i, col := range meta {
					scan := reflect.TypeFor[string]()
					if i == 0 {
						scan = reflect.TypeFor[int64]()
						if b.PG {
							scan = reflect.TypeFor[int32]()
						}
					} else if i == 1 && b.PG {
						scan = reflect.TypeFor[any]()
					}
					if col.ScanType() != scan {
						t.Fatalf("column %d ScanType=%v want=%v", i, col.ScanType(), scan)
					}
					nullable, known := col.Nullable()
					if b.PG || i == 2 {
						if nullable || known {
							t.Fatalf("column %d nullable=%t/%t want unknown", i, nullable, known)
						}
					} else if !known || nullable != (i == 1) {
						t.Fatalf("column %d nullable=%t/%t", i, nullable, known)
					}
					length, lengthKnown := col.Length()
					if i == 2 {
						if length != math.MaxInt64 || !lengthKnown {
							t.Fatalf("text length=%d/%t", length, lengthKnown)
						}
					} else if length != 0 || lengthKnown {
						t.Fatalf("column %d length=%d/%t want unknown", i, length, lengthKnown)
					}
					precision, scale, decimalKnown := col.DecimalSize()
					if precision != 0 || scale != 0 || decimalKnown {
						t.Fatalf("column %d decimal=%d,%d/%t", i, precision, scale, decimalKnown)
					}
				}
			}
			return s
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, step("INSERT INTO tb_raw VALUES(1,'Rats',NULL) RETURNING id,v AS raw_vector,v::text AS text_vector", []any{int64(1), "'rats':1", "'rats':1"}))
		q := "UPDATE tb_raw SET title=$1 WHERE id=1 RETURNING id,v AS raw_vector,v::text AS text_vector"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		s := step(q, []any{int64(1), "'cats':1", "'cats':1"})
		s.Args = []any{"Cats"}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		s = step(q)
		s.Args = []any{"Cats"}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, step("UPDATE tb_raw SET title=title WHERE id=1 RETURNING id,v AS raw_vector,v::text AS text_vector"))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step("UPDATE tb_raw SET title='Dogs' WHERE id=1 RETURNING id,v AS raw_vector,v::text AS text_vector", []any{int64(1), "'dogs':1", "'dogs':1"})))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step("UPDATE tb_raw SET title=title WHERE id=1 RETURNING id,v AS raw_vector,v::text AS text_vector")))
	})
}

func TestReferenceTriggerBuiltinsConfigNameReuse(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "tb_name_reuse")
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c, "CREATE SCHEMA tb_name_reuse",
			"CREATE TEXT SEARCH DICTIONARY tb_name_reuse.reject (TEMPLATE=pg_catalog.simple,Accept=false)",
			"CREATE TEXT SEARCH CONFIGURATION tb_name_reuse.live (COPY=pg_catalog.simple)",
			"CREATE TABLE tb_name_reuse.docs(id integer,title text,v tsvector)",
			"CREATE TRIGGER a BEFORE INSERT OR UPDATE ON tb_name_reuse.docs FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger('v','tb_name_reuse.live','title')")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tb_name_reuse.docs VALUES(1,'Rats',NULL)", 1))
		q := "UPDATE tb_name_reuse.docs SET title=$1 WHERE id=1 RETURNING id,v::text AS vector"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		check := func(conn *sql.Conn, backend fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			executor := fixture.HeldStatement{Executor: conn, SQL: q}
			if conn == c {
				executor.Statement = stmt
			} else {
				fresh, err := conn.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer fresh.Close()
				executor.Statement = fresh
			}
			call := func(vector string) {
				s := builtinVectorRows(q, []any{int64(1), vector})
				s.Args = []any{"Cats"}
				fixture.RunStep(t, p.Ctx, executor, backend.Recorder, s)
			}
			run(builtinVectorRows("SELECT id,v::text AS vector FROM tb_name_reuse.docs", []any{int64(1), "'rats':1"}))
			run(fixture.Exec("BEGIN", 0))
			call("'cats':1")
			run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION tb_name_reuse.live RENAME TO old_live", 0))
			run(fixture.Exec("SAVEPOINT missing_name", 0))
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42704", `text search configuration "tb_name_reuse.live" does not exist`), "Cats")
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, executor, backend.Recorder, s)
			run(fixture.Exec("ROLLBACK TO SAVEPOINT missing_name", 0))
			run(builtinVectorRows("SELECT id,v::text AS vector FROM tb_name_reuse.docs", []any{int64(1), "'cats':1"}))
			run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION tb_name_reuse.live (COPY=pg_catalog.simple)", 0))
			run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION tb_name_reuse.live ALTER MAPPING FOR asciiword WITH tb_name_reuse.reject", 0))
			call("")
			run(fixture.Exec("ROLLBACK", 0))
		}
		parametersReplay(t, p, b, c, "tb_name_reuse", check)
	})
}

func TestReferenceTriggerBuiltinsDeclaredCoercibility(t *testing.T) {
	for _, tc := range []struct {
		name, declaration, method, context string
		accepted                           bool
	}{
		{"implicit_binary", "WITHOUT FUNCTION AS IMPLICIT", "b", "i", true},
		{"assignment_binary", "WITHOUT FUNCTION AS ASSIGNMENT", "b", "a", false},
		{"implicit_inout", "WITH INOUT AS IMPLICIT", "i", "i", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				castDefinitionCleanup(t, p, b, [2]string{"bytea", "text"})
				userOperatorExec(t, p, b, c, "CREATE CAST (bytea AS text) "+tc.declaration,
					"CREATE TABLE tb_declared_cast(id integer,title bytea,v tsvector)",
					"CREATE TRIGGER a BEFORE INSERT ON tb_declared_cast FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger('v','pg_catalog.simple','title')")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT castmethod::text AS method,castcontext::text AS context,castfunc=0 AS no_function FROM pg_cast WHERE castsource='bytea'::regtype AND casttarget='text'::regtype", []string{"method", "context", "no_function"}, []string{"TEXT", "TEXT", "BOOL"}, [][]any{{tc.method, tc.context, true}}))
				for i, prepared := range []bool{false, true} {
					q := `INSERT INTO tb_declared_cast VALUES($1,'\x52617473'::bytea,NULL) RETURNING id,v::text AS vector`
					s := builtinVectorRows(q, []any{int64(i + 1), "'rats':1"})
					if !tc.accepted {
						s = fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42804", `column "title" is not of a character type`))
						s.ErrorStage = "execute"
					}
					s.Args, s.Prepared = []any{i + 1}, prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
				count := int64(0)
				if tc.accepted {
					count = 2
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM tb_declared_cast", []string{"n"}, []string{"INT8"}, [][]any{{count}}))
				// Global casts must be gone before the next backend uses the pair.
				userOperatorExec(t, p, b, c, "DROP CAST (bytea AS text)")
			})
		})
	}
}
