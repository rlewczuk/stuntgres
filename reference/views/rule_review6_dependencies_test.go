package reference_test

import (
	"bytes"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func ruleReview6DependencyRoutes(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, bool, bool)) {
	t.Helper()
	for _, modifier := range []bool{false, true} {
		t.Run(fmt.Sprintf("modifier=%v", modifier), func(t *testing.T) {
			for _, held := range []bool{false, true} {
				t.Run(fmt.Sprintf("held=%v", held), func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) { body(t, p, b, held, modifier) })
					}
				})
			}
		})
	}
}

func ruleReview6DependencySetup(t *testing.T, p *fixture.Pair, b fixture.Backend, modifier bool) {
	t.Helper()
	ruleRun(t, p, b, false,
		fixture.Exec("CREATE TABLE src(id INT)", 0),
		fixture.Exec("CREATE TABLE sink(id INT CONSTRAINT sink_key UNIQUE)", 0),
		fixture.Exec("CREATE TABLE log(id INT)", 0),
		fixture.Exec("CREATE TABLE unrelated(id INT)", 0),
		fixture.Exec("INSERT INTO unrelated VALUES(99)", 1),
		fixture.Exec("CREATE RULE keep_r AS ON DELETE TO src DO ALSO NOTHING", 0))
	action := "INSERT INTO sink VALUES(NEW.id) ON CONFLICT ON CONSTRAINT sink_key DO NOTHING"
	if modifier {
		// The modifying WITH body has no OLD/NEW reference. Only its parent
		// action consumes NEW, preserving PostgreSQL's lexical restriction.
		action = "WITH moved AS (INSERT INTO sink VALUES(7) ON CONFLICT ON CONSTRAINT sink_key DO NOTHING RETURNING id) INSERT INTO log SELECT NEW.id FROM moved"
	}
	ruleRun(t, p, b, false, fixture.Exec("CREATE RULE r AS ON INSERT TO src DO ALSO "+action, 0))
}

func TestReferenceRuleReview6NamedDependency(t *testing.T) {
	ruleReview6DependencyRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held, modifier bool) {
		ruleReview6DependencySetup(t, p, b, modifier)
		var constraintOID, indexOID int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint,conindid::bigint FROM pg_constraint WHERE conrelid='sink'::regclass::oid AND conname='sink_key'").Scan(&constraintOID, &indexOID); err != nil {
			t.Fatal(err)
		}
		if constraintOID <= 0 || indexOID <= 0 {
			t.Fatalf("invalid constraint/backing identities: %d/%d", constraintOID, indexOID)
		}
		// Generated OIDs are relational expectations, never allocator goldens.
		// This is the entire constraint-provider bag owned by this actual rule.
		ruleRun(t, p, b, held,
			fixture.Select("SELECT refclassid::bigint,refobjid::bigint,refobjsubid,deptype FROM pg_depend WHERE classid=2618::oid AND objid=(SELECT oid FROM pg_rewrite WHERE ev_class='src'::regclass::oid AND rulename='r') AND refclassid=2606::oid ORDER BY refobjid,refobjsubid,deptype", []string{"refclassid", "refobjid", "refobjsubid", "deptype"}, []string{"INT8", "INT8", "INT4", "CHAR"}, [][]any{{int64(2606), constraintOID, int64(0), "n"}}),
			fixture.Select("SELECT count(*) AS edges FROM pg_depend WHERE classid=2618::oid AND objid=(SELECT oid FROM pg_rewrite WHERE ev_class='src'::regclass::oid AND rulename='r') AND refclassid=1259::oid AND refobjid=$1::oid AND deptype='n'", []string{"edges"}, []string{"INT8"}, [][]any{{int64(0)}}, indexOID))
	})
}

func TestReferenceRuleReview6NamedRetirement(t *testing.T) {
	ruleReview6DependencyRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held, modifier bool) {
		ruleReview6DependencySetup(t, p, b, modifier)
		run := func(steps ...fixture.Step) { ruleRun(t, p, b, false, steps...) }
		var notices []fixture.Diagnostic
		_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		const restrict = "ALTER TABLE sink DROP CONSTRAINT sink_key RESTRICT"
		const cascade = "ALTER TABLE sink DROP CONSTRAINT sink_key CASCADE"
		const insert = "INSERT INTO src VALUES($1)"
		dropExecutor, closeDrop := ruleReview2ConflictHeld(t, p.Ctx, conn, restrict, held)
		defer closeDrop()
		cascadeExecutor, closeCascade := ruleReview2ConflictHeld(t, p.Ctx, conn, cascade, held)
		defer closeCascade()
		insertExecutor, closeInsert := ruleReview2ConflictHeld(t, p.Ctx, conn, insert, held)
		defer closeInsert()
		deny := func() {
			// Full candidate English diagnostics/empty remaining fields are
			// independently inferred, not observed, until the root's oracle run.
			s := fixture.ImplicitError(restrict, fixture.Diagnostic{Code: "2BP01", Message: "cannot drop constraint sink_key on table sink because other objects depend on it", Detail: "rule r on table src depends on constraint sink_key on table sink", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, dropExecutor, recorder, s)
		}
		fire := func(id int) { fixture.RunStep(t, p.Ctx, insertExecutor, recorder, fixture.Exec(insert, 1, id)) }
		state := func(retired bool, src, sink, log [][]any) {
			rules := [][]any{{"keep_r"}, {"r"}}
			constraints := [][]any{{"sink_key", "u"}}
			if retired {
				rules = [][]any{{"keep_r"}}
				constraints = nil
			}
			run(fixture.Select("SELECT rulename FROM pg_rewrite WHERE ev_class='src'::regclass::oid ORDER BY rulename", []string{"rulename"}, []string{"NAME"}, rules),
				fixture.Select("SELECT conname,contype FROM pg_constraint WHERE conrelid='sink'::regclass::oid ORDER BY conname", []string{"conname", "contype"}, []string{"NAME", "CHAR"}, constraints),
				fixture.Select("SELECT id FROM src ORDER BY id", []string{"id"}, []string{"INT4"}, src),
				fixture.Select("SELECT id FROM sink ORDER BY id", []string{"id"}, []string{"INT4"}, sink),
				fixture.Select("SELECT id FROM log ORDER BY id", []string{"id"}, []string{"INT4"}, log),
				fixture.Select("SELECT id FROM unrelated", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
		}
		fire(1)
		fire(1)
		sink := [][]any{{int64(1)}}
		var log [][]any
		if modifier {
			sink = [][]any{{int64(7)}}
			log = [][]any{{int64(1)}}
		}
		src := [][]any{{int64(1)}, {int64(1)}}
		state(false, src, sink, log)
		deny()
		fixture.RegtypeAssertNotices(t, notices, nil)
		state(false, src, sink, log)
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			state(false, src, sink, log)
		}
		// The SAME DROP and source INSERT remain held through RESTRICT and
		// native replacement. Clearing sink makes the modifier's future action
		// observable too, rather than only exercising a conflict no-op.
		deny()
		run(fixture.Exec("DELETE FROM sink", 1))
		fire(2)
		src = append(src, []any{int64(2)})
		if modifier {
			log = append(log, []any{int64(2)})
		} else {
			sink = [][]any{{int64(2)}}
		}
		state(false, src, sink, log)
		fixture.RunStep(t, p.Ctx, cascadeExecutor, recorder, fixture.Exec(cascade, 0))
		fixture.RegtypeAssertNotices(t, notices, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to rule r on table src"}})
		state(true, src, sink, log)
		fire(3)
		src = append(src, []any{int64(3)})
		state(true, src, sink, log)
		// The target table survives too, but its unique provider is retired.
		run(fixture.Exec("INSERT INTO sink VALUES(9),(9)", 2))
		sink = append(sink, []any{int64(9)}, []any{int64(9)})
		state(true, src, sink, log)
	})
}
