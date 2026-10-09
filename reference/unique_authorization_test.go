package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceUniqueAuthorization(t *testing.T) {
	roleReferenceCleanup(t, []string{"unique_actor"})
	uniqueReferenceSchemaCleanup(t, "unique_guarded")
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a, _ := referenceLockConnections(t, p.ctx, backend.db)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, backend.recorder, s) }
			run(referenceExec("CREATE ROLE unique_actor", 0))
			run(referenceExec("CREATE SCHEMA unique_guarded", 0))
			if backend.pg {
				// PostgreSQL GRANT setup has no native SQL equivalent.
				run(referenceExec("GRANT USAGE ON SCHEMA public TO unique_actor", 0))
				run(referenceExec("GRANT USAGE ON SCHEMA unique_guarded TO unique_actor", 0))
			}
			run(referenceExec("CREATE TABLE t(a INT,b TEXT,CONSTRAINT u UNIQUE NULLS NOT DISTINCT(a,b))", 0))
			run(referenceExec("INSERT INTO t VALUES(1,'secret')", 1))
			run(referenceExec("SET SESSION AUTHORIZATION unique_actor", 0))
			// Native public grants USAGE/CREATE by default; an independently
			// owned nonpublic namespace is the shared schema-denial witness.
			run(referenceImplicitError("CREATE TABLE unique_guarded.denied(a INT UNIQUE)", referenceDiagnostic{Code: "42501", Message: "permission denied for schema unique_guarded", Position: "14"}))
			run(referenceImplicitError("INSERT INTO t VALUES(2,'x')", referenceDiagnostic{Code: "42501", Message: "permission denied for table t"}))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			// Native all/partial/no-key SELECT Detail authority requires
			// supplemental internal catalog-ACL units; SQL GRANT is unsupported.
			if backend.pg {
				run(referenceExec("GRANT INSERT ON t TO unique_actor", 0))
				for _, mode := range []string{"none", "partial", "all"} {
					if mode == "partial" {
						run(referenceExec("GRANT SELECT(a) ON t TO unique_actor", 0))
					}
					if mode == "all" {
						run(referenceExec("GRANT SELECT(b) ON t TO unique_actor", 0))
					}
					run(referenceExec("SET SESSION AUTHORIZATION unique_actor", 0))
					want := uniqueReferenceDuplicate("t", "u", "")
					if mode == "all" {
						want.Detail = "Key (a, b)=(1, secret) already exists."
					}
					s := referencePrepared(referenceErrorDiagnostic("INSERT INTO t VALUES($1,$2)", want, int64(1), "secret"))
					s.errorStage = "execute"
					run(s)
					run(referenceExec("RESET SESSION AUTHORIZATION", 0))
				}
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
		{"unique_index", "CREATE TABLE t(a INT)", "CREATE UNIQUE INDEX u ON t(a)", "unique indexes are unsupported", "UNIQUE"},
		{"rename_constraint", "CREATE TABLE t(a INT CONSTRAINT u UNIQUE)", "ALTER TABLE t RENAME CONSTRAINT u TO renamed_u", "RENAME CONSTRAINT is unsupported", "CONSTRAINT"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
					namespace := uniqueReferencePublicNamespace(t, p.ctx, backend.db, backend.pg)
					if c.setup != "" {
						run(referenceExec(c.setup, 0))
					}
					if backend.pg {
						run(referenceExec(c.query, 0))
					} else {
						run(referenceImplicitError(c.query, referenceDiagnostic{Code: "0A000", Message: c.message, Position: referencePosition(strings.Index(c.query, c.token) + 1)}))
						want := [][]any{{"t", "r"}}
						if c.name == "deferrable" {
							want = nil
						}
						if c.name == "rename_constraint" {
							want = [][]any{{"t", "r"}, {"u", "i"}}
						}
						run(referenceSelect("SELECT relname,relkind FROM pg_class WHERE relnamespace=$1 ORDER BY relname", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, want, namespace))
					}
				})
			}
		})
	}
}

func TestReferenceUniqueIndexExclusionPreservesState(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
					run(referenceExec("CREATE TABLE t(n INT,c INT,s TEXT)", 0))
					run(referenceExec("INSERT INTO t VALUES(2,1,'one')", 1))
					var before bytes.Buffer
					if !b.pg {
						if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
							t.Fatal(err)
						}
					}
					query := "CREATE UNIQUE INDEX i ON t(n)"
					s := referenceExec(query, 0)
					if !b.pg {
						s = referenceImplicitError(query, referenceDiagnostic{Code: "0A000", Message: "unique indexes are unsupported", Position: "8"})
						s.errorStage = "execute"
						if prepared {
							s.errorStage = "prepare"
						}
					}
					if prepared {
						s = referencePrepared(s)
					}
					run(s)
					if !b.pg {
						var after bytes.Buffer
						if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || before.String() != after.String() {
							t.Fatal("excluded declaration changed state", err, after.String())
						}
					}
					run(referenceSelect("SELECT c,s FROM t", []string{"c", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
					expected := [][]any(nil)
					if b.pg {
						expected = [][]any{{"i"}}
					}
					run(referenceSelect("SELECT relname FROM pg_class WHERE relname='i' OR relname='ix'", []string{"relname"}, []string{"NAME"}, expected))
					if b.pg {
						run(referenceExec("DROP INDEX i", 0))
					}
					run(referenceExec("CREATE INDEX i ON t(n)", 0))
					run(referenceExec("ALTER TABLE t ALTER c TYPE TEXT USING c::text", 0))
					run(referenceSelect("SELECT c FROM t", []string{"c"}, []string{"TEXT"}, [][]any{{"1"}}))
				})
			}
		})
	}
}
