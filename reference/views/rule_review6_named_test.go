package reference_test

import (
	"bytes"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func ruleReview6NamedRoutes(t *testing.T, reuse bool) {
	t.Helper()
	for _, update := range []bool{false, true} {
		for _, cte := range []bool{false, true} {
			for _, held := range []bool{false, true} {
				t.Run(fmt.Sprintf("update=%v/cte=%v/held=%v", update, cte, held), func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							ruleReview6NamedRoundTrip(t, p, b, update, cte, held, reuse)
						})
					}
				})
			}
		}
	}
}

func TestReferenceRuleReview6NamedRenameRoundTrip(t *testing.T) {
	ruleReview6NamedRoutes(t, false)
}

func TestReferenceRuleReview6NamedNameReuseRoundTrip(t *testing.T) {
	ruleReview6NamedRoutes(t, true)
}

func ruleReview6NamedRoundTrip(t *testing.T, p *fixture.Pair, b fixture.Backend, update, cte, held, reuse bool) {
	t.Helper()
	var notices []fixture.Diagnostic
	_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
	run := func(steps ...fixture.Step) {
		for _, step := range steps {
			fixture.RunStep(t, p.Ctx, conn, recorder, step)
		}
	}
	run(fixture.Exec("CREATE TABLE src(id INT, other INT)", 0),
		fixture.Exec("CREATE TABLE sink(id INT CONSTRAINT sink_key UNIQUE, other INT CONSTRAINT other_key UNIQUE)", 0),
		fixture.Exec("CREATE TABLE input(id INT, other INT)", 0),
		fixture.Exec("CREATE TABLE log(id INT)", 0),
		fixture.Exec("CREATE TABLE unrelated(id INT)", 0),
		fixture.Exec("INSERT INTO unrelated VALUES(99)", 1),
		fixture.Exec("INSERT INTO input VALUES(1,10)", 1))
	conflict := " ON CONFLICT ON CONSTRAINT sink_key DO NOTHING"
	if update {
		conflict = " ON CONFLICT ON CONSTRAINT sink_key DO UPDATE SET other=EXCLUDED.other"
	}
	action := "INSERT INTO sink VALUES(NEW.id,NEW.other)" + conflict
	if cte {
		// The modifier itself is legally OLD/NEW-free. Its SELECT source and
		// RETURNING spool remain independent of the outer NEW consumer.
		action = "WITH moved AS (INSERT INTO sink SELECT id,other FROM input" + conflict + " RETURNING id) INSERT INTO log SELECT NEW.id FROM moved"
	}
	run(fixture.Exec("CREATE RULE r AS ON INSERT TO src DO ALSO "+action, 0))
	const insert = "INSERT INTO src VALUES($1,$2)"
	x, closeStmt := ruleReview2ConflictHeld(t, p.Ctx, conn, insert, held)
	defer closeStmt()
	fire := func(id, other int) {
		if cte {
			run(fixture.Exec("UPDATE input SET id=$1, other=$2", 1, id, other))
		}
		fixture.RunStep(t, p.Ctx, x, recorder, fixture.Exec(insert, 1, id, other))
	}
	state := func(src, sink, log, input [][]any, renamed bool) {
		constraints := [][]any{{"other_key"}, {"sink_key"}}
		if renamed {
			constraints = [][]any{{"other_key"}, {"renamed_key"}}
			if reuse {
				constraints = [][]any{{"renamed_key"}, {"sink_key"}}
			}
		}
		logRows := fixture.Select("SELECT id FROM log", []string{"id"}, []string{"INT4"}, log)
		logRows.Bag = true
		run(fixture.Select("SELECT id,other FROM src ORDER BY other", []string{"id", "other"}, []string{"INT4", "INT4"}, src),
			fixture.Select("SELECT id,other FROM sink ORDER BY id", []string{"id", "other"}, []string{"INT4", "INT4"}, sink),
			logRows,
			fixture.Select("SELECT id,other FROM input", []string{"id", "other"}, []string{"INT4", "INT4"}, input),
			fixture.Select("SELECT id FROM unrelated", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}),
			fixture.Select("SELECT rulename FROM pg_rewrite WHERE ev_class='src'::regclass::oid ORDER BY rulename", []string{"rulename"}, []string{"NAME"}, [][]any{{"r"}}),
			fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='sink'::regclass::oid ORDER BY conname", []string{"conname"}, []string{"NAME"}, constraints))
		fixture.RegtypeAssertNotices(t, notices, nil)
	}
	var src, log [][]any
	sink := [][]any{{int64(1), int64(10)}}
	input := [][]any{{int64(1), int64(10)}}
	observeFire := func(id, other int, renamed bool) {
		fire(id, other)
		src = append(src, []any{int64(id), int64(other)})
		// Independent expected contract: id=1 duplicates suppress or update;
		// id=2 is a new key. Counts never include ALSO/modifier side effects.
		if id == 2 {
			sink = append(sink, []any{int64(2), int64(other)})
		} else if update {
			sink[0] = []any{int64(1), int64(other)}
		}
		if cte {
			input = [][]any{{int64(id), int64(other)}}
			if update || other == 10 || id == 2 {
				log = append(log, []any{int64(id)})
			}
		}
		state(src, sink, log, input, renamed)
	}
	observeFire(1, 10, false)
	observeFire(1, 20, false)
	run(fixture.Exec("ALTER TABLE sink RENAME CONSTRAINT sink_key TO renamed_key", 0))
	if reuse {
		// Reuse only existing RENAME capability: the old spelling now denotes
		// the DIFFERENT unique provider on other, not a replacement id key.
		run(fixture.Exec("ALTER TABLE sink RENAME CONSTRAINT other_key TO sink_key", 0))
	}
	observeFire(1, 30, true)
	if !b.PG {
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
	}
	// Exact preservation after actual native replay, before any future write.
	state(src, sink, log, input, true)
	// SAME held consumer survives rename and Restore. Distinct other values
	// make obsolete-name retargeting observable: the id duplicate must still
	// suppress/update, never hit an unhandled renamed_key unique violation.
	observeFire(1, 40, true)
	observeFire(2, 50, true)
	observeFire(1, 60, true)
}
