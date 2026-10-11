package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExprCollationRowOperandDemand(t *testing.T) {
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		exprCollationTable(t, p, b)
		exprCollationCases(t, p, b, []fixture.Step{
			fixture.Select(`SELECT (id,a=b)<(id+1,TRUE) AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{true}}),
			fixture.Select(`SELECT (id,a=b)>(id+1,TRUE) AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{false}}),
			fixture.Select(`SELECT (id,TRUE)<(id+1,a=b) AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{true}}),
			fixture.Select(`SELECT (NULL::INT,a=b)<(id,TRUE) AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{nil}}),
			exprCollationDemand(`SELECT (id,a=b)<(id,TRUE) AS v FROM expr_coll_t WHERE id=1`),
			exprCollationDemand(`SELECT (id,TRUE)<(id,a=b) AS v FROM expr_coll_t WHERE id=1`),
		})
		fixture.CollationExec(t, p, b, `CREATE SEQUENCE expr_coll_row_effect`)
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT (id,nextval('expr_coll_row_effect'))<(id+1,0::BIGINT) AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT last_value,is_called FROM expr_coll_row_effect`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
	})
}

func TestReferenceExprCollationScheduledPayloadDemand(t *testing.T) {
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		exprCollationTable(t, p, b)
		for _, c := range []struct{ name, tail string }{
			{"expr_coll_payload_sorted", `ORDER BY id`},
			{"expr_coll_payload_bounded", `LIMIT 1`},
			{"expr_coll_payload_sorted_bound", `ORDER BY id LIMIT 1`},
		} {
			fixture.CollationExec(t, p, b, `CREATE VIEW `+c.name+` AS SELECT id,a=b AS unused,a COLLATE "C"=b AS selected FROM expr_coll_t WHERE id<=2 `+c.tail)
			fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT id,selected FROM `+c.name+` WHERE id=1`, []string{"id", "selected"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}))
			fixture.CollationRoutes(t, p, b, exprCollationDemand(`SELECT unused FROM `+c.name+` WHERE id=1`))
			fixture.CollationRoutes(t, p, b, exprCollationDemand(`SELECT * FROM `+c.name+` WHERE id=1`))
			fixture.CollationRoutes(t, p, b, exprCollationDemand(`SELECT id,unused,selected FROM `+c.name+` WHERE id=1`))
		}
		fixture.CollationExec(t, p, b, `CREATE VIEW expr_coll_payload_key AS SELECT id,a=b AS unused,a COLLATE "C"=b AS selected FROM expr_coll_t WHERE id=1 ORDER BY a=b`)
		fixture.CollationRoutes(t, p, b, exprCollationDemand(`SELECT id,selected FROM expr_coll_payload_key`))
		fixture.CollationExec(t, p, b, `CREATE SEQUENCE expr_coll_payload_effect`, `CREATE VIEW expr_coll_payload_volatile AS SELECT id,nextval('expr_coll_payload_effect') AS unused,a COLLATE "C"=b AS selected FROM expr_coll_t WHERE id<=2 ORDER BY id LIMIT 1`)
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT id,selected FROM expr_coll_payload_volatile`, []string{"id", "selected"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}))
		// One retained volatile output executes per route after bounding.
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT last_value,is_called FROM expr_coll_payload_effect`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(4), true}}))
		fixture.CollationExec(t, p, b, `CREATE MATERIALIZED VIEW expr_coll_payload_refresh AS SELECT id,a=b AS unused,a COLLATE "C"=b AS selected FROM expr_coll_t WHERE id=1 WITH NO DATA`)
		refresh := exprCollationDemand(`REFRESH MATERIALIZED VIEW expr_coll_payload_refresh`)
		refresh.SelectRows = false
		fixture.CollationRoutes(t, p, b, refresh)
	})
}
