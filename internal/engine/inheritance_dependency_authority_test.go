package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

// Real staging functions, immutable descriptors and the supplied physical
// projector are the authorities here; no SQL parser or driver fixture is used.
func TestInheritanceDependencyAuthority(t *testing.T) {
	for _, definition := range []string{"column", "default", "check"} {
		t.Run(definition, func(t *testing.T) {
			for _, authority := range []string{"surviving", "staged_child", "removed_child", "replaced_child", "whole", "consumed", "unchanged", "restrict", "standalone"} {
				t.Run(authority, func(t *testing.T) {
					e := New()
					u := testUsage(e, sqltype.Int4)
					domain, err := sqltype.Domain(29000, "authority_d", "public.authority_d", u)
					if err != nil {
						t.Fatal(err)
					}
					provider := &typeDescriptor{typ: domain, namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), revision: 1}
					p := inheritanceLifecycleTable(e, 29001, "authority_p")
					p.columns = []ColumnDescriptor{{Name: "a", Usage: u}, {Name: "keep", Usage: u}}
					p.attributes, p.attributeHighWater, p.index = []int32{3, 7}, 7, map[string]int{"a": 0, "keep": 1}
					ref := e.catalog.domain.Ref(e.catalog.state.epoch, provider.Address())
					switch definition {
					case "column":
						p.columns[0].Usage = domain.Usage()
					case "default":
						p.defaults = map[int32]columnDefault{3: {OID: 29003, dependencies: []ids.ObjectRef{ref}}}
					case "check":
						p.checkConstraints = []checkConstraint{{OID: 29003, Name: "authority_check", dependencies: []expressionDependency{{ref: ref}}}}
					}
					c := inheritanceLifecycleTable(e, 29002, "authority_c", p.address)
					inheritanceLifecycleInstall(e, p, c)
					e.catalog.state.descriptors[provider.Address()] = provider
					e.catalog.state.names[descriptorNameKey(provider)] = provider.Address()
					if authority == "standalone" || authority == "staged_child" {
						delete(e.catalog.state.descriptors, c.address)
						delete(e.catalog.state.names, descriptorNameKey(c))
					}
					base := e.CatalogRead()
					stage := e.BeginCatalogStage(base)
					defer e.DiscardCatalogStage(stage)
					remove := func(address ids.ObjectAddress) {
						if err := stage.Remove(e.catalog.domain.Ref(base.Epoch(), address)); err != nil {
							t.Fatal(err)
						}
					}
					if authority == "removed_child" {
						remove(c.address)
					}
					if authority == "staged_child" {
						if err := stage.Create(c); err != nil {
							t.Fatal(err)
						}
					}
					if authority == "replaced_child" {
						next := *c
						next.inheritance = nil
						if err := stage.Replace(&next); err != nil {
							t.Fatal(err)
						}
					}
					if authority == "consumed" {
						next := *p
						next.columns, next.attributes, next.index = p.columns[1:], []int32{7}, map[string]int{"keep": 0}
						next.defaults, next.checkConstraints = nil, nil
						if err := stage.Replace(&next); err != nil {
							t.Fatal(err)
						}
					}
					roots := []ids.ObjectAddress{provider.Address()}
					if authority == "unchanged" {
						roots = []ids.ObjectAddress{c.address}
					}
					if authority == "whole" {
						remove(p.address)
					}
					mode := parser.DropCascade
					if authority == "restrict" {
						mode = parser.DropRestrict
					}
					projected := 0
					project := func(old, next *relationDescriptor) error {
						projected++
						if old != p || len(next.columns) != 1 || next.columns[0].Name != "keep" || next.attributes[0] != 7 {
							t.Fatal("wrong stable-column projection")
						}
						return nil
					}
					if definition == "column" || authority == "whole" {
						err = stageExpressionDependencies(context.Background(), stage, roots, mode, nil, nil, project, ids.ObjectAddress{})
					} else if definition == "default" {
						var edges []defaultDependencyEdge
						edges, err = stageDefaultDependents(stage, roots, mode)
						if authority == "restrict" && (err != nil || len(edges) != 1) {
							t.Fatal("RESTRICT must report its dependency without mutation", edges, err)
						}
					} else {
						var edges []checkDependencyEdge
						edges, err = stageCheckDependents(stage, roots, mode)
						if authority == "restrict" && (err != nil || len(edges) != 1) {
							t.Fatal("RESTRICT must report its dependency without mutation", edges, err)
						}
					}
					if authority == "surviving" || authority == "staged_child" || authority == "restrict" && definition == "column" {
						want := "0A000"
						if authority == "restrict" {
							want = "2BP01"
						}
						var diagnostic *sqlstate.Error
						if !errors.As(err, &diagnostic) || diagnostic.Code != want {
							t.Fatalf("got %v, want %s", err, want)
						}
						wantDescriptors := 0
						if authority == "staged_child" {
							wantDescriptors = 1
						}
						if projected != 0 || len(stage.descriptors) != wantDescriptors {
							t.Fatal("rejection mutated stage")
						}
					} else if err != nil {
						t.Fatal(err)
					}
					if authority == "whole" {
						if _, removed := stage.removals[c.address]; !removed || projected != 0 {
							t.Fatal("whole retirement projected or retained descendant")
						}
					}
					if authority == "removed_child" || authority == "replaced_child" || authority == "standalone" {
						next, ok := stage.descriptors[p.address].(*relationDescriptor)
						if !ok {
							t.Fatal("standalone mutation not staged")
						}
						if definition == "column" && projected != 1 || definition == "default" && len(next.defaults) != 0 || definition == "check" && len(next.checkConstraints) != 0 {
							t.Fatal("dependent definition was not consumed")
						}
					}
					if e.catalog.state != base.state || e.catalog.state.descriptors[p.address] != p || len(p.columns) != 2 || c.inheritance.parents[0] != p.address {
						t.Fatal("source authority changed")
					}
				})
			}
		})
	}
}

func TestInheritanceProspectiveRetirement(t *testing.T) {
	for _, definition := range []string{"default", "check"} {
		t.Run(definition, func(t *testing.T) {
			e := New()
			q := inheritanceLifecycleTable(e, 29100, "prospective_q")
			p := inheritanceLifecycleTable(e, 29101, "prospective_p")
			c := inheritanceLifecycleTable(e, 29102, "prospective_c", q.address, p.address)
			u := testUsage(e, sqltype.Int4)
			p.columns = []ColumnDescriptor{{Name: "a", Usage: u}}
			p.attributes, p.attributeHighWater, p.index = []int32{1}, 1, map[string]int{"a": 0}
			c.columns = []ColumnDescriptor{{Name: "a", Usage: u}}
			c.attributes, c.attributeHighWater, c.index = []int32{1}, 1, map[string]int{"a": 0}
			ref := e.catalog.domain.Ref(e.catalog.state.epoch, q.address)
			if definition == "default" {
				p.defaults = map[int32]columnDefault{1: {OID: 29103, dependencies: []ids.ObjectRef{ref}}}
				c.defaults = map[int32]columnDefault{1: {OID: 29104, dependencies: []ids.ObjectRef{ref}}}
			} else {
				p.checkConstraints = []checkConstraint{{OID: 29103, Name: "provider_check", dependencies: []expressionDependency{{ref: ref}}}}
				c.checkConstraints = []checkConstraint{{OID: 29104, Name: "provider_check", dependencies: []expressionDependency{{ref: ref}}}}
			}
			inheritanceLifecycleInstall(e, q, p, c)
			op, done := roleOperation(t, e, "postgres")
			defer done()
			tx, err := e.Begin(op, TxOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := e.Rollback(op, tx); err != nil {
					t.Error(err)
				}
				lockEmpty(t, e)
			}()
			scope := &StatementScope{op: op, owner: op.session.transactions, txRef: tx, ticket: op.session.transactions.ticket}
			base := e.CatalogRead()
			stage := e.BeginCatalogStage(base)
			defer e.DiscardCatalogStage(stage)
			if err := stage.Remove(ref); err != nil {
				t.Fatal(err)
			}
			if err := stageExpressionDependencies(op.Context(), stage, []ids.ObjectAddress{q.address}, parser.DropCascade, scope, nil, nil, ids.ObjectAddress{}); err != nil {
				t.Fatal(err)
			}
			if stage.removals[q.address].descriptor != q || stage.removals[c.address].descriptor != c {
				t.Fatal("complete retirement lost captured descriptor authority")
			}
			if _, removed := stage.removals[p.address]; removed {
				t.Fatal("other parent was whole-retired")
			}
			next, ok := stage.descriptors[p.address].(*relationDescriptor)
			if !ok || next == p || len(next.columns) != len(p.columns) {
				t.Fatal("retained parent definition mutation not staged")
			}
			if len(next.defaults) != 0 || len(next.checkConstraints) != 0 || inheritanceStageAlterGuard(stage, next) != nil {
				t.Fatal("retained parent did not become standalone with consumed definition")
			}
			if e.catalog.state != base.state || e.catalog.state.descriptors[q.address] != q || e.catalog.state.descriptors[p.address] != p || e.catalog.state.descriptors[c.address] != c || len(c.inheritance.parents) != 2 || c.inheritance.parents[0] != q.address || c.inheritance.parents[1] != p.address {
				t.Fatal("source or captured inheritance authority changed")
			}
			if definition == "default" {
				if p.defaults[1].OID != 29103 || c.defaults[1].OID != 29104 || p.defaults[1].dependencies[0] != ref {
					t.Fatal("captured default authority changed")
				}
			} else if p.checkConstraints[0].OID != 29103 || c.checkConstraints[0].OID != 29104 || p.checkConstraints[0].dependencies[0].ref != ref {
				t.Fatal("captured CHECK authority changed")
			}
		})
	}
}
