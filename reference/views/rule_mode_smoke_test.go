package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRuleModeFeatureSmoke(t *testing.T) {
	ruleModeRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		c := ruleModeConn(t, p, b)
		run := transactionalRulesRun(t, p, b, c, nil)
		ruleModeSetup(run)
		oid := transactionalRulesOID(t, p, c, "r")
		q := "INSERT INTO src VALUES($1,$2)"
		h := transactionalRulesHandles(t, p, c, held, q)
		run = transactionalRulesRun(t, p, b, c, h)
		insert := func(id int64) { run(fixture.Exec(q, 1, id, id*10)) }
		insert(1)
		run(fixture.Exec("ALTER TABLE src DISABLE RULE r", 0))
		insert(2)
		run(fixture.Exec("ALTER TABLE src ENABLE REPLICA RULE r", 0))
		insert(3)
		run(fixture.Exec("SET session_replication_role=replica", 0))
		insert(4)
		run(fixture.Exec("ALTER TABLE src ENABLE ALWAYS RULE r", 0))
		insert(5)
		run(fixture.Exec("SET session_replication_role=local", 0))
		insert(6)
		run(fixture.Exec("ALTER TABLE src ENABLE RULE r", 0))
		insert(7)
		run(ruleModeCatalog("O"))
		transactionalRulesSameOID(t, p, c, "r", oid)
		run(ruleModeMissing("ALTER TABLE src DISABLE RULE r, ENABLE RULE missing", "missing", "src"))
		run(ruleModeCatalog("O"))
		insert(8)
		run(fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Exec("ALTER TABLE src DISABLE RULE r", 0))
		run(ruleModeCatalog("D"))
		run(fixture.Exec("ROLLBACK", 0))
		run(ruleModeCatalog("O"))
		insert(9)
		var src, audit [][]any
		for id := int64(1); id <= 9; id++ {
			src = append(src, []any{id, id * 10})
		}
		for _, id := range []int64{1, 4, 5, 6, 7, 8, 9} {
			audit = append(audit, []any{id, id * 10})
		}
		run(ruleModeValueRows("src", src...))
		run(ruleModeValueRows("sink1", audit...))
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(ruleModeCatalog("O"))
			run(ruleModeValueRows("src", src...))
			run(ruleModeValueRows("sink1", audit...))
			insert(10)
			audit = append(audit, []any{int64(10), int64(100)})
			run(ruleModeValueRows("sink1", audit...))
		}
	})
}
