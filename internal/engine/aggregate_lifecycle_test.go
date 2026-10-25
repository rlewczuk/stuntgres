package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestAggregateLifecycleDescriptor(t *testing.T) {
	e := New()
	if _, err := testExecute(e, context.Background(), statement(t, "CREATE AGGREGATE aggregate_lifecycle_unit(integer) (SFUNC=pg_catalog.int4pl,FINALFUNC=pg_catalog.abs,STYPE=int4,INITCOND='0')"), nil); err != nil {
		t.Fatal(err)
	}
	before := e.CatalogRead()
	key := ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "aggregate_lifecycle_unit", Signature: "23"}
	original := before.MustProcedure(before.NameAddress(key))
	snapshot := *original
	command := &parser.AlterRoutine{Kind: "aggregate", Action: "rename", Target: parser.Identifier{Text: "aggregate_lifecycle_changed"}, Routine: parser.GrantRoutineTarget{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: key.Name}}}, SignatureSpecified: true, Arguments: []parser.GrantRoutineArgument{{Type: parser.TypeName{Name: "int4", System: true}}}}}
	if _, err := testExecute(e, context.Background(), command, nil); err != nil {
		t.Fatal(err)
	}
	after := e.CatalogRead()
	current := after.MustProcedure(original.Address())
	if !reflect.DeepEqual(original, &snapshot) || before.MustProcedure(original.Address()) != original {
		t.Fatal("retained descriptor mutated")
	}
	if current == original || current.Address() != original.Address() || current.Revision() == original.Revision() || current.Aggregate != original.Aggregate || current.SQL != original.SQL || current.Builtin != original.Builtin || !exec.SameRoutineDeclaration(current, original) {
		t.Fatal("identity/payload lost", current, original)
	}
	if after.NameAddress(key) != (ids.ObjectAddress{}) {
		t.Fatal("stale lookup remains")
	}
	newKey := key
	newKey.Name = command.Target.Text
	if after.NameAddress(newKey) != original.Address() {
		t.Fatal("new lookup lost address")
	}
	// Validate and then cancel a genuine staged aggregate rename. Publication
	// cannot mutate the retained descriptor, index, or dependency graph.
	op, _ := conversionUnitAuth(t, e)
	command.Routine.Name.Parts[0].Text = newKey.Name
	command.Target.Text = "aggregate_lifecycle_cancelled"
	e.Mu.Lock()
	stage, err := e.stageSchemaLocked(op, command, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	ctx, cancel := context.WithCancel(context.Background())
	prepared, err := e.ValidateCatalogStage(ctx, stage)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: after.Epoch()}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if e.CatalogRead().State() != after.State() || e.CatalogRead().MustProcedure(original.Address()) != current {
		t.Fatal("canceled stage published")
	}
}
