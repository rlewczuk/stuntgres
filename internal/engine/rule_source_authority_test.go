package engine

import (
	"strings"
	"stuntgres/internal/engine/authorization"
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
					owner := auth.state.CurrentUser
					actor, _ := auth.roles.Lookup("pg_monitor")
					reader, _ := auth.roles.Lookup("pg_read_all_data")
					auth.state.CurrentUser = actor.Ref
					src.owner, sink.owner = owner, owner
					src.acl = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: authorization.SelectPrivilege, Grantor: owner}}}
					input := *src
					input.address.ObjectID += 2
					input.name, input.rules = "input", nil
					next := cloneCatalog(e.catalog.state)
					next.descriptors[input.address] = &input
					e.catalog.state = next
					auth.catalog = e.catalog.read(next)
					auth.pairedCatalog = auth.catalog
					auth.roles = e.roleRead(auth.catalog, auth.roleRecord)
					inputRef, lookupRef := ruleUnitRef(e, &input), ruleUnitRef(e, sink)
					original := ruleUnitInsert(e, src, 1, 10).binding
					original.querySources = []queryLeaf{{ref: inputRef, relation: &input, uses: []int{0}}}
					if effective {
						original.querySources[0].principal = reader.Ref
					}
					from := &sourceNode{querySchema: querySchema(input.columns), ref: inputRef}
					var syntax parser.Statement = &parser.Delete{}
					original.deleteUsing = from
					if event == "UPDATE" {
						syntax = &parser.Update{}
						original.update = &boundUpdate{assignments: []BoundExpr{ruleUnitInt(src, 1), ruleUnitInt(src, 10)}, from: from}
					}
					action := ruleUnitInsert(e, sink, 7, 8)
					ref := lookupRef
					ref.Address.SubID = sink.attributes[0]
					value := BoundExpr(columnExpression{expressionInfo: ruleColumnInfo(sink.columns[0]), origin: ref, scope: 1, frame: 1})
					if dependent {
						ref = original.ref
						ref.Address.SubID = src.attributes[0]
						value = columnExpression{expressionInfo: ruleColumnInfo(src.columns[0]), origin: ref, scope: rulePseudoScope, frame: 1}
					}
					action.binding.expressions = nil
					action.binding.commandQuery = true
					action.binding.query = &BoundQuery{Root: &projectNode{querySchema: querySchema(sink.columns), child: &sourceNode{querySchema: querySchema(sink.columns), ref: lookupRef}, expressions: []BoundExpr{value, ruleUnitInt(sink, 8)}, frame: 1}}
					action.binding.querySources = []queryLeaf{{ref: lookupRef, relation: sink, uses: []int{0}}}
					src.rules = []ruleDefinition{{OID: 900101, Name: "r", event: event, actions: []ruleAction{action}}}
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
					if len(sources) != want || sources[len(sources)-1].principal != owner {
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
						if sources[0].principal != actor.Ref || sources[1].principal != principal {
							t.Fatal("retained producer lost caller/effective source authority")
						}
					}
					if err := auth.checkQuerySources(sources); err != nil {
						t.Fatal("canonical source authorization rejected resolved principals", err)
					}
					input.acl = authorization.ACLValue{Explicit: true}
					err = auth.checkQuerySources(sources)
					if dependent && !effective {
						if err == nil || !strings.Contains(err.Error(), "permission denied for table input") {
							t.Fatal("canonical caller-source control did not deny revoked input", err)
						}
					} else if err != nil {
						t.Fatal("caller revocation changed independent/effective authority", err)
					}
					if action.binding.querySources[0].principal != (authorization.PrincipalRef{}) || original.querySources[0].principal != inherited {
						t.Fatal("expansion mutated original source or stored action authority")
					}
				})
			}
		}
	}
}
