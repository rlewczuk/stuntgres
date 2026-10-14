package reference_test

import (
	"bytes"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func ruleReview4InferenceRoutes(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, bool)) {
	t.Helper()
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("held=%v", held), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) { body(t, p, b, held) })
			}
		})
	}
}

func ruleReview4InferenceFailure(q string, args ...any) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{
		Code: "42P10", Message: "there is no unique or exclusion constraint matching the ON CONFLICT specification",
	}, args...)
	s.ErrorStage = "execute"
	return s
}

func ruleReview4InferenceSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, cte bool, decoration, indexDecoration string) {
	t.Helper()
	run := func(s ...fixture.Step) { ruleRun(t, p, b, false, s...) }
	run(fixture.Exec("CREATE TABLE src(v TEXT)", 0), fixture.Exec("CREATE TABLE sink(v TEXT)", 0),
		fixture.Exec("CREATE TABLE log(v TEXT)", 0), fixture.Exec("CREATE TABLE input(v TEXT)", 0),
		fixture.Exec("INSERT INTO input VALUES('x')", 1),
		fixture.Exec("CREATE UNIQUE INDEX sink_u ON sink(v"+indexDecoration+")", 0))
	action := "INSERT INTO sink VALUES(NEW.v) ON CONFLICT(v" + decoration + ") DO NOTHING"
	if cte {
		// The action-owned modifier has no OLD/NEW; only its consumer uses NEW.
		action = "WITH moved AS (INSERT INTO sink SELECT v FROM input ON CONFLICT(v" + decoration + ") DO NOTHING RETURNING v) INSERT INTO log SELECT NEW.v FROM moved"
	}
	run(fixture.Exec("CREATE RULE r AS ON INSERT TO src DO "+action, 0))
}

func ruleReview4InferenceBags(t *testing.T, p *fixture.Pair, b fixture.Backend, cte bool, column string, src, sink [][]any) {
	t.Helper()
	var log [][]any
	if cte {
		log = sink
	}
	ruleRun(t, p, b, false,
		fixture.Select("SELECT v FROM src ORDER BY v", []string{"v"}, []string{"TEXT"}, src),
		fixture.Select("SELECT "+column+" FROM sink ORDER BY "+column, []string{column}, []string{"TEXT"}, sink),
		fixture.Select("SELECT v FROM log ORDER BY v", []string{"v"}, []string{"TEXT"}, log),
		fixture.Select("SELECT v FROM input", []string{"v"}, []string{"TEXT"}, [][]any{{"x"}}))
}

func ruleReview4InferenceRestore(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	if b.PG {
		return
	}
	// A real public native-control phase, not a substitute PostgreSQL replay.
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceRuleReview4InferenceMatching(t *testing.T) {
	for _, decoration := range []struct{ name, matching, mismatching string }{
		{"class", " pg_catalog.text_pattern_ops", " pg_catalog.text_ops"},
		{"collation", ` COLLATE pg_catalog."C"`, ` COLLATE pg_catalog."default"`},
		{"combined", ` COLLATE pg_catalog."C" pg_catalog.text_pattern_ops`, ` COLLATE pg_catalog."default" pg_catalog.text_ops`},
	} {
		t.Run(decoration.name, func(t *testing.T) {
			for _, cte := range []bool{false, true} {
				t.Run(fmt.Sprintf("cte=%v", cte), func(t *testing.T) {
					ruleReview4InferenceRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
						run := func(s ...fixture.Step) { ruleRun(t, p, b, false, s...) }
						ruleReview4InferenceSetup(t, p, b, cte, decoration.matching, decoration.matching)
						conn, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						q := "INSERT INTO src VALUES($1)"
						x, closeStmt := ruleReview2ConflictHeld(t, p.Ctx, conn, q, held)
						defer closeStmt()
						fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 1, "x"))
						fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 1, "x"))
						ruleReview4InferenceBags(t, p, b, cte, "v", [][]any{{"x"}, {"x"}}, [][]any{{"x"}})
						run(fixture.Exec("ALTER TABLE sink RENAME COLUMN v TO key", 0))
						ruleReview4InferenceRestore(t, p, b)
						ruleReview4InferenceBags(t, p, b, cte, "key", [][]any{{"x"}, {"x"}}, [][]any{{"x"}})
						fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 1, "x"))
						// Initially successful restored definitions must retain restrictions on FUTURE indexes.
						run(fixture.Exec("DROP INDEX sink_u", 0), fixture.Exec("CREATE UNIQUE INDEX sink_u ON sink(key"+decoration.mismatching+")", 0))
						fixture.RunStep(t, p.Ctx, x, b.Recorder, ruleReview4InferenceFailure(q, "y"))
						ruleReview4InferenceBags(t, p, b, cte, "key", [][]any{{"x"}, {"x"}, {"x"}}, [][]any{{"x"}})
						run(fixture.Exec("DROP INDEX sink_u", 0), fixture.Exec("CREATE UNIQUE INDEX sink_u ON sink(key"+decoration.matching+")", 0))
						fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 1, "x"))
						ruleReview4InferenceBags(t, p, b, cte, "key", [][]any{{"x"}, {"x"}, {"x"}, {"x"}}, [][]any{{"x"}})
						// A distinct future row proves restored target/RETURNING names, not just duplicate suppression.
						if cte {
							run(fixture.Exec("UPDATE input SET v='z'", 1))
						}
						fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 1, "z"))
						var log [][]any
						input := [][]any{{"x"}}
						if cte {
							log, input = [][]any{{"x"}, {"z"}}, [][]any{{"z"}}
						}
						run(fixture.Select("SELECT v FROM src ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"x"}, {"x"}, {"x"}, {"x"}, {"z"}}),
							fixture.Select("SELECT key FROM sink ORDER BY key", []string{"key"}, []string{"TEXT"}, [][]any{{"x"}, {"z"}}),
							fixture.Select("SELECT v FROM log ORDER BY v", []string{"v"}, []string{"TEXT"}, log),
							fixture.Select("SELECT v FROM input", []string{"v"}, []string{"TEXT"}, input))
					})
				})
			}
		})
	}
}

func TestReferenceRuleReview4InferenceMismatch(t *testing.T) {
	for _, decoration := range []struct{ name, matching, mismatching string }{
		// Exact review witness: pattern family cannot infer an ordinary text_ops index.
		{"class", " text_pattern_ops", ""},
		// C and default have identical ordering in this fixture, but DISTINCT identities.
		{"collation", ` COLLATE "C"`, ""},
	} {
		t.Run(decoration.name, func(t *testing.T) {
			for _, cte := range []bool{false, true} {
				t.Run(fmt.Sprintf("cte=%v", cte), func(t *testing.T) {
					ruleReview4InferenceRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
						run := func(s ...fixture.Step) { ruleRun(t, p, b, false, s...) }
						ruleReview4InferenceSetup(t, p, b, cte, decoration.matching, decoration.mismatching)
						conn, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						q := "INSERT INTO src VALUES($1)"
						x, closeStmt := ruleReview2ConflictHeld(t, p.Ctx, conn, q, held)
						defer closeStmt()
						fixture.RunStep(t, p.Ctx, x, b.Recorder, ruleReview4InferenceFailure(q, "x"))
						fixture.RunStep(t, p.Ctx, x, b.Recorder, ruleReview4InferenceFailure(q, "y"))
						ruleReview4InferenceBags(t, p, b, cte, "v", nil, nil)
						run(fixture.Exec("ALTER TABLE sink RENAME COLUMN v TO key", 0))
						ruleReview4InferenceRestore(t, p, b)
						ruleReview4InferenceBags(t, p, b, cte, "key", nil, nil)
						fixture.RunStep(t, p.Ctx, x, b.Recorder, ruleReview4InferenceFailure(q, "x"))
						fixture.RunStep(t, p.Ctx, x, b.Recorder, ruleReview4InferenceFailure(q, "y"))
						ruleReview4InferenceBags(t, p, b, cte, "key", nil, nil)
						// Recover with a genuinely matching provider, reusing the SAME consumer.
						run(fixture.Exec("DROP INDEX sink_u", 0), fixture.Exec("CREATE UNIQUE INDEX sink_u ON sink(key"+decoration.matching+")", 0))
						fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 1, "x"))
						fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 1, "x"))
						ruleReview4InferenceBags(t, p, b, cte, "key", [][]any{{"x"}, {"x"}}, [][]any{{"x"}})
					})
				})
			}
		})
	}
}
