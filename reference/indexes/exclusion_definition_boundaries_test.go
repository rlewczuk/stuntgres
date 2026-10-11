package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceExclusionExpressionDiagnostics(t *testing.T) {
	for _, c := range []struct{ key, predicate, code, message, token string }{
		{"lower(missing)", "", "42703", `column "missing" does not exist`, "missing"},
		{"(count(*))", "", "42803", "aggregate functions are not allowed in index expressions", "count"},
		{"((SELECT v FROM t))", "", "0A000", "cannot use subquery in index expression", "(SELECT"},
		{"(row_number() OVER ())", "", "42P20", "window functions are not allowed in index expressions", "row_number"},
		{"v", " WHERE((SELECT TRUE))", "0A000", "cannot use subquery in index predicate", "(SELECT"},
		{"v", " WHERE(sum(v)>0)", "42803", "aggregate functions are not allowed in index predicates", "sum"},
		{"v", " WHERE(v)", "42804", "argument of WHERE must be type boolean, not type integer", "v)"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s%s/%t", c.key, c.predicate, prepared), func(t *testing.T) {
				fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, b.DB, s) }
					run(fixture.Exec("CREATE TABLE t(v INT,note TEXT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1,'unchanged')", 1))
					q := "/*é*/ ALTER TABLE t ADD CONSTRAINT ex EXCLUDE(" + c.key + " WITH =)" + c.predicate
					d := fixture.ControlsReferenceDiagnostic(c.code, c.message)
					d.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.LastIndex(q, c.token)]) + 1)
					s := fixture.ErrorDiagnostic(q, d)
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
					run(fixture.Select("SELECT v,note FROM t", []string{"v", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "unchanged"}}))
					run(fixture.Select("SELECT conname FROM pg_constraint WHERE conname='ex'", []string{"conname"}, []string{"NAME"}, nil))
				})
			})
		}
	}
}

func TestReferenceExclusionCatalogDefinitions(t *testing.T) {
	fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, b.DB, s) }
		run(fixture.Exec("CREATE TABLE t(v INT,note TEXT,active BOOL,CONSTRAINT ex EXCLUDE USING btree(v DESC WITH =,lower(note) WITH =) INCLUDE(active) WHERE(active) DEFERRABLE INITIALLY DEFERRED)", 0))
		run(fixture.Select("SELECT conname,contype,pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conname='ex'", []string{"conname", "contype", "definition"}, []string{"NAME", "CHAR", "TEXT"}, [][]any{{"ex", "x", "EXCLUDE USING btree (v DESC WITH =, lower(note) WITH =) INCLUDE (active) WHERE (active) DEFERRABLE INITIALLY DEFERRED"}}))
		if b.PG {
			run(fixture.Select("SELECT condeferrable,condeferred FROM pg_constraint WHERE conname='ex'", []string{"condeferrable", "condeferred"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		}
		run(fixture.Exec("INSERT INTO t VALUES(1,'A',TRUE),(1,'b',TRUE),(1,'a',FALSE)", 3))
		run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,'a',TRUE)", fixture.ExclusionReferenceConflict("t", "ex", "v, lower(note)", "1, a", "1, a")))
		// Only bounded existing catalog columns are shared. Physical pg_index
		// facts and page/WAL/pg_dump guarantees are not an invented native API.
		run(fixture.Exec("CREATE TABLE generated(v INT,EXCLUDE(v WITH =))", 0))
		run(fixture.Exec("INSERT INTO generated VALUES(1)", 1))
		run(fixture.ErrorDiagnostic("INSERT INTO generated VALUES(1)", fixture.ExclusionReferenceConflict("generated", "generated_v_excl", "v", "1", "1")))
	})
}

func TestReferenceExclusionQuotedNames(t *testing.T) {
	fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, b.DB, s) }
		run(fixture.Exec(`CREATE TABLE "té"("vé" INT,CONSTRAINT "é""x" EXCLUDE("vé" WITH =))`, 0))
		run(fixture.Exec(`INSERT INTO "té" VALUES(1)`, 1))
		run(fixture.Prepared(fixture.ErrorDiagnostic(`INSERT INTO "té" VALUES(1)`, fixture.ExclusionReferenceConflict("té", `é"x`, `"vé"`, "1", "1"))))
		run(fixture.Exec(`ALTER TABLE "té" RENAME CONSTRAINT "é""x" TO "néw"`, 0))
		run(fixture.ErrorDiagnostic(`INSERT INTO "té" VALUES(1)`, fixture.ExclusionReferenceConflict("té", "néw", `"vé"`, "1", "1")))
	})
}

func TestReferenceExclusionStandaloneHashBoundary(t *testing.T) {
	fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, b.DB, s) }
		run(fixture.Exec("CREATE TABLE t(v INT,active BOOL)", 0))
		q := "CREATE INDEX standalone_hash ON t USING hash(v) WHERE(active)"
		if b.PG {
			run(fixture.Exec(q, 0))
		} else {
			run(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("0A000", "partial indexes are unsupported")))
		}
		run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT ex EXCLUDE USING hash(v WITH =) WHERE(active)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,TRUE),(1,FALSE),(1,NULL)", 3))
		run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,TRUE)", fixture.ExclusionReferenceConflict("t", "ex", "v", "1", "1")))
	})
}

func TestReferenceExclusionDisclosure(t *testing.T) {
	for _, disclose := range []bool{false, true} {
		t.Run(fmt.Sprintf("select=%t", disclose), func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"exclusion_reader"})
			fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				if !b.PG {
					return
				} // SQL GRANT is not native's public ACL API.
				c := fixture.ConcurrentIndexConn(t, p, b)
				run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, c, s) }
				run(fixture.Exec("CREATE ROLE exclusion_reader", 0))
				run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO exclusion_reader", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION exclusion_reader", 0))
				defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				run(fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =))", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
				run(fixture.Exec("REVOKE SELECT ON t FROM exclusion_reader", 0))
				if disclose {
					run(fixture.Exec("GRANT SELECT(v) ON t TO exclusion_reader", 0))
				}
				d := fixture.ExclusionReferenceConflict("t", "ex", "v", "1", "1")
				if !disclose {
					d.Detail = "Key conflicts with existing key."
				}
				run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1)", d)))
				run(fixture.Exec("CREATE TABLE build(v INT)", 0))
				run(fixture.Exec("INSERT INTO build VALUES(1),(1)", 2))
				run(fixture.Exec("REVOKE SELECT ON build FROM exclusion_reader", 0))
				if disclose {
					run(fixture.Exec("GRANT SELECT(v) ON build TO exclusion_reader", 0))
				}
				d = fixture.ExclusionReferenceBuild("build", "bx", "v", "1")
				if !disclose {
					d.Detail = "Key conflicts exist."
				}
				run(fixture.ErrorDiagnostic("ALTER TABLE build ADD CONSTRAINT bx EXCLUDE(v WITH =)", d))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.ExclusionReferenceRows("SELECT v FROM t", 1))
				run(fixture.ExclusionReferenceRows("SELECT v FROM build ORDER BY v", 1, 1))
			})
		})
	}
}
