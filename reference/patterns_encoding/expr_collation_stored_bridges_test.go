package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExprCollationStoredPreflightDemand(t *testing.T) {
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		exprCollationTable(t, p, b)
		fixture.CollationExec(t, p, b,
			`CREATE DOMAIN expr_coll_preflight_c AS TEXT COLLATE "C"`,
			`CREATE DOMAIN expr_coll_preflight_posix AS TEXT COLLATE "POSIX"`,
			`CREATE VIEW expr_coll_preflight_payload AS SELECT id,CAST(CASE WHEN ('A'::expr_coll_preflight_c)=('A'::expr_coll_preflight_posix) THEN TRUE ELSE FALSE END AS TEXT) AS unused,TRUE AS selected FROM expr_coll_t ORDER BY id`)
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT id,selected FROM expr_coll_preflight_payload WHERE id=1`, []string{"id", "selected"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}))
		for _, q := range []string{`SELECT unused FROM expr_coll_preflight_payload WHERE id=1`, `SELECT * FROM expr_coll_preflight_payload WHERE id=1`, `SELECT id,unused,selected FROM expr_coll_preflight_payload WHERE id=1`} {
			error := exprCollationDemand(q)
			// Demanded constant comparison fails at execution, including
			// extended-query prepared routes.
			error.ErrorStage = "execute"
			fixture.CollationRoutes(t, p, b, error)
		}
	})
}

func TestReferenceExprCollationNestedPayloadDemand(t *testing.T) {
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		exprCollationTable(t, p, b)
		fixture.CollationExec(t, p, b,
			`CREATE VIEW expr_coll_inner_payload AS SELECT id,a=b AS unused,a COLLATE "C"=b AS selected FROM expr_coll_t WHERE id<=2 ORDER BY id`,
			`CREATE VIEW expr_coll_outer_payload AS SELECT id,unused,selected FROM expr_coll_inner_payload`,
			`CREATE VIEW expr_coll_outer_bound AS SELECT id,unused,selected FROM expr_coll_inner_payload ORDER BY id LIMIT 1`,
			`CREATE VIEW expr_coll_outer_repeated AS SELECT l.id,l.unused,r.selected FROM expr_coll_inner_payload l JOIN expr_coll_inner_payload r ON l.id=r.id`)
		for _, name := range []string{"expr_coll_outer_payload", "expr_coll_outer_bound", "expr_coll_outer_repeated"} {
			fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT id,selected FROM `+name+` WHERE id=1`, []string{"id", "selected"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}))
			fixture.CollationRoutes(t, p, b, exprCollationDemand(`SELECT unused FROM `+name+` WHERE id=1`))
			fixture.CollationRoutes(t, p, b, exprCollationDemand(`SELECT * FROM `+name+` WHERE id=1`))
		}
		fixture.CollationExec(t, p, b, `CREATE VIEW expr_coll_outer_key AS SELECT id,unused,selected FROM expr_coll_inner_payload ORDER BY unused`)
		fixture.CollationRoutes(t, p, b, exprCollationDemand(`SELECT id,selected FROM expr_coll_outer_key`))
		fixture.CollationExec(t, p, b,
			`CREATE SEQUENCE expr_coll_nested_effect`,
			`CREATE VIEW expr_coll_inner_volatile AS SELECT id,nextval('expr_coll_nested_effect') AS unused,a COLLATE "C"=b AS selected FROM expr_coll_t WHERE id<=2 ORDER BY id LIMIT 1`,
			`CREATE VIEW expr_coll_outer_volatile AS SELECT id,unused,selected FROM expr_coll_inner_volatile`)
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT id,selected FROM expr_coll_outer_volatile`, []string{"id", "selected"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}))
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT last_value,is_called FROM expr_coll_nested_effect`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(4), true}}))
	})
}

func TestReferenceExprCollationDomainDerivation(t *testing.T) {
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		exprCollationTable(t, p, b)
		fixture.CollationExec(t, p, b,
			`CREATE DOMAIN expr_domain_c AS TEXT COLLATE "C"`,
			`CREATE DOMAIN expr_domain_posix AS TEXT COLLATE "POSIX"`,
			`CREATE DOMAIN expr_domain_default AS TEXT`)
		exprCollationCases(t, p, b, []fixture.Step{
			fixture.Select(`SELECT ('A' COLLATE "POSIX")::expr_domain_c=b AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{true}}),
			fixture.Select(`SELECT ('A' COLLATE "C")::expr_domain_posix=a AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{true}}),
			fixture.Select(`SELECT (('A' COLLATE "POSIX")::expr_domain_c) COLLATE "POSIX"=b AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{true}}),
			fixture.Select(`SELECT ('A' COLLATE "POSIX")::expr_domain_default=a AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{true}}),
			fixture.Select(`SELECT (a||b)::expr_domain_c=a AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{false}}),
			exprCollationDemand(`SELECT (a||b)::expr_domain_default=a AS v FROM expr_coll_t WHERE id=1`),
			fixture.Select(`SELECT ('A' COLLATE "C")::expr_domain_posix=('A' COLLATE "C") AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{true}}),
			exprCollationExplicit(`SELECT ('A' COLLATE "C")::expr_domain_posix=('A' COLLATE "POSIX") AS v FROM expr_coll_t WHERE id=1`, "C", "POSIX"),
			exprCollationDemand(`SELECT (('A' COLLATE "POSIX")||'')::expr_domain_c=b AS v FROM expr_coll_t WHERE id=1`),
			fixture.Select(`SELECT (('A' COLLATE "POSIX")||'')::expr_domain_c=a AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{true}}),
			exprCollationDemand(`SELECT (CASE WHEN TRUE THEN 'A' COLLATE "POSIX" ELSE '' END)::expr_domain_c=b AS v FROM expr_coll_t WHERE id=1`),
			fixture.Select(`SELECT (('A' COLLATE "POSIX")||'')::expr_domain_default=a AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{true}}),
			fixture.Select(`SELECT (CASE WHEN TRUE THEN 'A' COLLATE "POSIX" ELSE '' END)::expr_domain_default=a AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{true}}),
			fixture.Select(`SELECT ((CASE WHEN TRUE THEN 'A' COLLATE "C" ELSE '' END) COLLATE "POSIX")::expr_domain_c=b AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{true}}),
		})
	})
}
