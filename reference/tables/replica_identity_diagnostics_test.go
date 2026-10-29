package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceReplicaIdentityEligibility(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			run := func(s fixture.Step) { p.Step(t, s) }
			replicaSetup(run)
			for _, q := range []string{
				"CREATE TABLE ri_other(k INT NOT NULL)", "CREATE UNIQUE INDEX ri_other_idx ON ri_other(k)", "CREATE TABLE ri_not_index(k INT)",
				"CREATE INDEX ri_bad ON ri_t(k)", "CREATE INDEX ri_hash ON ri_t USING hash(k)",
				"CREATE INDEX ri_defects ON ri_t((k+1)) WHERE k>0",
				"CREATE UNIQUE INDEX ri_partial ON ri_t(k) WHERE k>0",
				"CREATE UNIQUE INDEX ri_nullable ON ri_t(p)", "CREATE UNIQUE INDEX ri_nnd ON ri_t(p) NULLS NOT DISTINCT",
				"CREATE UNIQUE INDEX ri_good_nnd ON ri_t(k) NULLS NOT DISTINCT",
				"ALTER TABLE ri_t ADD CONSTRAINT ri_defer UNIQUE(k) DEFERRABLE INITIALLY IMMEDIATE",
				"ALTER TABLE ri_t ADD CONSTRAINT ri_defer_nullable UNIQUE(p) DEFERRABLE INITIALLY DEFERRED",
				"ALTER TABLE ri_t ADD CONSTRAINT ri_exclude EXCLUDE USING btree(k WITH =) DEFERRABLE",
			} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("ALTER TABLE ri_t REPLICA IDENTITY USING INDEX ri_good_nnd", 0))
			for _, tc := range []struct{ name, code, message string }{
				{"ri_missing", "42704", `index "ri_missing" for table "ri_t" does not exist`},
				{"ri_not_index", "42809", `"ri_not_index" is not an index`},
				{"ri_other_idx", "42809", `"ri_other_idx" is not an index for table "ri_t"`},
				{"ri_bad", "42809", `cannot use non-unique index "ri_bad" as replica identity`},
				{"ri_hash", "42809", `cannot use non-unique index "ri_hash" as replica identity`},
				{"ri_defects", "42809", `cannot use non-unique index "ri_defects" as replica identity`},
				{"ri_exclude", "42809", `cannot use non-unique index "ri_exclude" as replica identity`},
				{"ri_defer", "0A000", `cannot use non-immediate index "ri_defer" as replica identity`},
				{"ri_defer_nullable", "0A000", `cannot use non-immediate index "ri_defer_nullable" as replica identity`},
				{"ri_partial", "0A000", `cannot use partial index "ri_partial" as replica identity`},
				{"ri_nullable", "42809", `index "ri_nullable" cannot be used as replica identity because column "p" is nullable`},
				{"ri_nnd", "42809", `index "ri_nnd" cannot be used as replica identity because column "p" is nullable`},
			} {
				s := fixture.ImplicitError("ALTER TABLE ri_t REPLICA IDENTITY USING INDEX "+tc.name, fixture.Diagnostic{Code: tc.code, Message: tc.message})
				if prepared {
					s = fixture.Prepared(s)
					s.ErrorStage = "execute"
				}
				run(s)
				run(replicaMode("ri_t", "i"))
				run(fixture.Select("SELECT c.relname FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE i.indrelid='ri_t'::regclass AND i.indisreplident", []string{"relname"}, []string{"NAME"}, [][]any{{"ri_good_nnd"}}))
				run(replicaData())
			}
		})
	}
	t.Run("unrepresented_setups", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE ri_t(k INT NOT NULL)", 0))
				expr := "CREATE UNIQUE INDEX ri_expr ON ri_t((k+1))"
				if b.PG {
					run(fixture.Exec(expr, 0))
					run(fixture.ImplicitError("ALTER TABLE ri_t REPLICA IDENTITY USING INDEX ri_expr", fixture.Diagnostic{Code: "0A000", Message: `cannot use expression index "ri_expr" as replica identity`}))
				} else {
					// Unique expressions remain an honest native setup boundary.
					run(fixture.ImplicitError(expr, fixture.Diagnostic{Code: "0A000", Message: "unique expression indexes are unsupported", Position: fixture.Position(strings.Index(expr, "(k+1)") + 1)}))
				}
				run(replicaMode("ri_t", "d"))
			})
		}
	})
}

func TestReferenceReplicaIdentityOptions(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			run := func(s fixture.Step) { p.Step(t, s) }
			replicaSetup(run)
			run(fixture.Exec("ALTER TABLE ri_t REPLICA IDENTITY USING INDEX ri_k", 0))
			for _, tc := range []struct{ q, token string }{
				{"ALTER TABLE ri_t REPLICA IDENTITY", ""},
				{"ALTER TABLE ri_t REPLICA IDENTITY UNKNOWN", "UNKNOWN"},
				{"ALTER TABLE ri_t REPLICA IDENTITY USING INDEX", ""},
				{"ALTER TABLE ri_t REPLICA IDENTITY USING INDEX public.ri_k", "."},
				{"ALTER TABLE ri_t REPLICA IDENTITY FULL junk", "junk"},
				{"ALTER TABLE ri_t REPLICA IDENTITY NOTHING, REPLICA IDENTITY FULL junk", "junk"},
			} {
				position := utf8.RuneCountInString(tc.q) + 1
				message := "syntax error at end of input"
				if tc.token != "" {
					position = utf8.RuneCountInString(tc.q[:strings.Index(tc.q, tc.token)]) + 1
					message = `syntax error at or near "` + tc.token + `"`
				}
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42601", Message: message, Position: fixture.Position(position)})
				if prepared {
					s = fixture.Prepared(s)
					s.ErrorStage = "prepare"
				}
				run(s)
				run(replicaMode("ri_t", "i"))
				run(replicaFlags("ri_k"))
				run(replicaData())
			}
			run(fixture.Exec("CREATE TABLE replica(replica INT)", 0))
			run(fixture.Exec("ALTER TABLE replica REPLICA IDENTITY FULL", 0))
			run(replicaMode("replica", "f"))
		})
	}
}

func TestReferenceReplicaIdentityDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	run := func(s fixture.Step) { p.Step(t, s) }
	replicaSetup(run)
	run(fixture.Exec("CREATE VIEW ri_v AS SELECT k FROM ri_t", 0))
	run(fixture.Exec("CREATE SEQUENCE ri_s", 0))
	run(fixture.Exec("CREATE TYPE ri_c AS(k INT)", 0))
	run(fixture.Exec("CREATE FOREIGN DATA WRAPPER ri_fdw", 0))
	run(fixture.Exec("CREATE SERVER ri_server FOREIGN DATA WRAPPER ri_fdw", 0))
	run(fixture.Exec("CREATE FOREIGN TABLE ri_foreign(k INT) SERVER ri_server", 0))
	for _, tc := range []struct{ name, kind string }{{"ri_v", "views"}, {"ri_s", "sequences"}, {"ri_k", "indexes"}, {"ri_c", "composite types"}, {"ri_foreign", "foreign tables"}} {
		for _, prepared := range []bool{false, true} {
			want := fixture.Diagnostic{Code: "42809", Message: `ALTER action REPLICA IDENTITY cannot be performed on relation "` + tc.name + `"`, Detail: "This operation is not supported for " + tc.kind + "."}
			if tc.name == "ri_c" {
				want = fixture.Diagnostic{Code: "42809", Message: `"ri_c" is a composite type`, Hint: "Use ALTER TYPE instead."}
			}
			s := fixture.ImplicitError("ALTER TABLE "+tc.name+" REPLICA IDENTITY FULL", want)
			if prepared {
				s = fixture.Prepared(s)
			}
			run(s)
		}
	}
	run(fixture.Exec("CREATE MATERIALIZED VIEW ri_mv AS SELECT k FROM ri_t", 2))
	run(fixture.Exec("ALTER TABLE ri_mv REPLICA IDENTITY FULL", 0))
	run(replicaMode("ri_mv", "f"))
	run(fixture.ImplicitError("ALTER TABLE ri_absent REPLICA IDENTITY FULL", fixture.Diagnostic{Code: "42P01", Message: `relation "ri_absent" does not exist`}))
	run(fixture.Exec("DROP SERVER ri_server CASCADE", 0))
	run(fixture.Exec("DROP FOREIGN DATA WRAPPER ri_fdw", 0))
}

func TestReferenceReplicaIdentityAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ri_owner", "ri_stranger"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, _ := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			run(fixture.Exec("CREATE ROLE ri_owner", 0))
			run(fixture.Exec("CREATE ROLE ri_stranger", 0))
			run(fixture.Exec("SET ROLE ri_owner", 0))
			fixture.WithPublicSchemaOwner(t, p, b, "ri_owner", func() {
				run(fixture.Exec("CREATE TABLE ri_t(k INT NOT NULL)", 0))
				run(fixture.Exec("CREATE UNIQUE INDEX ri_k ON ri_t(k)", 0))
			})
			q := "ALTER TABLE ri_t REPLICA IDENTITY USING INDEX ri_k"
			held, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			run(fixture.Exec(q, 0))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("SET ROLE ri_stranger", 0))
			want := fixture.Diagnostic{Code: "42501", Message: "must be owner of table ri_t"}
			run(fixture.ImplicitError("ALTER TABLE ri_t REPLICA IDENTITY FULL", want))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: q}, b.Recorder, fixture.ImplicitError(q, want))
			run(fixture.Exec("RESET ROLE", 0))
			run(replicaMode("ri_t", "i"))
		})
	}
}
