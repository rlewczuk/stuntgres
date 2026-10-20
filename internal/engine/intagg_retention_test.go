package engine

import (
	"context"
	"stuntgres/internal/engine/extension"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// These are actual private binding/freezing/staging calls, not SQL execution.
func intaggUnitTemporaryView(t *testing.T, e *Engine, op *Operation, namespace *catalog.Namespace, name, call string) *catalog.Relation {
	t.Helper()
	raw := statement(t, "CREATE TEMP VIEW "+name+" AS SELECT "+namespace.Name()+"."+call+" AS v").(*parser.CreateView)
	stage, consumer, err := func() (*CatalogStage, *catalog.Relation, error) {
		e.mu.Lock()
		defer e.mu.Unlock()
		auth, err := e.authorizationLocked(op)
		if err != nil {
			return nil, nil, err
		}
		b, err := e.describe(op.Context(), raw, auth, ids.ObjectRef{}, sqltype.InputEnvironment{}, nil, nil, nil)
		if err != nil {
			return nil, nil, err
		}
		oid, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return nil, nil, err
		}
		rule, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return nil, nil, err
		}
		revision, err := e.catalog.revisionLocked()
		if err != nil {
			return nil, nil, err
		}
		definition, err := e.freezeViewDefinitionLocked(auth, raw.Query, b, false, false, rule, catalog.ViewRelation, true)
		if err != nil {
			return nil, nil, err
		}
		d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: namespace.Address(), ObjectName: name, ObjectRevision: revision, Owner: namespace.Owner, RelationKind: catalog.ViewRelation, View: viewPayloadOf(definition), Columns: append([]catalog.Column(nil), b.query.Root.Schema()...), Index: map[string]int{"v": 0}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}}
		definition.consumer = d.Address()
		stage := e.BeginCatalogStage(auth.Catalog)
		if err := e.stageRowTypeLocked(stage, d); err != nil {
			e.DiscardCatalogStage(stage)
			return nil, nil, err
		}
		if err := stage.Create(d); err != nil {
			e.DiscardCatalogStage(stage)
			return nil, nil, err
		}
		return stage, d, nil
	}()
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	extensionUnitPublish(t, e, op, stage)
	return consumer
}

func intaggUnitTemporaryDependencies(t *testing.T) {
	e := New()
	op, done := extensionTemporaryOperation(t, e, "postgres")
	defer done()
	namespace := intarrayUnitTemporaryNamespace(t, e, op)
	extensionUnitPublish(t, e, op, intarrayUnitStage(t, e, op, "intagg", namespace.Name()))
	consumers := []*catalog.Relation{
		intaggUnitTemporaryView(t, e, op, namespace, "ia_unit_retained_aggregate", "int_array_aggregate(2)"),
		intaggUnitTemporaryView(t, e, op, namespace, "ia_unit_retained_enum", "int_array_enum('{2,2,NULL}')"),
	}
	before := e.CatalogRead()
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	view := e.CatalogRead()
	if view.Epoch() == before.Epoch() {
		t.Fatal("replacement did not advance epoch")
	}
	for _, consumer := range consumers {
		if view.Get(consumer.Address()) != consumer {
			t.Fatal("TEMP consumer not retained exactly")
		}
		refs := pgDependRefs{ctx: context.Background(), catalog: view}
		refs.query(viewOf(consumer).query.Root)
		if refs.err != nil {
			t.Fatal("retained query dependency collection", refs.err)
		}
		if len(viewOf(consumer).routineRefs) != 1 {
			t.Fatal("missing real captured procedure")
		}
		for path, ref := range viewOf(consumer).routineRefs {
			proc := before.MustProcedure(ref.Address)
			if view.Get(ref.Address) != proc {
				t.Fatal("TEMP procedure descriptor changed")
			}
			if _, exists := refs.addresses[ref.Address]; !exists {
				t.Fatal("missing genuine retained procedure dependency")
			}
			if !storedViewReferenceValid(view, consumer, ref) {
				t.Fatal("exact contextual TEMP authority rejected")
			}
			if _, err := viewOf(consumer).intaggRecipeProcedure(view, path, proc.Name()); err != nil {
				t.Fatal(err)
			}
			if err := e.ValidateObjectRef(view, ref); err == nil {
				t.Fatal("ordinary stale ObjectRef was relaxed")
			}
			if err := extension.ValidateIntaggIdentity(view, proc, ref, proc.Name()); err == nil {
				t.Fatal("fresh intagg identity validator was relaxed")
			}
			if storedViewReferenceValid(New().CatalogRead(), consumer, ref) {
				t.Fatal("foreign engine gained retained authority")
			}
			future := ref
			future.Epoch = view.Epoch() + 1
			if storedViewReferenceValid(view, consumer, future) {
				t.Fatal("future epoch gained retained authority")
			}
			impostor := *proc
			state := catalog.CloneState(view.State())
			state.SetDescriptor(ref.Address, &impostor)
			if storedViewReferenceValid(e.catalog.read(state), consumer, ref) {
				t.Fatal("same-address procedure clone gained captured authority")
			}
			if _, err := viewOf(consumer).intaggRecipeProcedure(e.catalog.read(state), path, proc.Name()); err == nil {
				t.Fatal("recipe accepted substituted procedure descriptor")
			}
			unretained := *consumer
			if storedViewReferenceValid(view, &unretained, ref) {
				t.Fatal("uncaptured consumer clone gained contextual authority")
			}
			unretainedDefinition := *viewOf(consumer)
			if _, err := unretainedDefinition.intaggRecipeProcedure(view, path, proc.Name()); err == nil {
				t.Fatal("recipe borrowed another retained view's authority")
			}
		}
	}
}

func intaggUnitValuesScope(t *testing.T) {
	e, _ := intaggUnitInstalled(t)
	for _, kind := range []string{"placement", "argument", "filter", "input"} {
		t.Run(kind, func(t *testing.T) {
			b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			b.clause.aggregateClause = "INSERT VALUES"
			b.query.hidden = []queryQualifier{{name: "outside"}}
			raw := parser.AggregateExpr{Name: "int_array_aggregate", Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}}}
			want := "42803"
			missing := parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "missing"}}}}
			switch kind {
			case "argument":
				raw.Args[0], want = missing, "42703"
			case "filter":
				raw.Filter, want = missing, "42703"
			case "input":
				raw.Args[0], want = parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "bad"}}, "22P02"
			}
			_, err := b.bind(raw, expressionPolicy{context: projectionContext})
			state, ok := err.(interface{ SQLState() string })
			if !ok || state.SQLState() != want {
				t.Fatal("VALUES precedence", kind, err, want)
			}
			if b.query.namespace != nil || len(b.query.hidden) != 1 || b.query.hidden[0].name != "outside" || len(b.target.uses) != 0 || len(b.grouping.aggregates) != 0 || b.clause.aggregateClause != "INSERT VALUES" {
				t.Fatal("VALUES diagnostic scope leaked into next expression")
			}
		})
	}
}
