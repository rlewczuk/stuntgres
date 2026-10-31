package core_test

import (
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// This tree owns genuine row descriptors and stores; no SQL/driver scenario or
// fake trigger provider is used to distinguish batching from accepted prefixes.
func triggerPartitionUnitTree(t *testing.T, e *core.Engine, model *catalog.Relation) (*catalog.Relation, *catalog.Relation, *catalog.Relation) {
	t.Helper()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	class, _ := sqltype.DefaultHashIndexClass(integer)
	hash, err := operatorFamilyTestPartitionHash(integer.Usage(), class)
	if err != nil {
		t.Fatal(err)
	}
	var relations [3]*catalog.Relation
	e.Mu.Lock()
	state := catalog.CloneState(e.Catalog.State)
	for i, name := range []string{"trigger_partition_root", "trigger_partition_low", "trigger_partition_high"} {
		d := *model
		d.ObjectAddress.ObjectID = 9500101 + ids.OID(i)
		d.ObjectName = name
		d.RowType = 9500201 + sqltype.ID(i)
		d.Triggers = nil
		members := []sqltype.CompositeMember{{Attribute: 1, Name: "id", Usage: integer.Usage()}, {Attribute: 2, Name: "v", Usage: integer.Usage()}}
		shape, failure := sqltype.Composite(d.RowType, name, "public."+name, 0, members)
		if failure != nil {
			e.Mu.Unlock()
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
	e.Catalog.PublishLocked(state)
	e.Storage.Relations[low.Address()] = &storage.Relation{Generation: 1}
	e.Storage.Relations[high.Address()] = &storage.Relation{Generation: 1}
	e.Mu.Unlock()
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
			if err := scope.Gate(syntax); err != nil {
				t.Fatal(err)
			}
			scope.Statement = syntax
			if err := core.ScopeAcquireRelationTarget(scope, syntax, syntax, true, false); err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(scope.Op)
			if err == nil {
				scope.RoutineAuth = auth
				err = core.ScopeCapturePartitionInsertLocked(scope, e, auth, root)
			}
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			binding := analyze.Binding{Ref: scope.LockedRef, Relation: root, Targets: []int{0, 1}}
			host := scope.WriteHost()
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
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.ActivateRuntime(scope.Op.Context()), Host: host, Catalog: e.CatalogRead(), Input: scope.Input, Output: scope.Output}}
			proceed, err := dml.ValidateInsertRow(host, binding, "public", row, &evaluation)
			if err != nil || !proceed {
				t.Fatal(proceed, err)
			}
			if scope.PartitionInsert.Destination.Address() != low.Address() {
				t.Fatal("unit key did not select the expected physical leaf")
			}
			candidate := index.WriteCandidate{Values: row}
			var batch index.WriteBatch
			admitted, err := dml.AdmitInsertProducer(evaluation.Context, host, binding, &candidate, batch, &evaluation)
			if err != nil {
				t.Fatal(err)
			}
			if admitted {
				batch.Append(candidate)
			}
			if incremental {
				if admitted || len(scope.Stage.Rows[low.Address()]) != 1 || scope.InsertReturning.Rows.Count != 1 {
					t.Fatal("leaf trigger did not publish a private accepted prefix")
				}
			} else {
				if !admitted || len(scope.Stage.Rows[low.Address()]) != 0 {
					t.Fatal("trigger-free proposal bypassed batch ownership")
				}
				count, err := dml.ApplyInsertValidated(evaluation.Context, host, binding.Ref, batch)
				if err != nil || count != 1 {
					t.Fatal(count, err)
				}
			}
			if len(e.Storage.Relations[low.Address()].Versions) != 0 {
				t.Fatal("private insertion escaped before Finish")
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			if len(e.Storage.Relations[low.Address()].Versions) != 1 {
				t.Fatal("accepted insertion did not publish at Finish")
			}
		})
	}
}
