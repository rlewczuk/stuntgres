package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRuleModeDiagnostics(t *testing.T) {
	ruleModeRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		c := ruleModeConn(t, p, b)
		run := transactionalRulesRun(t, p, b, c, nil)
		ruleModeSetup(run)
		run(fixture.Exec("INSERT INTO src VALUES(1,10)", 1))
		run(fixture.Exec("CREATE VIEW v AS SELECT id,v FROM src", 0))
		run(fixture.Exec("CREATE MATERIALIZED VIEW mv AS SELECT id,v FROM src", 1))
		oid := transactionalRulesOID(t, p, c, "r")
		type viewIdentity struct {
			oid        int64
			definition string
		}
		views := make(map[string]viewIdentity)
		for _, name := range []string{"v", "mv"} {
			var identity viewIdentity
			if err := c.QueryRowContext(p.Ctx, "SELECT oid::BIGINT,pg_get_viewdef(ev_class) FROM pg_rewrite WHERE ev_class=$1::regclass AND rulename='_RETURN'", name).Scan(&identity.oid, &identity.definition); err != nil {
				t.Fatal(err)
			}
			views[name] = identity
		}
		for _, mode := range ruleModeCases {
			for _, name := range []string{"v", "mv"} {
				q := "ALTER TABLE " + name + " " + mode.action + " \"_RETURN\""
				kind := "view"
				if name == "mv" {
					kind = "materialized view"
				}
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42809", Message: "ALTER action " + mode.action + " cannot be performed on relation \"" + name + "\"", Detail: "This operation is not supported for " + kind + "s."})
				s.ErrorStage = "execute"
				ruleRun(t, p, b, held, s)
				run(ruleModeCatalog("O"))
				identity := views[name]
				run(fixture.Select("SELECT oid::BIGINT,ev_enabled::TEXT,pg_get_viewdef(ev_class) FROM pg_rewrite WHERE ev_class='"+name+"'::regclass AND rulename='_RETURN'", []string{"oid", "ev_enabled", "pg_get_viewdef"}, []string{"INT8", "TEXT", "TEXT"}, [][]any{{identity.oid, "O", identity.definition}}))
				run(ruleModeValueRows(name, []any{int64(1), int64(10)}))
			}
		}
		for _, role := range []string{"origin", "local", "replica"} {
			run(fixture.Exec("SET session_replication_role="+role, 0))
			transactionalViewsExec(t, p, b, c, held, ruleModeValueRows("v", []any{int64(1), int64(10)}))
		}
		run(fixture.Exec("RESET session_replication_role", 0))
		for _, s := range []fixture.Step{
			ruleModeMissing("ALTER TABLE src DISABLE RULE missing", "missing", "src"),
			ruleError("ALTER TABLE absent ENABLE RULE r", "42P01", "relation \"absent\" does not exist"),
		} {
			s.ErrorStage = "execute"
			ruleRun(t, p, b, held, s)
			run(ruleModeCatalog("O"))
			transactionalRulesSameOID(t, p, c, "r", oid)
			run(ruleModeValueRows("src", []any{int64(1), int64(10)}))
			run(ruleModeValueRows("sink1", []any{int64(1), int64(10)}))
		}
	})
	t.Run("if-exists-notice", func(t *testing.T) {
		ruleModeRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
			var notices []fixture.Diagnostic
			_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			q := "ALTER TABLE IF EXISTS absent DISABLE RULE missing"
			run := func(s fixture.Step) { s.Prepared = held; fixture.RunStep(t, p.Ctx, c, rec, s) }
			run(fixture.Exec(q, 0))
			want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "relation \"absent\" does not exist, skipping"}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("notices=%+v want=%+v", notices, want)
			}
			run(fixture.Exec("CREATE TABLE src(id INT)", 0))
			run(ruleModeMissing("ALTER TABLE IF EXISTS src DISABLE RULE missing", "missing", "src"))
		})
	})
	for _, tc := range []struct{ q, token string }{
		{"ALTER TABLE src DISABLE REPLICA RULE r", "REPLICA"},
		{"ALTER TABLE src ENABLE RULE IF EXISTS r", "EXISTS"},
		{"ALTER TABLE src ENABLE RULE ALL", "ALL"},
		{"ALTER TABLE src DISABLE RULE r trailing", "trailing"},
		{"ALTER TABLE src ENABLE SOMETIMES RULE r", "SOMETIMES"},
	} {
		t.Run(tc.token, func(t *testing.T) {
			ruleModeRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				c := ruleModeConn(t, p, b)
				run := transactionalRulesRun(t, p, b, c, nil)
				ruleModeSetup(run)
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42601", Message: "syntax error at or near \"" + tc.token + "\"", Position: fixture.Position(strings.Index(tc.q, tc.token) + 1)})
				if held {
					s.ErrorStage = "prepare"
				} else {
					s.ErrorStage = "execute"
				}
				ruleRun(t, p, b, held, s)
				run(ruleModeCatalog("O"))
				run(ruleModeCount("src", 0))
				run(ruleModeCount("sink1", 0))
			})
		})
	}
	t.Run("read-only", func(t *testing.T) {
		ruleModeTransactions(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
			base := transactionalRulesRun(t, p, b, c, nil)
			ruleModeSetup(base)
			q := "ALTER TABLE src DISABLE RULE r"
			h := transactionalRulesHandles(t, p, c, held, q)
			e, finish := transactionalViewsBegin(t, p, b, c, mode)
			run := transactionalRulesRun(t, p, b, e, h)
			run(fixture.Exec("SET TRANSACTION READ ONLY", 0))
			s := ruleError(q, "25006", "cannot execute ALTER TABLE in a read-only transaction")
			s.ErrorStage = "execute"
			run(s)
			run(ruleError("SELECT 1", "25P02", "current transaction is aborted, commands ignored until end of transaction block"))
			finish(false)
			base(ruleModeCatalog("O"))
			base(ruleModeCount("src", 0))
		})
	})
}
