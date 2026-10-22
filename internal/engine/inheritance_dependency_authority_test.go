package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
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
					provider := &catalog.Type{SQLType: domain, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Owner: e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), ObjectRevision: 1}
					p := inheritanceLifecycleTable(e, 29001, "authority_p")
					p.Columns = []catalog.Column{{Name: "a", Usage: u}, {Name: "keep", Usage: u}}
					p.Attributes, p.ColumnMeta.HighWater, p.Index = []int32{3, 7}, 7, map[string]int{"a": 0, "keep": 1}
					ref := e.catalog.Domain.Ref(e.catalog.state.Epoch(), provider.Address())
					switch definition {
					case "column":
						p.Columns[0].Usage = domain.Usage()
					case "default":
						p.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{3: {OID: 29003, Dependencies: []ids.ObjectRef{ref}}}
					case "check":
						p.Constraints.Check = []catalog.CheckConstraint{{OID: 29003, Name: "authority_check", Dependencies: []catalog.ExpressionDependency{{Ref: ref}}}}
					}
					c := inheritanceLifecycleTable(e, 29002, "authority_c", p.Address())
					inheritanceLifecycleInstall(e, p, c)
					e.catalog.state.SetDescriptor(provider.Address(), provider)
					e.catalog.state.BindName(catalog.DescriptorNameKey(provider), provider.Address())
					if authority == "standalone" || authority == "staged_child" {
						e.catalog.state.RemoveDescriptor(c.Address())
						e.catalog.state.UnbindName(catalog.DescriptorNameKey(c))
					}
					base := e.CatalogRead()
					stage := e.BeginCatalogStage(base)
					defer e.DiscardCatalogStage(stage)
					remove := func(address ids.ObjectAddress) {
						if err := stage.Remove(e.catalog.Domain.Ref(base.Epoch(), address)); err != nil {
							t.Fatal(err)
						}
					}
					if authority == "removed_child" {
						remove(c.Address())
					}
					if authority == "staged_child" {
						if err := stage.Create(c); err != nil {
							t.Fatal(err)
						}
					}
					if authority == "replaced_child" {
						next := *c
						next.Inheritance = nil
						if err := stage.Replace(&next); err != nil {
							t.Fatal(err)
						}
					}
					if authority == "consumed" {
						next := *p
						next.Columns, next.Attributes, next.Index = p.Columns[1:], []int32{7}, map[string]int{"keep": 0}
						next.ColumnMeta.Defaults, next.Constraints.Check = nil, nil
						if err := stage.Replace(&next); err != nil {
							t.Fatal(err)
						}
					}
					roots := []ids.ObjectAddress{provider.Address()}
					if authority == "unchanged" {
						roots = []ids.ObjectAddress{c.Address()}
					}
					if authority == "whole" {
						remove(p.Address())
					}
					mode := parser.DropCascade
					if authority == "restrict" {
						mode = parser.DropRestrict
					}
					projected := 0
					project := func(old, next *catalog.Relation) error {
						projected++
						if old != p || len(next.Columns) != 1 || next.Columns[0].Name != "keep" || next.Attributes[0] != 7 {
							t.Fatal("wrong stable-column projection")
						}
						return nil
					}
					if definition == "column" || authority == "whole" {
						err = stageExpressionDependencies(context.Background(), stage, roots, mode, nil, nil, project, ids.ObjectAddress{})
					} else if definition == "default" {
						var edges []table.DefaultDependencyEdge
						edges, err = stageDefaultDependents(stage, roots, mode)
						if authority == "restrict" && (err != nil || len(edges) != 1) {
							t.Fatal("RESTRICT must report its dependency without mutation", edges, err)
						}
					} else {
						var edges []table.CheckDependencyEdge
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
						if projected != 0 || stage.DescriptorCount() != wantDescriptors {
							t.Fatal("rejection mutated stage")
						}
					} else if err != nil {
						t.Fatal(err)
					}
					if authority == "whole" {
						if _, removed := stage.Removal(c.Address()); !removed || projected != 0 {
							t.Fatal("whole retirement projected or retained descendant")
						}
					}
					if authority == "removed_child" || authority == "replaced_child" || authority == "standalone" {
						next, ok := stage.Get(p.Address()).(*catalog.Relation)
						if !ok {
							t.Fatal("standalone mutation not staged")
						}
						if definition == "column" && projected != 1 || definition == "default" && len(next.ColumnMeta.Defaults) != 0 || definition == "check" && len(next.Constraints.Check) != 0 {
							t.Fatal("dependent definition was not consumed")
						}
					}
					if e.catalog.state != base.State() || e.catalog.state.Get(p.Address()) != p || len(p.Columns) != 2 || c.Inheritance.Parents[0].Relation != p.Address() {
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
			c := inheritanceLifecycleTable(e, 29102, "prospective_c", q.Address(), p.Address())
			u := testUsage(e, sqltype.Int4)
			p.Columns = []catalog.Column{{Name: "a", Usage: u}}
			p.Attributes, p.ColumnMeta.HighWater, p.Index = []int32{1}, 1, map[string]int{"a": 0}
			c.Columns = []catalog.Column{{Name: "a", Usage: u}}
			c.Attributes, c.ColumnMeta.HighWater, c.Index = []int32{1}, 1, map[string]int{"a": 0}
			ref := e.catalog.Domain.Ref(e.catalog.state.Epoch(), q.Address())
			if definition == "default" {
				p.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: {OID: 29103, Dependencies: []ids.ObjectRef{ref}}}
				c.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: {OID: 29104, Dependencies: []ids.ObjectRef{ref}}}
			} else {
				p.Constraints.Check = []catalog.CheckConstraint{{OID: 29103, Name: "provider_check", Dependencies: []catalog.ExpressionDependency{{Ref: ref}}}}
				c.Constraints.Check = []catalog.CheckConstraint{{OID: 29104, Name: "provider_check", Dependencies: []catalog.ExpressionDependency{{Ref: ref}}}}
			}
			inheritanceLifecycleInstall(e, q, p, c)
			op, done := roleOperation(t, e, "postgres")
			defer done()
			tx, err := e.Begin(op, session.TxOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := e.Rollback(op, tx); err != nil {
					t.Error(err)
				}
				lockEmpty(t, e)
			}()
			scope := &StatementScope{op: op, owner: op.Session.Transactions, txRef: tx, ticket: op.Session.Transactions.Ticket}
			base := e.CatalogRead()
			stage := e.BeginCatalogStage(base)
			defer e.DiscardCatalogStage(stage)
			if err := stage.Remove(ref); err != nil {
				t.Fatal(err)
			}
			if err := stageExpressionDependencies(op.Context(), stage, []ids.ObjectAddress{q.Address()}, parser.DropCascade, scope, nil, nil, ids.ObjectAddress{}); err != nil {
				t.Fatal(err)
			}
			removedQ, hasQ := stage.Removal(q.Address())
			removedC, hasC := stage.Removal(c.Address())
			if !hasQ || !hasC || removedQ.Descriptor != q || removedC.Descriptor != c {
				t.Fatal("complete retirement lost captured descriptor authority")
			}
			if _, removed := stage.Removal(p.Address()); removed {
				t.Fatal("other parent was whole-retired")
			}
			next, ok := stage.Get(p.Address()).(*catalog.Relation)
			if !ok || next == p || len(next.Columns) != len(p.Columns) {
				t.Fatal("retained parent definition mutation not staged")
			}
			if len(next.ColumnMeta.Defaults) != 0 || len(next.Constraints.Check) != 0 || stageDropProvider(nil, stage, nil, nil, nil).InheritanceAlterGuard(next) != nil {
				t.Fatal("retained parent did not become standalone with consumed definition")
			}
			if e.catalog.state != base.State() || e.catalog.state.Get(q.Address()) != q || e.catalog.state.Get(p.Address()) != p || e.catalog.state.Get(c.Address()) != c || len(c.Inheritance.Parents) != 2 || c.Inheritance.Parents[0].Relation != q.Address() || c.Inheritance.Parents[1].Relation != p.Address() {
				t.Fatal("source or captured inheritance authority changed")
			}
			if definition == "default" {
				if p.ColumnMeta.Defaults[1].OID != 29103 || c.ColumnMeta.Defaults[1].OID != 29104 || p.ColumnMeta.Defaults[1].Dependencies[0] != ref {
					t.Fatal("captured default authority changed")
				}
			} else if p.Constraints.Check[0].OID != 29103 || c.Constraints.Check[0].OID != 29104 || p.Constraints.Check[0].Dependencies[0].Ref != ref {
				t.Fatal("captured CHECK authority changed")
			}
		})
	}
}
