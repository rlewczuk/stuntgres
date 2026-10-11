package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUniqueAuthorization(t *testing.T) {
	uniqueReferenceAuthorization(t, false)
}

func TestReferenceUniqueIndexAuthorization(t *testing.T) {
	uniqueReferenceAuthorization(t, true)
}

func uniqueReferenceAuthorization(t *testing.T, standalone bool) {
	fixture.RoleReferenceCleanup(t, []string{"unique_actor"})
	fixture.UniqueReferenceSchemaCleanup(t, "unique_guarded")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, _ := fixture.LockConnections(t, p.Ctx, backend.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, backend.Recorder, s) }
			run(fixture.Exec("CREATE ROLE unique_actor", 0))
			run(fixture.Exec("CREATE SCHEMA unique_guarded", 0))
			if backend.PG {
				// PostgreSQL GRANT setup has no native SQL equivalent.
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO unique_actor", 0))
				run(fixture.Exec("GRANT USAGE ON SCHEMA unique_guarded TO unique_actor", 0))
			}
			uniqueIndexReferenceCreate(run, standalone, "CREATE TABLE t(a INT,b TEXT,CONSTRAINT u UNIQUE NULLS NOT DISTINCT(a,b))", "CREATE TABLE t(a INT,b TEXT)", "CREATE UNIQUE INDEX u ON t(a,b) NULLS NOT DISTINCT")
			run(fixture.Exec("INSERT INTO t VALUES(1,'secret')", 1))
			run(fixture.Exec("SET SESSION AUTHORIZATION unique_actor", 0))
			// Native public grants USAGE/CREATE by default; an independently
			// owned nonpublic namespace is the shared schema-denial witness.
			run(fixture.ImplicitError("CREATE TABLE unique_guarded.denied(a INT UNIQUE)", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema unique_guarded", Position: "14"}))
			run(fixture.ImplicitError("INSERT INTO t VALUES(2,'x')", fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"}))
			if standalone {
				s := fixture.Prepared(fixture.ImplicitError("CREATE UNIQUE INDEX denied ON t(a)", fixture.Diagnostic{Code: "42501", Message: "must be owner of table t"}))
				s.ErrorStage = "execute"
				run(s)
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("GRANT INSERT ON t TO unique_actor", 0))
			for _, mode := range []string{"none", "partial", "all"} {
				if mode == "partial" {
					run(fixture.Exec("GRANT SELECT(a) ON t TO unique_actor", 0))
				}
				if mode == "all" {
					run(fixture.Exec("GRANT SELECT(b) ON t TO unique_actor", 0))
				}
				run(fixture.Exec("SET SESSION AUTHORIZATION unique_actor", 0))
				want := fixture.UniqueReferenceDuplicate("t", "u", "")
				if mode == "all" {
					want.Detail = "Key (a, b)=(1, secret) already exists."
				}
				s := fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO t VALUES($1,$2)", want, int64(1), "secret"))
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			}
		})
	}
}

func TestReferenceUniqueExcludedControls(t *testing.T) {
	// Core SQL witnesses share a body; excluded native syntax has an explicit
	// complete capability diagnostic rather than pretend PostgreSQL success.
	for _, c := range []struct{ name, setup, query, message, token string }{
		{"deferrable", "", "CREATE TABLE t(a INT UNIQUE DEFERRABLE INITIALLY DEFERRED)", "UNIQUE deferrability is unsupported", "DEFERRABLE"},
		{"alter_add", "CREATE TABLE t(a INT)", "ALTER TABLE t ADD CONSTRAINT u UNIQUE(a)", "ALTER TABLE action is unsupported", "ADD"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
					namespace := fixture.UniqueReferencePublicNamespace(t, p.Ctx, backend.DB, backend.PG)
					if c.setup != "" {
						run(fixture.Exec(c.setup, 0))
					}
					if backend.PG || c.name == "alter_add" {
						run(fixture.Exec(c.query, 0))
						if c.name == "alter_add" {
							run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
							run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1)", fixture.UniqueReferenceDuplicate("t", "u", "Key (a)=(1) already exists.")))
							run(fixture.Select("SELECT conname,contype FROM pg_constraint WHERE conrelid='t'::regclass::oid", []string{"conname", "contype"}, []string{"NAME", "CHAR"}, [][]any{{"u", "u"}}))
						}
					} else {
						run(fixture.ImplicitError(c.query, fixture.Diagnostic{Code: "0A000", Message: c.message, Position: fixture.Position(strings.Index(c.query, c.token) + 1)}))
						want := [][]any{{"t", "r"}}
						if c.name == "deferrable" {
							want = nil
						}
						run(fixture.Select("SELECT relname,relkind FROM pg_class WHERE relnamespace=$1 ORDER BY relname", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, want, namespace))
					}
				})
			}
		})
	}
}

func TestReferenceUniqueIndexSupportedControlsPreserveState(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(n INT,c INT,s TEXT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(2,1,'one')", 1))
					query := "CREATE UNIQUE INDEX i ON t(n)"
					s := fixture.Exec(query, 0)
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
					if !b.PG {
						var after bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || strings.Count(after.String(), "CREATE UNIQUE INDEX") != 1 {
							t.Fatal("supported declaration missing from dump", err, after.String())
						}
					}
					run(fixture.Select("SELECT c,s FROM t", []string{"c", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
					run(fixture.Select("SELECT relname FROM pg_class WHERE relname='i' OR relname='ix'", []string{"relname"}, []string{"NAME"}, [][]any{{"i"}}))
					run(fixture.Exec("DROP INDEX i", 0))
					run(fixture.Exec("CREATE INDEX i ON t(n)", 0))
					run(fixture.Exec("ALTER TABLE t ALTER c TYPE TEXT USING c::text", 0))
					run(fixture.Select("SELECT c FROM t", []string{"c"}, []string{"TEXT"}, [][]any{{"1"}}))
				})
			}
		})
	}
}
