package core_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

func TestDeparseRewriteTriggerImmutable(t *testing.T) {
	// A real invocation would divide by zero; the WHEN also needs an execution
	// frame. Definition rendering traverses both without invoking either.
	e, scope, read, relation, function := triggerUnitFixture(t, "BEGIN NEW.v:=NEW.v/0; RETURN NEW; END")
	definition := triggerUnitDefinition(t, read, relation, function, "display", stored.TriggerInsert|stored.TriggerUpdate)
	parts := definition.Parts()
	parts.Arguments, parts.UpdateColumns = []string{"O'Reilly", `a\b`}, []int32{2, 1}
	id := relation.Address()
	id.SubID = 1
	parts.References = append(parts.References, id)
	definition, err := stored.NewTrigger(parts)
	if err != nil {
		t.Fatal(err)
	}
	definition = infoTriggersWhen(t, e, scope, read, relation, definition)
	relation = triggerUnitInstall(t, e, relation, definition)
	in := infoTriggersInput(t, e, scope)
	before := definition.Parts()
	arguments := slices.Clone(before.Arguments)
	attributes := slices.Clone(before.UpdateColumns)
	references := slices.Clone(before.References)
	for _, pretty := range []bool{false, true, false} {
		renderer := stored.Deparser{View: in.Catalog, Pretty: pretty, Path: []ids.ObjectAddress{relation.Namespace()}}
		// The fixture owns the relation spelling, independently of function and
		// attribute spellings above.
		target := renderer.Identifier(relation.Name())
		if !pretty {
			target = "public." + target
		}
		prefix := "CREATE TRIGGER display BEFORE INSERT OR UPDATE OF v, id ON "
		condition := "((new.v > 0))"
		if pretty {
			condition = "(new.v > 0)"
		}
		want := prefix + target + " FOR EACH ROW WHEN " + condition + " EXECUTE FUNCTION trigger_unit_fn('O''Reilly', 'a\\b')"
		got, err := stored.TriggerDefinitionSQL(t.Context(), relation, definition, renderer)
		if err != nil || got != want {
			t.Fatalf("pretty %t: %q, %v; want %q", pretty, got, err, want)
		}
		after := stored.TriggersOf(relation)[0].Parts()
		if !reflect.DeepEqual(before, after) || !slices.Equal(arguments, after.Arguments) || !slices.Equal(attributes, after.UpdateColumns) || !slices.Equal(references, after.References) || before.Presentation != after.Presentation || before.Binding != after.Binding || &before.Arguments[0] != &after.Arguments[0] || &before.UpdateColumns[0] != &after.UpdateColumns[0] || &before.References[0] != &after.References[0] {
			t.Fatal("definition rendering changed frozen trigger backing")
		}
	}
	// Stable attribute identities follow a new descriptor's current names
	// without rewriting the frozen UPDATE list or WHEN expression.
	renamedState := catalog.CloneState(in.Catalog.State())
	renamed := *relation
	renamed.ObjectName = "renamed"
	renamed.Columns = slices.Clone(relation.Columns)
	renamed.Columns[0].Name, renamed.Columns[1].Name = "record_id", "amount"
	renamedState.SetDescriptor(renamed.Address(), &renamed)
	got, err := stored.TriggerDefinitionSQL(t.Context(), &renamed, definition, stored.Deparser{View: in.Catalog.WithState(renamedState), Path: []ids.ObjectAddress{relation.Namespace()}})
	want := "CREATE TRIGGER display BEFORE INSERT OR UPDATE OF amount, record_id ON public.renamed FOR EACH ROW WHEN ((new.amount > 0)) EXECUTE FUNCTION trigger_unit_fn('O''Reilly', 'a\\b')"
	if err != nil || got != want || !slices.Equal(attributes, definition.Parts().UpdateColumns) || relation.Columns[1].Name != "v" {
		t.Fatal("current names retargeted or mutated frozen identity", got, err)
	}
	for _, remaining := range []int{1, 2, 3, 5} {
		ctx := &dtpCancelContext{Context: context.Background(), remaining: remaining}
		got, err := stored.TriggerDefinitionSQL(ctx, relation, definition, stored.Deparser{View: in.Catalog})
		if !errors.Is(err, context.Canceled) || got != "" {
			t.Fatal("canceled render exposed a definition", got, err)
		}
	}
	// A real advertised identity with invalid current authority is an error,
	// not an absent-object NULL. Exercise the same typed table inquiry seam.
	state := catalog.CloneState(in.Catalog.State())
	broken := *function
	broken.SQL = nil
	state.SetDescriptor(broken.Address(), &broken)
	oidType, _ := read.LookupType(sqltype.Oid)
	textType, _ := read.LookupType(sqltype.Text)
	oid, err := sqltype.OIDValue(oidType, uint32(definition.OID()))
	if err != nil {
		t.Fatal(err)
	}
	inputs := table.DeparseInputs{Catalog: in.Catalog.WithState(state)}
	c := &exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context()}}
	if value, err := table.Deparse(c, inputs, functions.DeparseTrigger, [3]sqltype.Value{oid}, textType); err == nil || value.Type() != nil {
		t.Fatal("invalid captured authority became a result", value, err)
	}
}
