package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRuleModeCatalog(t *testing.T) {
	ruleModeRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		c := ruleModeConn(t, p, b)
		run := transactionalRulesRun(t, p, b, c, nil)
		ruleModeSetup(run)
		oid := transactionalRulesOID(t, p, c, "r")
		// Definitions and dependency bags must remain exact across mode-only ALTER.
		snapshot := func() []string {
			rows, err := c.QueryContext(p.Ctx, "SELECT definition FROM pg_rules WHERE schemaname='public' AND tablename='src' AND rulename='r' UNION ALL SELECT d.refclassid::TEXT||':'||d.refobjid::TEXT||':'||d.refobjsubid::TEXT||':'||d.deptype::TEXT FROM pg_depend d JOIN pg_rewrite r ON r.oid=d.objid WHERE d.classid='pg_rewrite'::regclass AND r.ev_class='src'::regclass AND r.rulename='r' ORDER BY 1")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var values []string
			for rows.Next() {
				var s string
				if err := rows.Scan(&s); err != nil {
					t.Fatal(err)
				}
				values = append(values, s)
			}
			if rows.Err() != nil {
				t.Fatal(rows.Err())
			}
			return values
		}
		before := snapshot()
		for _, mode := range append(ruleModeCases[1:], ruleModeCases[0]) {
			ruleRun(t, p, b, held, fixture.Exec("ALTER TABLE src "+mode.action+" r", 0))
			run(ruleModeCatalog(mode.code))
			transactionalRulesSameOID(t, p, c, "r", oid)
			if after := snapshot(); !reflect.DeepEqual(before, after) {
				t.Fatalf("mode ALTER changed definition/dependency bag: before=%q after=%q", before, after)
			}
			run(fixture.Select("SELECT relhasrules FROM pg_class WHERE oid='src'::regclass", []string{"relhasrules"}, []string{"BOOL"}, [][]any{{true}}))
		}
	})
	for _, mode := range ruleModeCases[1:] {
		t.Run(mode.code, func(t *testing.T) {
			ruleModeRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				c := ruleModeConn(t, p, b)
				run := transactionalRulesRun(t, p, b, c, nil)
				ruleModeSetup(run)
				run(fixture.Exec("ALTER TABLE src "+mode.action+" r", 0))
				oid := transactionalRulesOID(t, p, c, "r")
				ruleRun(t, p, b, held, fixture.Exec("CREATE OR REPLACE RULE r AS ON INSERT TO src DO ALSO INSERT INTO sink2 VALUES(NEW.id,NEW.v+1)", 0))
				run(ruleModeCatalog(mode.code))
				transactionalRulesSameOID(t, p, c, "r", oid)
				run(transactionalRulesEdge("sink1", 0))
				run(transactionalRulesEdge("sink2", 1))
				run(fixture.Exec("INSERT INTO src VALUES(1,10)", 1))
				var want [][]any
				if ruleModeActive(mode.code, "origin") {
					want = [][]any{{int64(1), int64(11)}}
				}
				run(ruleModeValueRows("sink2", want...))
				run(fixture.Exec("ALTER RULE r ON src RENAME TO renamed", 0))
				transactionalRulesSameOID(t, p, c, "renamed", oid)
				run(fixture.Exec("ALTER TABLE src RENAME TO renamed_src", 0))
				run(fixture.Select("SELECT oid::BIGINT,ev_enabled::TEXT FROM pg_rewrite WHERE ev_class='renamed_src'::regclass AND rulename='renamed'", []string{"oid", "ev_enabled"}, []string{"INT8", "TEXT"}, [][]any{{oid, mode.code}}))
				run(fixture.Exec("ALTER TABLE renamed_src DISABLE RULE renamed", 0))
				run(fixture.ImplicitError("DROP TABLE sink2 RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop table sink2 because other objects depend on it", Detail: "rule renamed on table renamed_src depends on table sink2", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
				run(fixture.Exec("DROP TABLE sink2 CASCADE", 0))
				run(ruleModeCount("pg_rewrite WHERE ev_class='renamed_src'::regclass", 0))
				run(fixture.Exec("INSERT INTO renamed_src VALUES(2,20)", 1))
				run(ruleModeValueRows("renamed_src", []any{int64(1), int64(10)}, []any{int64(2), int64(20)}))
			})
		})
	}
}
