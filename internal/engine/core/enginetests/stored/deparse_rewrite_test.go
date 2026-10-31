package stored_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/viewcmds"
	"stuntgres/internal/parser"
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

// Metadata is constructed directly: these units exercise payloads, frames and
// write ownership rather than duplicating the public SQL scenarios.
func triggerUnitFixture(t *testing.T, source string) (*core.Engine, *core.StatementScope, catalog.Read, *catalog.Relation, *catalog.Procedure) {
	t.Helper()
	return triggerUnitFixtureContext(t, context.Background(), source)
}

func triggerUnitDefinition(t *testing.T, read catalog.Read, relation *catalog.Relation, d *catalog.Procedure, name string, event stored.TriggerEvent) stored.Trigger {
	t.Helper()
	p := stored.TriggerParts{OID: 9500010, Name: name, Relation: read.Owner().Domain.Ref(read.Epoch(), relation.Address()), Procedure: read.Owner().Domain.Ref(read.Epoch(), d.Address()), Timing: stored.TriggerBefore, Level: stored.TriggerRow, Events: event, References: []ids.ObjectAddress{d.Address()}}
	definition, err := stored.NewTrigger(p)
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func infoTriggersWhen(t *testing.T, e *core.Engine, scope *core.StatementScope, read catalog.Read, relation *catalog.Relation, definition stored.Trigger) stored.Trigger {
	t.Helper()
	syntax, err := parser.Parse("SELECT NEW.v>0")
	if err != nil {
		t.Fatal(err)
	}
	raw := syntax[0].(*parser.Select).Targets[0]
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(scope.Op)
	var condition expr.Expr
	var query *parser.Select
	var binding analyze.Binding
	if err == nil {
		condition, query, binding, err = (viewcmds.ViewHost{tablecmds.TableHost{core.DDLHost{E: e, Op: scope.Op}}}).BindTriggerWhen(auth, &analyze.RuleNamespace{Relation: relation, Ref: read.Owner().Domain.Ref(read.Epoch(), relation.Address()), Event: "UPDATE", Trigger: true}, raw, scope.Input)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	p := definition.Parts()
	p.When = condition
	p.Binding = &binding
	p.Presentation, err = stored.FreezeQueryPresentation(read, ids.SessionToken{}, query, binding, false, false, 0, "", false)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := stored.ExpressionDependRefs(scope.Op.Context(), read, condition)
	if err != nil {
		t.Fatal(err)
	}
	p.References = append(p.References, refs...)
	definition, err = stored.NewTrigger(p)
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func triggerUnitInstall(t *testing.T, e *core.Engine, relation *catalog.Relation, definitions ...stored.Trigger) *catalog.Relation {
	t.Helper()
	copy := *relation
	copy.Triggers = stored.TriggersPayload(definitions)
	e.Mu.Lock()
	next := catalog.CloneState(e.Catalog.State)
	next.SetDescriptor(copy.Address(), &copy)
	e.Catalog.PublishLocked(next)
	e.Mu.Unlock()
	return &copy
}

// A deterministic cancellation point inside enumeration, without timing races.
type dtpCancelContext struct {
	context.Context
	remaining int
}

func (c *dtpCancelContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

func triggerUnitFixtureContext(t *testing.T, ctx context.Context, source string) (*core.Engine, *core.StatementScope, catalog.Read, *catalog.Relation, *catalog.Procedure) {
	t.Helper()
	e := engine.New()
	read := e.CatalogRead()
	namespace := read.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	owner := e.Roles().LookupRef("postgres")
	integer, _ := read.LookupType(sqltype.Int4)
	shape, err := sqltype.Composite(9500002, "trigger_unit_table", "public.trigger_unit_table", 0, []sqltype.CompositeMember{{Attribute: 1, Name: "id", Usage: integer.Usage()}, {Attribute: 2, Name: "v", Usage: integer.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 9500001}, ObjectNamespace: namespace, ObjectName: "trigger_unit_table", ObjectRevision: 1, RelationKind: catalog.TableRelation, Owner: owner, RowType: shape.ID(), Attributes: []int32{1, 2}, Index: map[string]int{"id": 0, "v": 1}, Columns: []catalog.Column{{Name: "id", Usage: integer.Usage(), Nullable: true}, {Name: "v", Usage: integer.Usage(), Nullable: true}}}
	relation.TableMethod = 2
	relation.ColumnMeta.HighWater = 2
	rowtype := &catalog.Type{SQLType: shape, ObjectNamespace: namespace, ObjectRevision: 1, Owner: owner, OwningRelation: relation.Address()}
	e.Mu.Lock()
	next := catalog.CloneState(e.Catalog.State)
	next.SetDescriptor(relation.Address(), relation)
	next.BindName(catalog.DescriptorNameKey(relation), relation.Address())
	next.SetDescriptor(rowtype.Address(), rowtype)
	next.BindName(catalog.DescriptorNameKey(rowtype), rowtype.Address())
	e.Catalog.PublishLocked(next)
	e.Storage.Relations[relation.Address()] = &storage.Relation{Generation: 1}
	e.Mu.Unlock()
	op, done, err := enginetest.Operation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(done)
	s, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Finish(nil) })
	trigger, _ := e.CatalogRead().LookupType(2279)
	d := triggerUnitProcedure(t, e, s, "trigger_unit_fn", 9500003, trigger, proc.DMLTriggerInvocation, source)
	return e, s, e.CatalogRead(), relation, d
}

func triggerUnitProcedure(t *testing.T, e *core.Engine, s *core.StatementScope, name string, oid ids.OID, result *sqltype.Descriptor, invocation proc.InvocationKind, source string) *catalog.Procedure {
	t.Helper()
	read := e.CatalogRead()
	language, err := catalog.LookupLanguage(read, "plpgsql")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	namespace := read.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	d := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: namespace, ObjectName: name, ObjectRevision: 1, Owner: e.Roles().LookupRef("postgres"), Language: language.Address(), RoutineKind: catalog.FunctionRoutine, Result: ids.OID(result.ID()), Volatility: expr.Volatile, Parallel: 'u', Cost: 100, SQL: &proc.Definition{Kind: proc.PLpgSQLBody, Invocation: invocation, Name: name, Source: source, Result: result.Usage(), PLSyntax: raw}}
	if err := d.SQL.SetFormals(nil); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(s.Op)
	if err == nil {
		err = e.CompilePLRoutineLocked(s.Op, auth, d)
		s.RoutineAuth = auth
	}
	if err == nil {
		next := catalog.CloneState(e.Catalog.State)
		// Direct fixture publication must maintain the same overload-candidate
		// index as catalog candidate application, not only exact name lookup.
		index := catalog.ProcedureIndexEdit{Index: next.ProcedureNames()}
		index.Replace(next.Get(d.Address()), d)
		next.SetProcedureNames(index.Index)
		next.SetDescriptor(d.Address(), d)
		next.BindName(catalog.DescriptorNameKey(d), d.Address())
		e.Catalog.PublishLocked(next)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return d
}
