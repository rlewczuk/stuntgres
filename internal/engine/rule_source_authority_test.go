package engine

import (
	"strings"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

func TestRuleExpansionSourceAuthority(t *testing.T) {
	for _, event := range []string{"UPDATE", "DELETE"} {
		for _, dependent := range []bool{false, true} {
			for _, effective := range []bool{false, true} {
				name := event
				if dependent {
					name += "/dependent"
				} else {
					name += "/independent"
				}
				if effective {
					name += "/effective"
				} else {
					name += "/caller"
				}
				t.Run(name, func(t *testing.T) {
					e, _, src, sink := ruleUnitCatalog(t)
					op, done := roleOperation(t, e, "postgres")
					defer done()
					auth, err := operationAuthorization(op)
					if err != nil {
						t.Fatal(err)
					}
					owner := auth.State.CurrentUser
					actor, _ := auth.Roles.Lookup("pg_monitor")
					reader, _ := auth.Roles.Lookup("pg_read_all_data")
					auth.State.CurrentUser = actor.Ref
					src.Owner, sink.Owner = owner, owner
					src.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: authorization.SelectPrivilege, Grantor: owner}}}
					input := *src
					input.ObjectAddress.ObjectID += 2
					input.ObjectName, input.Rules = "input", nil
					next := catalog.CloneState(e.catalog.state)
					next.SetDescriptor(input.Address(), &input)
					e.catalog.state = next
					auth.Catalog = e.catalog.read(next)
					auth.PairedCatalog = auth.Catalog
					auth.Roles = e.roles.read(e.catalog.owner(), auth.Catalog, auth.RoleRecord)
					inputRef, lookupRef := ruleUnitRef(e, &input), ruleUnitRef(e, sink)
					original := ruleUnitInsert(e, src, 1, 10).Binding()
					original.QuerySources = []exec.Leaf{{Ref: inputRef, Relation: &input, Uses: []int{0}}}
					if effective {
						original.QuerySources[0].Principal = reader.Ref
					}
					from := &plan.SourceNode{QuerySchema: plan.QuerySchema(input.Columns), Ref: inputRef}
					var syntax parser.Statement = &parser.Delete{}
					original.Delete.Using = from
					if event == "UPDATE" {
						syntax = &parser.Update{}
						original.Update = &analyze.UpdateBinding{Assignments: []expr.Expr{ruleUnitInt(src, 1), ruleUnitInt(src, 10)}, From: from}
					}
					action := ruleUnitInsert(e, sink, 7, 8)
					ref := lookupRef
					ref.Address.SubID = sink.Attributes[0]
					value := expr.Expr(plan.ColumnExpr{Info: view.RuleColumnInfo(sink.Columns[0]), Origin: ref, Scope: 1, Frame: 1})
					if dependent {
						ref = original.Ref
						ref.Address.SubID = src.Attributes[0]
						value = plan.ColumnExpr{Info: view.RuleColumnInfo(src.Columns[0]), Origin: ref, Scope: analyze.RulePseudoScope, Frame: 1}
					}
					action = action.With(func(p *stored.RuleActionParts) {
						p.Binding.Expressions = nil
						p.Binding.CommandQuery = true
						p.Binding.Query = &plan.Query{Root: &plan.ProjectNode{QuerySchema: plan.QuerySchema(sink.Columns), Child: &plan.SourceNode{QuerySchema: plan.QuerySchema(sink.Columns), Ref: lookupRef}, Expressions: []expr.Expr{value, ruleUnitInt(sink, 8)}, Frame: 1}}
						p.Binding.QuerySources = []exec.Leaf{{Ref: lookupRef, Relation: sink, Uses: []int{0}}}
					})
					src.Rules = stored.RulesPayload([]stored.Rule{stored.NewRule(stored.RuleParts{OID: 900101, Name: "r", Event: event, Actions: []stored.RuleAction{action}})})
					program := view.RuleProgram{Count: -1, Returning: -1}
					e.mu.Lock()
					_, err = view.RuleRewriter{Owner: e.catalog.owner()}.ExpandLocked(auth, syntax, original, nil, &program)
					e.mu.Unlock()
					if err != nil {
						t.Fatal(err)
					}
					sources := program.Commands[0].Binding.QuerySources
					want := 1
					if dependent {
						want = 3
					}
					if len(sources) != want || sources[len(sources)-1].Principal != owner {
						t.Fatal("rule-added lookup lost current event-owner authority")
					}
					inherited := authorization.PrincipalRef{}
					if effective {
						inherited = reader.Ref
					}
					if dependent {
						principal := actor.Ref
						if effective {
							principal = reader.Ref
						}
						if sources[0].Principal != actor.Ref || sources[1].Principal != principal {
							t.Fatal("retained producer lost caller/effective source authority")
						}
					}
					if err := analyze.CheckQuerySources(auth, sources); err != nil {
						t.Fatal("canonical source authorization rejected resolved principals", err)
					}
					input.Security.ACL = authorization.ACLValue{Explicit: true}
					err = analyze.CheckQuerySources(auth, sources)
					if dependent && !effective {
						if err == nil || !strings.Contains(err.Error(), "permission denied for table input") {
							t.Fatal("canonical caller-source control did not deny revoked input", err)
						}
					} else if err != nil {
						t.Fatal("caller revocation changed independent/effective authority", err)
					}
					if action.Binding().QuerySources[0].Principal != (authorization.PrincipalRef{}) || original.QuerySources[0].Principal != inherited {
						t.Fatal("expansion mutated original source or stored action authority")
					}
				})
			}
		}
	}
}
