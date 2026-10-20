package engine

import (
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"testing"
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
					auth, err := op.Authorization()
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
					original := ruleUnitInsert(e, src, 1, 10).binding
					original.querySources = []exec.Leaf{{Ref: inputRef, Relation: &input, Uses: []int{0}}}
					if effective {
						original.querySources[0].Principal = reader.Ref
					}
					from := &plan.SourceNode{QuerySchema: plan.QuerySchema(input.Columns), Ref: inputRef}
					var syntax parser.Statement = &parser.Delete{}
					original.delete.using = from
					if event == "UPDATE" {
						syntax = &parser.Update{}
						original.update = &boundUpdate{assignments: []expr.Expr{ruleUnitInt(src, 1), ruleUnitInt(src, 10)}, from: from}
					}
					action := ruleUnitInsert(e, sink, 7, 8)
					ref := lookupRef
					ref.Address.SubID = sink.Attributes[0]
					value := expr.Expr(plan.ColumnExpr{Info: ruleColumnInfo(sink.Columns[0]), Origin: ref, Scope: 1, Frame: 1})
					if dependent {
						ref = original.ref
						ref.Address.SubID = src.Attributes[0]
						value = plan.ColumnExpr{Info: ruleColumnInfo(src.Columns[0]), Origin: ref, Scope: rulePseudoScope, Frame: 1}
					}
					action.binding.expressions = nil
					action.binding.commandQuery = true
					action.binding.query = &plan.Query{Root: &plan.ProjectNode{QuerySchema: plan.QuerySchema(sink.Columns), Child: &plan.SourceNode{QuerySchema: plan.QuerySchema(sink.Columns), Ref: lookupRef}, Expressions: []expr.Expr{value, ruleUnitInt(sink, 8)}, Frame: 1}}
					action.binding.querySources = []exec.Leaf{{Ref: lookupRef, Relation: sink, Uses: []int{0}}}
					src.Rules = rulesPayloadOf([]ruleDefinition{{OID: 900101, Name: "r", event: event, actions: []ruleAction{action}}})
					program := ruleProgram{count: -1, returning: -1}
					e.mu.Lock()
					_, err = e.expandRulesLocked(nil, auth, syntax, original, nil, &program)
					e.mu.Unlock()
					if err != nil {
						t.Fatal(err)
					}
					sources := program.commands[0].binding.querySources
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
					if err := auth.checkQuerySources(sources); err != nil {
						t.Fatal("canonical source authorization rejected resolved principals", err)
					}
					input.Security.ACL = authorization.ACLValue{Explicit: true}
					err = auth.checkQuerySources(sources)
					if dependent && !effective {
						if err == nil || !strings.Contains(err.Error(), "permission denied for table input") {
							t.Fatal("canonical caller-source control did not deny revoked input", err)
						}
					} else if err != nil {
						t.Fatal("caller revocation changed independent/effective authority", err)
					}
					if action.binding.querySources[0].Principal != (authorization.PrincipalRef{}) || original.querySources[0].Principal != inherited {
						t.Fatal("expansion mutated original source or stored action authority")
					}
				})
			}
		}
	}
}
