package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// These expectations exercise the scalar expression inquiry, not pg_get_ruledef:
// local subquery Vars must not be mistaken for outer OLD/NEW range slots.
func TestReferencePGRewriteQualSubqueries(t *testing.T) {
	for _, tc := range []struct {
		name, condition, sql, prettySQL string
		outer, newSide                  bool
	}{
		{"uncorrelated", "EXISTS(SELECT v FROM rw_qual_gate)", "(EXISTS ( SELECT v\n   FROM rw_qual_gate))", "(EXISTS ( SELECT v\n   FROM rw_qual_gate))", false, false},
		{"old", "EXISTS(SELECT 1 FROM rw_qual_gate WHERE rw_qual_gate.v=OLD.v)", "(EXISTS ( SELECT 1\n   FROM rw_qual_gate\n  WHERE (rw_qual_gate.v = rw_qual_event.v)))", "(EXISTS ( SELECT 1\n   FROM rw_qual_gate\n  WHERE rw_qual_gate.v = rw_qual_event.v))", true, false},
		{"new", "EXISTS(SELECT 1 FROM rw_qual_gate WHERE rw_qual_gate.v=NEW.v)", "", "", true, true},
		{"mixed", "EXISTS(SELECT 1 FROM rw_qual_gate WHERE rw_qual_gate.v=NEW.v AND rw_qual_gate.v>OLD.v)", "", "", true, true},
		{"nested_new", "EXISTS(SELECT 1 FROM rw_qual_gate g WHERE EXISTS(SELECT 1 FROM rw_qual_gate h WHERE h.v=NEW.v AND g.v=h.v))", "", "", true, true},
		{"local_old_alias", "EXISTS(SELECT 1 FROM rw_qual_gate old WHERE old.v>0)", "(EXISTS ( SELECT 1\n   FROM rw_qual_gate old\n  WHERE (v > 0)))", "(EXISTS ( SELECT 1\n   FROM rw_qual_gate old\n  WHERE v > 0))", false, false},
		{"old_in", "OLD.v IN(SELECT v FROM rw_qual_gate)", "(v IN ( SELECT rw_qual_gate.v\n   FROM rw_qual_gate))", "(v IN ( SELECT rw_qual_gate.v\n   FROM rw_qual_gate))", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					rewriteExec(t, p, b, "CREATE TABLE rw_qual_event(v integer)", "CREATE TABLE rw_qual_gate(v integer)")
					rewriteEffect(t, p, b, "INSERT INTO rw_qual_event VALUES(1)", 1)
					rewriteEffect(t, p, b, "INSERT INTO rw_qual_gate VALUES(1)", 1)
					rewriteExec(t, p, b, "CREATE RULE conditional AS ON UPDATE TO rw_qual_event WHERE "+tc.condition+" DO ALSO NOTHING")
					original := rewriteTrees(t, p, b, "rw_qual_event", "conditional")
					qualified := func(text string) string {
						if tc.name == "uncorrelated" {
							return strings.ReplaceAll(text, "SELECT v\n", "SELECT rw_qual_gate.v\n")
						}
						if tc.name == "local_old_alias" {
							text = strings.ReplaceAll(text, "(v >", "(old.v >")
							return strings.ReplaceAll(text, "WHERE v >", "WHERE old.v >")
						}
						return text
					}
					probe := func(relation, expected, prettyExpected, contextExpected, contextPrettyExpected string) {
						t.Helper()
						for oi, oid := range []string{"0", "'" + relation + "'::regclass", "4294967295::oid"} {
							for pi, pretty := range []string{"", ",false", ",true"} {
								t.Run(relation+"/"+[]string{"zero", "owner", "missing"}[oi]+"/"+[]string{"default", "false", "true"}[pi], func(t *testing.T) {
									q := "SELECT pg_get_expr(ev_qual," + oid + pretty + ") AS expression FROM pg_rewrite WHERE ev_class='" + relation + "'::regclass AND rulename='conditional'"
									message := ""
									if tc.outer && oid == "0" {
										message = "expression contains variables"
									}
									if tc.newSide && oid != "0" {
										message = "expression contains variables of more than one relation"
									}
									if message != "" {
										for _, prepared := range []bool{false, true} {
											s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22023", Message: message})
											s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
											fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
										}
										return
									}
									var want any = expected
									if oid == "4294967295::oid" {
										want = nil
									} else if oi == 1 {
										want = contextExpected
										if pretty == ",true" {
											want = contextPrettyExpected
										}
									} else if pretty == ",true" {
										want = prettyExpected
									}
									rewriteRows(t, p, b, fixture.Select(q, []string{"expression"}, []string{"TEXT"}, [][]any{{want}}))
								})
							}
						}
					}
					probe("rw_qual_event", tc.sql, tc.prettySQL, qualified(tc.sql), qualified(tc.prettySQL))
					// A separate conditional ALSO action proves nonmatch→match
					// effects without turning the inquiry itself into execution.
					rewriteEffect(t, p, b, "UPDATE rw_qual_event SET v=0", 1)
					rewriteExec(t, p, b, "CREATE TABLE rw_qual_log(v integer)", "CREATE RULE logged AS ON UPDATE TO rw_qual_event WHERE "+tc.condition+" DO ALSO INSERT INTO rw_qual_log VALUES(NEW.v)")
					if !tc.outer {
						rewriteEffect(t, p, b, "DELETE FROM rw_qual_gate", 1)
					}
					firstValue, secondValue := 1, 2
					if tc.newSide {
						firstValue, secondValue = 0, 1
					}
					rewriteEffect(t, p, b, "UPDATE rw_qual_event SET v="+strconv.Itoa(firstValue), 1)
					rewriteCount(t, p, b, "SELECT count(*) FROM rw_qual_log", 0)
					if !tc.outer {
						rewriteEffect(t, p, b, "INSERT INTO rw_qual_gate VALUES(1)", 1)
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(fixture.Exec("UPDATE rw_qual_event SET v="+strconv.Itoa(secondValue), 1)))
					rewriteRows(t, p, b, fixture.Select("SELECT v FROM rw_qual_log", []string{"v"}, []string{"INT4"}, [][]any{{int64(secondValue)}}))
					var eventOID int64
					if err := b.DB.QueryRowContext(p.Ctx, "SELECT 'rw_qual_event'::regclass::bigint").Scan(&eventOID); err != nil {
						t.Fatal(err)
					}
					type heldInquiry struct {
						query, suffix string
						statement     *sql.Stmt
					}
					var inquiries []heldInquiry
					for _, suffix := range []string{"", ",false", ",true"} {
						query := "SELECT pg_get_expr(ev_qual,$1::oid" + suffix + ") AS expression FROM pg_rewrite WHERE ev_class=$1::oid AND rulename='conditional'"
						statement, err := b.DB.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer statement.Close()
						inquiries = append(inquiries, heldInquiry{query: query, suffix: suffix, statement: statement})
					}
					rewriteExec(t, p, b, "ALTER TABLE rw_qual_gate RENAME TO rw_qual_renamed_gate", "ALTER TABLE rw_qual_renamed_gate RENAME COLUMN v TO amount", "ALTER TABLE rw_qual_event RENAME TO rw_qual_renamed_event", "ALTER TABLE rw_qual_renamed_event RENAME COLUMN v TO event_value")
					if got := rewriteTrees(t, p, b, "rw_qual_renamed_event", "conditional"); got != original {
						t.Fatal("rename changed retained qualification trees", got, original)
					}
					renamed := func(text string) string {
						text = strings.ReplaceAll(text, "rw_qual_gate.v", "rw_qual_renamed_gate.amount")
						text = strings.ReplaceAll(text, "FROM rw_qual_gate", "FROM rw_qual_renamed_gate")
						text = strings.ReplaceAll(text, "old.v", "old.amount")
						text = strings.ReplaceAll(text, "SELECT rw_qual_renamed_gate.amount\n", "SELECT rw_qual_renamed_gate.amount AS v\n")
						text = strings.ReplaceAll(text, "SELECT v\n", "SELECT amount AS v\n")
						text = strings.ReplaceAll(text, "(v >", "(amount >")
						text = strings.ReplaceAll(text, "WHERE v >", "WHERE amount >")
						text = strings.ReplaceAll(text, "(v IN", "(event_value IN")
						return strings.ReplaceAll(text, "rw_qual_event.v", "rw_qual_renamed_event.event_value")
					}
					probe("rw_qual_renamed_event", renamed(tc.sql), renamed(tc.prettySQL), renamed(qualified(tc.sql)), renamed(qualified(tc.prettySQL)))
					for _, inquiry := range inquiries {
						held := fixture.HeldStatement{Executor: b.DB, Statement: inquiry.statement, SQL: inquiry.query}
						if tc.newSide {
							step := fixture.ImplicitError(inquiry.query, fixture.Diagnostic{Code: "22023", Message: "expression contains variables of more than one relation"}, eventOID)
							step.SelectRows, step.ErrorStage = true, "execute"
							fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
						} else {
							want := renamed(qualified(tc.sql))
							if inquiry.suffix == ",true" {
								want = renamed(qualified(tc.prettySQL))
							}
							fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(inquiry.query, []string{"expression"}, []string{"TEXT"}, [][]any{{want}}, eventOID))
						}
					}
				})
			}
		})
	}
}
