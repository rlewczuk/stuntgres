package engine

import (
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// This tree owns genuine row descriptors and stores; no SQL/driver scenario or
// fake trigger provider is used to distinguish batching from accepted prefixes.
func triggerPartitionUnitTree(t *testing.T, e *Engine, model *catalog.Relation) (*catalog.Relation, *catalog.Relation, *catalog.Relation) {
	t.Helper()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	class, _ := sqltype.DefaultHashIndexClass(integer)
	hash, err := operatorFamilyTestPartitionHash(integer.Usage(), class)
	if err != nil {
		t.Fatal(err)
	}
	var relations [3]*catalog.Relation
	e.mu.Lock()
	state := catalog.CloneState(e.catalog.State)
	for i, name := range []string{"trigger_partition_root", "trigger_partition_low", "trigger_partition_high"} {
		d := *model
		d.ObjectAddress.ObjectID = 9500101 + ids.OID(i)
		d.ObjectName = name
		d.RowType = 9500201 + sqltype.ID(i)
		d.Triggers = nil
		members := []sqltype.CompositeMember{{Attribute: 1, Name: "id", Usage: integer.Usage()}, {Attribute: 2, Name: "v", Usage: integer.Usage()}}
		shape, failure := sqltype.Composite(d.RowType, name, "public."+name, 0, members)
		if failure != nil {
			e.mu.Unlock()
			t.Fatal(failure)
		}
		rowtype := &catalog.Type{SQLType: shape, ObjectNamespace: d.Namespace(), ObjectRevision: 1, Owner: d.Owner, OwningRelation: d.Address()}
		state.SetDescriptor(rowtype.Address(), rowtype)
		state.BindName(catalog.DescriptorNameKey(rowtype), rowtype.Address())
		relations[i] = &d
	}
	root, low, high := relations[0], relations[1], relations[2]
	root.RelationKind = catalog.PartitionedRelation
	root.History.HasSubclasses = true
	root.Partition = &catalog.PartitionDefinition{Strategy: parser.PartitionHash, Keys: []catalog.PartitionKeyEntry{{Attribute: 1, Usage: integer.Usage(), Class: class, Hash: hash, Label: "id"}}, Children: []ids.ObjectAddress{low.Address(), high.Address()}}
	low.Membership = &catalog.PartitionMembership{Parent: root.Address(), Strategy: parser.PartitionHash, Modulus: 4, Remainder: 0}
	high.Membership = &catalog.PartitionMembership{Parent: root.Address(), Strategy: parser.PartitionHash, Modulus: 4, Remainder: 2}
	for _, d := range relations {
		state.SetDescriptor(d.Address(), d)
		state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	e.catalog.PublishLocked(state)
	e.storage.Relations[low.Address()] = &storage.Relation{Generation: 1}
	e.storage.Relations[high.Address()] = &storage.Relation{Generation: 1}
	e.mu.Unlock()
	return root, low, high
}

func TestTriggerCorePartitionPath(t *testing.T) {
	for _, mode := range []string{"none", "update_only", "leaf_insert"} {
		t.Run(mode, func(t *testing.T) {
			e, scope, _, model, function := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
			root, low, _ := triggerPartitionUnitTree(t, e, model)
			if mode != "none" {
				event := stored.TriggerUpdate
				if mode == "leaf_insert" {
					event = stored.TriggerInsert
				}
				definition := triggerUnitDefinition(t, e.CatalogRead(), low, function, "leaf_only", event)
				low = triggerUnitInstall(t, e, low, definition)
			}
			syntax := &parser.Insert{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: root.Name()}}}}
			if err := scope.gate(syntax); err != nil {
				t.Fatal(err)
			}
			scope.statement = syntax
			if err := scope.acquireRelationTarget(syntax, syntax, true, false); err != nil {
				t.Fatal(err)
			}
			e.mu.Lock()
			auth, err := e.authorizationLocked(scope.op)
			if err == nil {
				scope.routineAuth = auth
				err = scope.capturePartitionInsertLocked(e, auth, root)
			}
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			binding := analyze.Binding{Ref: scope.lockedRef, Relation: root, Targets: []int{0, 1}}
			host := scope.writeHost()
			incremental := mode == "leaf_insert"
			if !incremental {
				if allocations := testing.AllocsPerRun(10, func() { dml.BeginInsertReturning(host, binding) }); allocations != 0 {
					t.Fatal("trigger-free INSERT allocated output state", allocations)
				}
			} else {
				dml.BeginInsertReturning(host, binding)
				if scope.InsertReturning == nil {
					t.Fatal("leaf INSERT chain lost accepted-prefix ownership")
				}
				defer scope.InsertReturning.Rows.Close()
			}
			if (scope.InsertReturning != nil) != incremental {
				t.Fatal("incorrect applicable-trigger output ownership")
			}
			integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
			row := []sqltype.Value{triggerUnitInt(t, integer, 1), triggerUnitInt(t, integer, 5)}
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.op.Context(), Host: host, Catalog: e.CatalogRead(), Input: scope.input, Output: scope.output}}
			proceed, err := dml.ValidateInsertRow(host, binding, "public", row, &evaluation)
			if err != nil || !proceed {
				t.Fatal(proceed, err)
			}
			if scope.PartitionInsert.Destination.Address() != low.Address() {
				t.Fatal("unit key did not select the expected physical leaf")
			}
			candidate := index.WriteCandidate{Values: row}
			var batch index.WriteBatch
			admitted, err := dml.AdmitInsertProducer(host, binding, &candidate, batch, &evaluation)
			if err != nil {
				t.Fatal(err)
			}
			if admitted {
				batch.Append(candidate)
			}
			if incremental {
				if admitted || len(scope.stage.rows[low.Address()]) != 1 || scope.InsertReturning.Rows.Count != 1 {
					t.Fatal("leaf trigger did not publish a private accepted prefix")
				}
			} else {
				if !admitted || len(scope.stage.rows[low.Address()]) != 0 {
					t.Fatal("trigger-free proposal bypassed batch ownership")
				}
				count, err := dml.ApplyInsertValidated(host, binding.Ref, batch)
				if err != nil || count != 1 {
					t.Fatal(count, err)
				}
			}
			if len(e.storage.Relations[low.Address()].Versions) != 0 {
				t.Fatal("private insertion escaped before Finish")
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			if len(e.storage.Relations[low.Address()].Versions) != 1 {
				t.Fatal("accepted insertion did not publish at Finish")
			}
		})
	}
}

func TestTriggerCoreWhenProvider(t *testing.T) {
	e, scope, _, relation, trigger := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	helper := triggerUnitProcedure(t, e, scope, "trigger_when_helper", 9500004, boolean, false, "BEGIN RETURN true; END")
	read := e.CatalogRead()
	syntax, err := parser.Parse("SELECT public.trigger_when_helper() AND NEW.v>5")
	if err != nil {
		t.Fatal(err)
	}
	raw := syntax[0].(*parser.Select).Targets[0]
	e.mu.Lock()
	auth, err := e.authorizationLocked(scope.op)
	var condition expr.Expr
	var query *parser.Select
	var binding analyze.Binding
	if err == nil {
		var h view.Host = ddlHost{e: e, op: scope.op}
		condition, query, binding, err = h.BindTriggerWhen(auth, &analyze.RuleNamespace{Relation: relation, Ref: read.Owner().Domain.Ref(read.Epoch(), relation.Address()), Event: "UPDATE", Trigger: true}, raw, scope.input)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	definition := triggerUnitDefinition(t, read, relation, trigger, "captured_helper", stored.TriggerUpdate)
	parts := definition.Parts()
	parts.When = condition
	parts.Binding = &binding
	parts.Presentation, err = stored.FreezeQueryPresentation(read, ids.SessionToken{}, query, binding, false, false, 0, "", false)
	if err != nil {
		t.Fatal(err)
	}
	references, err := stored.ExpressionDependRefs(scope.op.Context(), read, condition)
	if err != nil {
		t.Fatal(err)
	}
	parts.References = append(parts.References, references...)
	definition, err = stored.NewTrigger(parts)
	if err != nil {
		t.Fatal(err)
	}
	payload := stored.TriggersPayload([]stored.Trigger{definition})
	if err := payload.Validate(read, relation); err != nil {
		t.Fatal("healthy captured helper rejected", err)
	}
	for _, kind := range []string{"missing", "changed_definition", "changed_result", "changed_language"} {
		t.Run(kind, func(t *testing.T) {
			state := catalog.CloneState(read.State())
			changed := *helper
			switch kind {
			case "missing":
				state.RemoveDescriptor(helper.Address())
			case "changed_definition":
				copy := *helper.SQL
				changed.SQL = &copy
				state.SetDescriptor(helper.Address(), &changed)
			case "changed_result":
				changed.Result = ids.OID(sqltype.Int4)
				state.SetDescriptor(helper.Address(), &changed)
			case "changed_language":
				changed.Language = ids.ObjectAddress{}
				state.SetDescriptor(helper.Address(), &changed)
			}
			if err := payload.Validate(catalog.NewRead(read.Owner(), state), relation); err == nil {
				t.Fatal("invalid captured helper admitted", kind)
			}
		})
	}
	for _, kind := range []string{"missing_dependency", "foreign_epoch"} {
		t.Run(kind, func(t *testing.T) {
			p := definition.Parts()
			if kind == "missing_dependency" {
				p.References = nil
				for _, reference := range definition.Parts().References {
					if reference != helper.Address() {
						p.References = append(p.References, reference)
					}
				}
			} else {
				copy := *p.Binding
				copy.Dependencies = append([]catalog.ExpressionDependency(nil), copy.Dependencies...)
				found := false
				for i := range copy.Dependencies {
					if copy.Dependencies[i].Ref.Address == helper.Address() {
						copy.Dependencies[i].Ref.Epoch++
						found = true
					}
				}
				if !found {
					t.Fatal("healthy helper has no execution dependency")
				}
				p.Binding = &copy
			}
			bad, err := stored.NewTrigger(p)
			if err == nil {
				err = stored.TriggersPayload([]stored.Trigger{bad}).Validate(read, relation)
			}
			if err == nil {
				t.Fatal("invalid helper dependency admitted", kind)
			}
		})
	}
	if err := payload.Validate(read, relation); err != nil {
		t.Fatal("negative checks mutated the healthy source", err)
	}
	t.Run("shared_capture_graph", func(t *testing.T) {
		state := catalog.CloneState(read.State())
		destination := catalog.NewRead(read.Owner(), state)
		remap := stored.NewRoutineRemap(destination, nil, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, nil, false)
		if err := routine.RebindSQLDefinitions(state, remap); err != nil {
			t.Fatal(err)
		}
		owner := *relation
		owner.Triggers = payload
		mapped, err := stored.RemapTriggers(destination, &owner, nil, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, remap.Type, remap.Collation, remap.Procedure)
		if err != nil {
			t.Fatal(err)
		}
		if err := remap.Finish(); err != nil {
			t.Fatal(err)
		}
		captured, err := remap.Procedure(helper)
		if err != nil {
			t.Fatal(err)
		}
		published, ok := destination.Procedure(helper.Address())
		if !ok || !exec.SameRoutineDeclaration(published, captured) || published.SQL != captured.SQL || published.SQLBody != captured.SQLBody || published.SQLDefaults != captured.SQLDefaults {
			t.Fatal("trigger helper declaration and frozen captures lost shared payload identity")
		}
		check := func(label string, expression expr.Expr) bool {
			t.Helper()
			found := false
			plan.WalkExpression(expression, func(node expr.Expr) {
				if call, ok := node.(plan.SQLRoutineExpr); ok && call.Procedure.Address() == helper.Address() {
					found = true
					if call.Procedure != captured || call.RoutineReference != captured {
						t.Fatal(label, "did not retain the exact operation-wide mapped handle")
					}
				}
			})
			return found
		}
		p := stored.TriggersOf(mapped)[0].Parts()
		if !check("WHEN", p.When) {
			t.Fatal("WHEN lost captured helper")
		}
		for label, query := range map[string]*plan.Query{"binding": p.Binding.Query, "presentation": p.Presentation.Query()} {
			found := false
			plan.VisitCTEQuery(query.Root, func(plan.Node) {}, func(node expr.Expr) {
				if check(label, node) {
					found = true
				}
			}, true)
			if !found {
				t.Fatal(label, "lost captured helper")
			}
		}
		if err := mapped.Triggers.Validate(destination, mapped); err != nil {
			t.Fatal(err)
		}
		if err := payload.Validate(read, relation); err != nil {
			t.Fatal("mapping mutated source capture", err)
		}
	})
}
