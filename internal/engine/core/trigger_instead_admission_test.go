package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// This fixture publishes a real typed, source-free view over the existing
// composed trigger fixture. No SQL outcomes, synthetic heap or row IDs are used.
func insteadUnitFixture(t *testing.T, ctx context.Context, body string) (*core.Engine, *core.StatementScope, catalog.Read, *catalog.Relation, *catalog.Procedure) {
	t.Helper()
	e, s, read, relation, routine := triggerUnitFixtureContext(t, ctx, body)
	integer, _ := read.LookupType(sqltype.Int4)
	syntax := &parser.Select{}
	expressions := []expr.Expr{
		plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: triggerUnitInt(t, integer, 1)},
		plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: triggerUnitInt(t, integer, 5)},
	}
	logical := *relation
	logical.RelationKind, logical.VirtualView = catalog.ViewRelation, true
	logical.TableMethod = 0
	logical.View = stored.NewView(stored.ViewParts{Syntax: syntax, Query: &plan.Query{Root: &plan.ProjectNode{QuerySchema: plan.QuerySchema(logical.Columns), Child: &plan.SingletonNode{}, Expressions: expressions, Frame: 1, Syntax: syntax}}})
	parts := triggerUnitDefinition(t, read, &logical, routine, "a", stored.TriggerInsert|stored.TriggerUpdate|stored.TriggerDelete).Parts()
	parts.Timing = stored.TriggerInstead
	definition, err := stored.NewTrigger(parts)
	if err != nil {
		t.Fatal(err)
	}
	logical.Triggers = stored.TriggersPayload([]stored.Trigger{definition})
	e.Mu.Lock()
	next := catalog.CloneState(e.Catalog.State)
	next.SetDescriptor(logical.Address(), &logical)
	e.Catalog.PublishLocked(next)
	delete(e.Storage.Relations, logical.Address())
	e.Mu.Unlock()
	return e, s, e.CatalogRead(), &logical, routine
}

func insteadUnitAdmit(t *testing.T, s *core.StatementScope, relation *catalog.Relation, ref ids.ObjectRef, events ...stored.TriggerEvent) *view.Write {
	t.Helper()
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: relation.Name()}}}
	var statement parser.Statement = &parser.Insert{Name: name}
	event := stored.TriggerInsert
	if len(events) != 0 {
		event = events[0]
	}
	switch event {
	case stored.TriggerUpdate:
		statement = &parser.Update{Name: name}
	case stored.TriggerDelete:
		statement = &parser.Delete{Name: name}
	}
	if err := s.Gate(statement); err != nil {
		t.Fatal(err)
	}
	if err := core.ScopeAcquireRelationTarget(s, statement, statement, true, false); err != nil {
		t.Fatal(err)
	}
	binding := analyze.Binding{Relation: relation, Ref: ref}
	if event == stored.TriggerUpdate {
		binding.Update = &analyze.UpdateBinding{}
	}
	e := s.Engine()
	if err := view.BindViewWrite(&e.Catalog.Owner, e.CatalogRead(), s.RoutineAuth.RuleReplica(), 0, &binding, statement); err != nil {
		t.Fatal(err)
	}
	w := view.ViewWriteOf(binding.ViewWrite)
	if w == nil || !w.Instead || !binding.ViewWrite.LogicalTrigger() || w.Ref != ref || w.BaseRelation != relation || len(w.Layers) != 0 {
		t.Fatal("lost exact logical endpoint")
	}
	if err := core.ScopeAcquireViewWriteTarget(s, w); err != nil {
		t.Fatal(err)
	}
	return w
}

func TestTriggerInsteadLogicalAdmission(t *testing.T) {
	e, s, read, relation, _ := insteadUnitFixture(t, context.Background(), "BEGIN RETURN NEW; END")
	ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
	integer, _ := read.LookupType(sqltype.Int4)
	request := dml.BeforeRow{Relation: ref, Event: dml.BeforeInsert, New: []sqltype.Value{triggerUnitInt(t, integer, 1), triggerUnitInt(t, integer, 5)}, Instead: true}
	if _, err := s.WriteHost().BeforeRow(request); err == nil {
		t.Fatal("unadmitted logical invocation")
	}
	w := insteadUnitAdmit(t, s, relation, ref)
	if s.LockedStorage != nil || s.LockedDescriptor != relation || s.LockedRef != ref || !e.Locks.Holds(s.LockTicket, lock.Resource{ObjectRef: ref}, lock.RowExclusive) {
		t.Fatal("logical lifecycle admission forged storage or lost lock")
	}
	result, err := s.WriteHost().BeforeRow(request)
	if err != nil || !result.Proceed {
		t.Fatal(err)
	}
	physical := request
	physical.Instead = false
	if _, err := s.WriteHost().BeforeRow(physical); err == nil {
		t.Fatal("logical lifecycle RX granted physical BEFORE authority")
	}
	for _, kind := range []string{"domain", "epoch", "identity", "shape", "rowtype", "kind", "event"} {
		t.Run(kind, func(t *testing.T) {
			invalid := *w
			switch kind {
			case "domain":
				invalid.Ref = (&ids.Domain{}).Ref(ref.Epoch, ref.Address)
			case "epoch":
				invalid.Ref.Epoch++
			case "identity":
				invalid.Ref.Address.ObjectID++
			case "shape":
				copy := *relation
				copy.Attributes = []int32{7, 8}
				invalid.BaseRelation = &copy
			case "rowtype":
				copy := *relation
				copy.RowType = sqltype.Int4
				invalid.BaseRelation = &copy
			case "kind":
				copy := *relation
				copy.RelationKind = catalog.TableRelation
				invalid.BaseRelation = &copy
			case "event":
				invalid.TriggerEvent = stored.TriggerTruncate
			}
			if err := core.ScopeAcquireViewWriteTarget(s, &invalid); err == nil {
				t.Fatal("invalid logical target accepted")
			}
		})
	}
	e.Mu.Lock()
	logicalStorage := e.PrivateStorageLocked(nil, relation.Address())
	e.Mu.Unlock()
	if logicalStorage != nil {
		t.Fatal("logical execution manufactured storage")
	}
	if err := s.Finish(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteHost().BeforeRow(request); err == nil {
		t.Fatal("finished owner invoked chain")
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, canceled, canceledRead, logical, _ := insteadUnitFixture(t, ctx, "BEGIN RETURN NEW; END")
	logicalRef := canceledRead.Owner().Domain.Ref(canceledRead.Epoch(), logical.Address())
	recipe := &view.Write{BaseRelation: logical, Ref: logicalRef, Instead: true}
	cancel()
	if err := core.ScopeAcquireViewWriteTarget(canceled, recipe); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled admission", err)
	}
	t.Run("physical-leaf-authority", func(t *testing.T) {
		for _, nested := range []bool{false, true} {
			name, body := "return-only", "BEGIN RETURN NEW; END"
			if nested {
				name = "nested-update"
				body = "BEGIN UPDATE trigger_unit_table SET v=v+NEW.v WHERE id=NEW.id; RETURN NEW; END"
			}
			t.Run(name, func(t *testing.T) {
				e, s, read, base, routine := triggerUnitFixture(t, body)
				baseRef := read.Owner().Domain.Ref(read.Epoch(), base.Address())
				integer, _ := read.LookupType(sqltype.Int4)
				boolean, _ := read.LookupType(sqltype.Bool)
				logical := *base
				logical.ObjectAddress.ObjectID += 100
				logical.ObjectName = "trigger_unit_logical"
				logical.RelationKind, logical.VirtualView = catalog.ViewRelation, true
				logical.TableMethod = 0
				shape, err := sqltype.Composite(base.RowType+100, logical.Name(), "public."+logical.Name(), 0, []sqltype.CompositeMember{{Attribute: 1, Name: "id", Usage: integer.Usage()}, {Attribute: 2, Name: "v", Usage: integer.Usage()}})
				if err != nil {
					t.Fatal(err)
				}
				logical.RowType = shape.ID()
				syntax := &parser.Select{From: &parser.TableReference{}}
				expressions := make([]expr.Expr, len(base.Columns))
				for i, column := range base.Columns {
					origin := baseRef
					origin.Address.SubID = base.Attributes[i]
					expressions[i] = plan.ColumnExpr{Info: view.RuleColumnInfo(column), Origin: origin, Scope: 1, Frame: 1, Slot: i}
				}
				logical.View = stored.NewView(stored.ViewParts{Syntax: syntax, Query: &plan.Query{Root: &plan.ProjectNode{QuerySchema: plan.QuerySchema(logical.Columns), Frame: 1, Syntax: syntax, Child: &plan.SourceNode{QuerySchema: plan.QuerySchema(base.Columns), Ref: baseRef, Occurrence: 0}, Expressions: expressions}}, Sources: []stored.ViewLeaf{{Ref: baseRef, Attributes: base.Attributes, Reads: base.Attributes}}})
				parts := triggerUnitDefinition(t, read, &logical, routine, "a", stored.TriggerInsert).Parts()
				parts.Timing = stored.TriggerInstead
				definition, err := stored.NewTrigger(parts)
				if err != nil {
					t.Fatal(err)
				}
				logical.Triggers = stored.TriggersPayload([]stored.Trigger{definition})
				typ := &catalog.Type{SQLType: shape, ObjectNamespace: logical.Namespace(), ObjectRevision: 1, Owner: logical.Owner, OwningRelation: logical.Address()}
				var heap *storage.Relation
				var rows []storage.RowVersion
				sawPhysical := false
				probe := queryProbe(boolean.Usage(), func(*exec.Context) (sqltype.Value, error) {
					// This runs in the genuine nested UPDATE's physical CHECK
					// phase, while its command scratch and row grants are live.
					target, missing := e.Locks.ReplacementGrants(s.LockTicket, baseRef, []ids.RowID{rows[0].Row})
					if !target || missing != -1 || s.LockedRef != baseRef || s.LockedDescriptor != base || s.LockedStorage != heap || heap == nil || s.ViewWrite != nil {
						t.Fatal("nested DML lacks its own physical authority/storage")
					}
					sawPhysical = true
					return sqltype.Boolean(boolean, true), nil
				}).(queryProbeExpression)
				probe.Property.RowDependent = true
				physical := *base
				physical.Constraints.Check = []catalog.CheckConstraint{{OID: 9500200, Name: "physical_authority", Expression: probe}}
				base = &physical
				e.Mu.Lock()
				state := catalog.CloneState(e.Catalog.State)
				state.SetDescriptor(base.Address(), base)
				state.SetDescriptor(logical.Address(), &logical)
				state.BindName(catalog.DescriptorNameKey(&logical), logical.Address())
				state.SetDescriptor(typ.Address(), typ)
				state.BindName(catalog.DescriptorNameKey(typ), typ.Address())
				e.Catalog.PublishLocked(state)
				heap = e.Storage.Relations[base.Address()]
				rows, err = e.Storage.AllocateRowVersionsLocked([][]sqltype.Value{{triggerUnitInt(t, integer, 1), triggerUnitInt(t, integer, 5)}}, state)
				if err == nil {
					e.Visibility.AdvanceCommitSeqLocked()
					rows[0].Born = e.Visibility.CommitSeq()
					heap.Versions = rows
				}
				e.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				logicalRef := read.Owner().Domain.Ref(read.Epoch(), logical.Address())
				insteadUnitAdmit(t, s, &logical, logicalRef)
				assertLogical := func() {
					t.Helper()
					if e.Locks.TransactionTicket(s.TxRef) != s.LockTicket || s.LockedRef != logicalRef || s.LockedDescriptor != &logical || s.LockedStorage != nil {
						t.Fatal("logical invocation lost its genuine owner or gained storage")
					}
					if target, _ := e.Locks.ReplacementGrants(s.LockTicket, logicalRef, nil); !target {
						t.Fatal("logical view lost genuine lifecycle RX")
					}
				}
				assertNoLeafWrite := func() {
					t.Helper()
					target, missing := e.Locks.ReplacementGrants(s.LockTicket, baseRef, []ids.RowID{rows[0].Row})
					if target || missing != 0 || e.Locks.Holds(s.LockTicket, lock.Resource{ObjectRef: baseRef}, lock.RowExclusive) || e.Locks.Holds(s.LockTicket, lock.Resource{ObjectRef: baseRef, Row: rows[0].Row}, lock.ForNoKeyUpdate|lock.ForUpdate) {
						t.Fatal("logical interception fabricated physical-leaf write grants")
					}
				}
				assertLogical()
				assertNoLeafWrite()
				result, err := s.WriteHost().BeforeRow(dml.BeforeRow{Relation: logicalRef, Event: dml.BeforeInsert, Instead: true, New: []sqltype.Value{triggerUnitInt(t, integer, 1), triggerUnitInt(t, integer, 7)}})
				if err != nil || !result.Proceed {
					t.Fatal("actual logical invocation failed", err)
				}
				assertLogical()
				if nested {
					target, missing := e.Locks.ReplacementGrants(s.LockTicket, baseRef, []ids.RowID{rows[0].Row})
					staged := s.Stage.Rows[base.Address()]
					if !sawPhysical || !target || missing != -1 || len(staged) != 1 || staged[0].Row != rows[0].Row || staged[0].Version == rows[0].Version {
						t.Fatal("nested UPDATE did not retain genuine leaf grants/staged successor")
					}
				} else {
					assertNoLeafWrite()
					if sawPhysical || len(s.Stage.Rows[base.Address()]) != 0 {
						t.Fatal("return-only invocation entered physical mutation")
					}
				}
				if heap.Versions[0].Version != rows[0].Version || e.Storage.Relations[logical.Address()] != nil {
					t.Fatal("invocation mutated committed heap backing or fabricated logical storage")
				}
			})
		}
	})
	t.Run("automatic-outer-endpoint", func(t *testing.T) {
		e, s, read, inner, _ := insteadUnitFixture(t, context.Background(), "BEGIN RETURN NEW; END")
		innerRef := read.Owner().Domain.Ref(read.Epoch(), inner.Address())
		outer := *inner
		outer.ObjectAddress.ObjectID += 100
		outer.ObjectName = "trigger_unit_outer"
		outer.Triggers = nil
		shape, err := sqltype.Composite(inner.RowType+100, outer.Name(), "public."+outer.Name(), 0, []sqltype.CompositeMember{{Attribute: 1, Name: "id", Usage: inner.Columns[0].Usage}, {Attribute: 2, Name: "v", Usage: inner.Columns[1].Usage}})
		if err != nil {
			t.Fatal(err)
		}
		outer.RowType = shape.ID()
		syntax := &parser.Select{From: &parser.TableReference{}}
		expressions := make([]expr.Expr, len(outer.Columns))
		for i, c := range outer.Columns {
			expressions[i] = plan.ColumnExpr{Info: expr.Info{Usage: c.Usage, Property: expr.Properties{RowDependent: true}}, Origin: innerRef, Scope: 1, Frame: 1, Slot: i}
		}
		outer.View = stored.NewView(stored.ViewParts{Syntax: syntax, Query: &plan.Query{Root: &plan.ProjectNode{QuerySchema: plan.QuerySchema(outer.Columns), Frame: 1, Syntax: syntax, Child: &plan.SourceNode{QuerySchema: plan.QuerySchema(inner.Columns), Ref: innerRef, Occurrence: 0}, Expressions: expressions}}, Sources: []stored.ViewLeaf{{Ref: innerRef, Attributes: []int32{1, 2}, Reads: []int32{1, 2}}}})
		typ := &catalog.Type{SQLType: shape, ObjectNamespace: outer.Namespace(), ObjectRevision: 1, Owner: outer.Owner, OwningRelation: outer.Address()}
		e.Mu.Lock()
		state := catalog.CloneState(e.Catalog.State)
		state.SetDescriptor(outer.Address(), &outer)
		state.BindName(catalog.DescriptorNameKey(&outer), outer.Address())
		state.SetDescriptor(typ.Address(), typ)
		state.BindName(catalog.DescriptorNameKey(typ), typ.Address())
		e.Catalog.PublishLocked(state)
		e.Mu.Unlock()
		ref := read.Owner().Domain.Ref(read.Epoch(), outer.Address())
		statement := &parser.Insert{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: outer.Name()}}}}
		if err := s.Gate(statement); err != nil {
			t.Fatal(err)
		}
		if err := core.ScopeAcquireRelationTarget(s, statement, statement, true, false); err != nil {
			t.Fatal(err)
		}
		binding := analyze.Binding{Relation: &outer, Ref: ref}
		if err := view.BindViewWrite(&e.Catalog.Owner, e.CatalogRead(), s.RoutineAuth.RuleReplica(), 0, &binding, statement); err != nil {
			t.Fatal(err)
		}
		endpoint := view.ViewWriteOf(binding.ViewWrite)
		if endpoint == nil || !endpoint.Instead || endpoint.Ref != innerRef || endpoint.BaseRelation != inner || endpoint.Logical.Ref != ref || len(endpoint.Layers) != 1 || endpoint.Layers[0].Ref != ref || endpoint.Layers[0].SourceRef != innerRef {
			t.Fatal("automatic outer lost exact logical endpoint/layer identity")
		}
		if err := core.ScopeAcquireViewWriteTarget(s, endpoint); err != nil {
			t.Fatal(err)
		}
		if s.LockedStorage != nil || s.LockedRef != innerRef || s.LockedDescriptor != inner {
			t.Fatal("outer lowering manufactured physical authority")
		}
		stale := *endpoint
		stale.Layers = append([]view.WriteLayer(nil), endpoint.Layers...)
		changed := outer
		changed.View = stored.ViewOf(&outer).With(func(p *stored.ViewParts) { p.CheckOption = stored.CheckCascaded })
		stale.Layers[0].Relation = &changed
		if err := core.ScopeAcquireViewWriteTarget(s, &stale); err == nil {
			t.Fatal("stale outer definition admitted")
		}
	})
}
