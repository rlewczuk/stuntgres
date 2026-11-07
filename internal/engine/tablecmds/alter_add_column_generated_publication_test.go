package tablecmds_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func TestAlterAddColumnGeneratedAtomicity(t *testing.T) {
	e, op, ref := addGeneratedUnitFixture(t)
	old, store, snapshot := e.Catalog.State.MustRelation(ref.Address), e.Storage.Relations[ref.Address], e.Catalog.State
	rowID, versionID, frontier := e.Storage.RowFrontier(), e.Storage.VersionFrontier(), e.Visibility.CommitSeq()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(errors.New("unfinished ADD atomicity"))
	expression := alterGenerationOperator("/", alterGenerationLiteral(10), alterGenerationOperator("-", alterGenerationColumn("a"), alterGenerationLiteral(2)))
	_, err = scope.Execute(alterGenerationStatement(addGeneratedUnitAction("b", "stored", expression)), 0, nil)
	enginetest.State(t, err, "22012")
	if finish := scope.Finish(err); finish == nil {
		t.Fatal("failed rewrite accepted")
	}
	if e.Catalog.State != snapshot || e.Storage.Relations[ref.Address] != store || len(old.Columns) != 1 || len(old.ColumnMeta.Generations) != 0 || len(store.Versions) != 2 || len(store.Versions[0].Values) != 1 || e.Storage.RowFrontier() != rowID || e.Storage.VersionFrontier() != versionID || e.Visibility.CommitSeq() != frontier || scope.Owner.Catalog != nil {
		t.Fatal("failed late row escaped private roots")
	}
	enginetest.LockEmpty(t, e)
}

func TestAlterAddColumnGeneratedCancellation(t *testing.T) {
	e, _, ref := addGeneratedUnitFixture(t)
	old, store, snapshot := e.Catalog.State.MustRelation(ref.Address), e.Storage.Relations[ref.Address], e.Catalog.State
	rowID, versionID, frontier := e.Storage.RowFrontier(), e.Storage.VersionFrontier(), e.Visibility.CommitSeq()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, done, err := enginetest.Operation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(errors.New("unfinished cancellation"))
	if _, err := scope.Execute(alterGenerationStatement(addGeneratedUnitAction("b", "stored", alterGenerationOperator("+", alterGenerationColumn("a"), alterGenerationLiteral(1)))), 0, nil); err != nil {
		t.Fatal(err)
	}
	shape := scope.Catalog.Storage[ref.Address]
	if shape.Replacement == store || shape.Replacement.Generation != store.Generation+1 {
		t.Fatal("no actual private rewrite")
	}
	cancel()
	if err := scope.Finish(nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if e.Catalog.State != snapshot || e.Storage.Relations[ref.Address] != store || len(old.Columns) != 1 || len(old.ColumnMeta.Generations) != 0 || len(store.Versions[0].Values) != 1 || e.Storage.RowFrontier() != rowID || e.Storage.VersionFrontier() != versionID || e.Visibility.CommitSeq() != frontier || scope.Owner.Catalog != nil {
		t.Fatal("canceled ADD published roots/definition")
	}
	enginetest.LockEmpty(t, e)
}

func TestAlterAddColumnGeneratedProviderFences(t *testing.T) {
	t.Run("populated-fts", func(t *testing.T) { addGeneratedFTSProviderFences(t, false) })
	t.Run("pruned-original-fts", func(t *testing.T) { addGeneratedFTSProviderFences(t, true) })
	e, _, ref := addGeneratedUnitFixture(t)
	old, store := e.Catalog.State.MustRelation(ref.Address), e.Storage.Relations[ref.Address]
	retained := e.CatalogRead()
	generation := store.Generation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, done, err := enginetest.Operation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	provider := e.Catalog.Providers[ids.RelationClassID]
	defer func() { e.Catalog.Providers[ids.RelationClassID] = provider }()
	entered := false
	e.Catalog.Providers[ids.RelationClassID] = alterDropProvider{provider, func() {
		entered = true
		e.Catalog.Providers[ids.RelationClassID] = provider
		if !e.Mu.TryLock() {
			t.Error("provider callback held Engine.mu")
		} else {
			e.Mu.Unlock()
		}
		if e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != store {
			t.Error("provider observed published candidate")
		}
		cancel()
	}}
	_, err = e.Execute(op, alterGenerationStatement(addGeneratedUnitAction("b", "stored", alterGenerationOperator("+", alterGenerationColumn("a"), alterGenerationLiteral(1)))), 0, nil)
	if !entered || !errors.Is(err, context.Canceled) {
		t.Fatal("ADD did not fence actual provider publication", err)
	}
	if retained.Get(ref.Address) != old || e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != store || store.Generation != generation || len(old.ColumnMeta.Generations) != 0 || len(old.Columns) != 1 {
		t.Fatal("provider veto changed source/installed roots")
	}
	enginetest.LockEmpty(t, e)
}

type addGeneratedConfigImpostor struct{ *catalog.TSConfiguration }

func addGeneratedFTSProviderFences(t *testing.T, pruned bool) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{
		"CREATE TABLE g(title text NOT NULL, description text NOT NULL)",
		"INSERT INTO g VALUES ('Hello world','first post'),('Second','more text')",
	} {
		statements, err := parser.Parse(query)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.Execute(op, statements[0], 0, nil); err != nil {
			t.Fatal(err)
		}
	}
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "g")
	old := e.Catalog.State.MustRelation(ref.Address)
	sourceStore := e.Storage.Relations[ref.Address]
	sourceRows := addGeneratedSnapshotRows(sourceStore.Versions)
	expression := "setweight(to_tsvector('simple'::regconfig, coalesce(title,'')), 'A') || setweight(to_tsvector('simple'::regconfig, coalesce(description,'')), 'B')"
	if pruned {
		expression = "CASE WHEN true THEN ''::tsvector ELSE " + expression + " END"
	}
	statements, err := parser.Parse("ALTER TABLE g ADD search tsvector GENERATED ALWAYS AS (" + expression + ") STORED")
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(errors.New("unfinished FTS provider witness"))
	if _, err := scope.Execute(statements[0], 0, nil); err != nil {
		t.Fatal("real populated FTS candidate rejected", err)
	}
	candidate := scope.Catalog.Descriptors[ref.Address].(*catalog.Relation)
	shape := scope.Catalog.Storage[ref.Address]
	if !shape.Rewritten || shape.Replacement == sourceStore || len(shape.Replacement.Versions) != 2 || candidate.ColumnMeta.Generations[3].OID == 0 {
		t.Fatal("FTS ADD did not retain the real populated rewrite")
	}
	for _, row := range shape.Replacement.Versions {
		if len(row.Values) != 3 || row.Values[2].IsNull() || row.Values[2].Type() != candidate.Columns[2].Usage.Type {
			t.Fatal("FTS rewrite lost its generated payload")
		}
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal("FTS publication rejected the real provider", err)
	}
	view := e.CatalogRead()
	holder := view.MustRelation(ref.Address)
	attribute := holder.Attributes[holder.Index["search"]]
	definition := holder.ColumnMeta.Generations[attribute]
	originalDefinition := definition
	originalDefinition.Dependencies = slices.Clone(definition.Dependencies)
	selected := -1
	var configuration *catalog.TSConfiguration
	for i, dependency := range definition.Dependencies {
		if dependency.Address.ClassID != ids.TSConfigurationClassID {
			continue
		}
		if selected != -1 {
			t.Fatal("repeated regconfig literals were not deduplicated")
		}
		selected = i
		configuration = view.MustTSConfiguration(dependency.Address)
		if dependency.Address != configuration.Address() || dependency.Address.SubID != 0 || dependency.Domain() != view.Domain() || dependency.Epoch != view.Epoch() {
			t.Fatal("FTS manifest lost actual configuration provenance", dependency)
		}
	}
	if selected < 0 || configuration == nil {
		t.Fatal("actual analyzed FTS configuration dependency missing")
	}
	render := func() string {
		t.Helper()
		var dependencies []ids.ObjectRef
		text, err := (stored.ScalarRenderer{Owner: holder, Dependencies: &dependencies}).SQL(op.Context(), view, definition.Expression)
		if err != nil || !reflect.DeepEqual(dependencies, originalDefinition.Dependencies) {
			t.Fatal("original recipe/manifest no longer render together", err, dependencies)
		}
		return text
	}
	originalSQL := render()
	if !strings.Contains(originalSQL, "to_tsvector") {
		t.Fatal("original analyzed FTS tree was replaced by its simplified plan")
	}
	if pruned {
		planned, err := (stored.ScalarRenderer{Owner: holder}).SQL(op.Context(), view, definition.Planned)
		if err != nil || strings.Contains(planned, "to_tsvector") {
			t.Fatal("constant-pruned control did not distinguish original from planned", planned, err)
		}
	}
	if err := table.ValidateGenerationProviders(view, holder); err != nil {
		t.Fatal(err)
	}
	if err := (core.RelationProvider).Validate(view, holder); err != nil {
		t.Fatal("real relation provider rejected bound FTS definition", err)
	}
	withDependency := func(change func(*ids.ObjectRef)) *catalog.Relation {
		bad := *holder
		generation := definition
		generation.Dependencies = slices.Clone(definition.Dependencies)
		change(&generation.Dependencies[selected])
		bad.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{attribute: generation}
		return &bad
	}
	foreignDomain := new(ids.Domain)
	for name, change := range map[string]func(*ids.ObjectRef){
		"subid":           func(r *ids.ObjectRef) { r.Address.SubID = 1 },
		"negative-subid":  func(r *ids.ObjectRef) { r.Address.SubID = -1 },
		"zero-epoch":      func(r *ids.ObjectRef) { r.Epoch = 0 },
		"future-epoch":    func(r *ids.ObjectRef) { r.Epoch = view.Epoch() + 1 },
		"foreign-domain":  func(r *ids.ObjectRef) { *r = foreignDomain.Ref(r.Epoch, r.Address) },
		"missing-address": func(r *ids.ObjectRef) { r.Address.ObjectID = 4294967295 },
	} {
		t.Run(name, func(t *testing.T) {
			bad := withDependency(change)
			if err := table.ValidateGenerationProviders(view, bad); err == nil {
				t.Fatal("forged retained configuration reference admitted")
			}
			if err := (core.RelationProvider).Validate(view, bad); err == nil {
				t.Fatal("relation provider admitted forged FTS manifest")
			}
		})
	}
	t.Run("old-epoch", func(t *testing.T) {
		state := catalog.CloneState(view.State())
		state.SetEpoch(view.Epoch() + 1)
		bad := *holder
		generation := definition
		generation.Dependencies = slices.Clone(definition.Dependencies)
		for i := range generation.Dependencies {
			if i != selected {
				generation.Dependencies[i].Epoch = state.Epoch()
			}
		}
		bad.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{attribute: generation}
		if err := table.ValidateGenerationProviders(e.Catalog.Read(state), &bad); err == nil {
			t.Fatal("configuration gained an old-epoch bridge")
		}
	})
	for name, corrupt := range map[string]func(*catalog.State){
		"retired": func(state *catalog.State) {
			state.RemoveDescriptor(configuration.Address())
		},
		"same-spelled-replacement": func(state *catalog.State) {
			state.RemoveDescriptor(configuration.Address())
			replacement := *configuration
			replacement.ObjectAddress.ObjectID = 4294967294
			state.SetDescriptor(replacement.Address(), &replacement)
			state.BindName(catalog.DescriptorNameKey(&replacement), replacement.Address())
		},
		"wrong-class": func(state *catalog.State) {
			bad := *configuration
			bad.ObjectAddress.ClassID = ids.NamespaceClassID
			state.SetDescriptor(configuration.Address(), &bad)
		},
		"wrong-concrete-kind": func(state *catalog.State) {
			state.SetDescriptor(configuration.Address(), view.MustNamespace(configuration.Namespace()))
		},
		"configuration-impostor": func(state *catalog.State) {
			state.SetDescriptor(configuration.Address(), addGeneratedConfigImpostor{configuration})
		},
		"missing-parser": func(state *catalog.State) {
			state.RemoveDescriptor(configuration.Parser)
		},
		"missing-parser-callback": func(state *catalog.State) {
			parser := *view.MustTSParser(configuration.Parser)
			parser.Start = 0
			state.SetDescriptor(parser.Address(), &parser)
		},
		"parser-argument-signature": func(state *catalog.State) {
			addGeneratedCorruptParserCallback(t, view, state, configuration, false)
		},
		"parser-result-signature": func(state *catalog.State) {
			addGeneratedCorruptParserCallback(t, view, state, configuration, true)
		},
		"missing-dictionary": func(state *catalog.State) {
			if len(configuration.Mappings) == 0 || len(configuration.Mappings[0].Dictionaries) == 0 {
				t.Fatal("real configuration lacks dictionary control")
			}
			state.RemoveDescriptor(configuration.Mappings[0].Dictionaries[0])
		},
	} {
		t.Run(name, func(t *testing.T) {
			state := catalog.CloneState(view.State())
			corrupt(state)
			detached := e.Catalog.Read(state)
			err := table.ValidateGenerationProviders(detached, holder)
			if err == nil {
				t.Fatal("corrupt configuration authority admitted")
			}
			if current, ok := detached.TSConfiguration(configuration.Address()); ok && current != nil && current.Address() == configuration.Address() {
				providerErr := (catalog.TSConfigurationProvider{}).Validate(detached, current)
				if providerErr == nil || providerErr.Error() != err.Error() {
					t.Fatal("shared generation validator did not preserve provider error", err, providerErr)
				}
			}
			if err := (core.RelationProvider).Validate(detached, holder); err == nil {
				t.Fatal("relation provider admitted corrupt configuration authority")
			}
			if view.MustTSConfiguration(configuration.Address()) != configuration || view.MustRelation(ref.Address) != holder || e.CatalogRead().State() != view.State() || !reflect.DeepEqual(holder.ColumnMeta.Generations[attribute], originalDefinition) || render() != originalSQL {
				t.Fatal("rejected candidate mutated installed recipe/providers")
			}
		})
	}
	t.Run("current-configuration-revision", func(t *testing.T) {
		state := catalog.CloneState(view.State())
		current := *configuration
		current.ObjectRevision++
		state.SetDescriptor(current.Address(), &current)
		if err := table.ValidateGenerationProviders(e.Catalog.Read(state), holder); err != nil {
			t.Fatal("valid current configuration revision was frozen to an invented captured revision", err)
		}
	})
	if len(old.ColumnMeta.Generations) != 0 || len(old.Columns) != 2 || !reflect.DeepEqual(sourceStore.Versions, sourceRows) || !reflect.DeepEqual(holder.ColumnMeta.Generations[attribute], originalDefinition) || render() != originalSQL {
		t.Fatal("FTS publication/provider rejection mutated source owners")
	}
}

func addGeneratedCorruptParserCallback(t *testing.T, view catalog.Read, state *catalog.State, configuration *catalog.TSConfiguration, wrongResult bool) {
	t.Helper()
	parser := view.MustTSParser(configuration.Parser)
	address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: parser.Start}
	var procedure catalog.Procedure
	if actual, ok := view.Procedure(address); ok {
		procedure = *actual
	} else {
		actual, ok := catalog.BuiltinRoutineIdentityByOID(parser.Start)
		result, resultOK := catalog.BuiltinRoutineResultByOID(parser.Start)
		if !ok || !resultOK {
			t.Fatal("real parser callback has no released signature")
		}
		procedure = catalog.Procedure{ObjectAddress: address, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: actual.Namespace}, ObjectRevision: 1, ObjectName: actual.Name, Signature: actual.Signature, Result: result}
	}
	if wrongResult {
		procedure.Result = 23
	} else {
		procedure.Signature = "23"
	}
	state.SetDescriptor(address, &procedure)
	state.BindName(catalog.DescriptorNameKey(&procedure), address)
}

func TestAlterAddColumnGeneratedPreparedInheritance(t *testing.T) {
	e, op, parent := addGeneratedUnitFixture(t)
	if _, err := e.Execute(op, &parser.CreateTable{Name: alterGenerationName("child"), Parents: []parser.ObjectName{alterGenerationName("g")}}, 0, nil); err != nil {
		t.Fatal(err)
	}
	insert := &parser.Insert{Name: alterGenerationName("child"), Targets: []parser.InsertTarget{{Name: "a"}}, Rows: [][]parser.Expr{{alterGenerationLiteral(3)}}}
	if _, err := e.Execute(op, insert, 0, nil); err != nil {
		t.Fatal(err)
	}
	child := enginetest.CatalogRef(t, e, e.CatalogRead(), "child")
	oldParent, oldChild := e.Catalog.State.MustRelation(parent.Address), e.Catalog.State.MustRelation(child.Address)
	parentStore, childStore := e.Storage.Relations[parent.Address], e.Storage.Relations[child.Address]
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(errors.New("unfinished recursive ADD"))
	add := addGeneratedUnitAction("b", "stored", alterGenerationOperator("+", alterGenerationColumn("a"), alterGenerationLiteral(1)))
	if _, err := scope.Execute(alterGenerationStatement(add), 0, nil); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []ids.ObjectRef{parent, child} {
		candidate := scope.Catalog.Descriptors[ref.Address].(*catalog.Relation)
		shape := scope.Catalog.Storage[ref.Address]
		source := e.Storage.Relations[ref.Address]
		if len(candidate.Columns) != 2 || candidate.ColumnMeta.Generations[2].Kind != catalog.GenerationStored || candidate.ColumnMeta.Generations[2].OID == 0 || shape.Replacement.Generation != source.Generation+1 {
			t.Fatal("recursive prepared candidate lost recipe/rewrite")
		}
		for _, row := range shape.Replacement.Versions {
			a, ae := row.Values[0].Integer()
			b, be := row.Values[1].Integer()
			if ae != nil || be != nil || b != a+1 {
				t.Fatal(row, ae, be)
			}
		}
	}
	if len(oldParent.Columns) != 1 || len(oldChild.Columns) != 1 || e.Storage.Relations[parent.Address] != parentStore || e.Storage.Relations[child.Address] != childStore {
		t.Fatal("recursive candidate published early")
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	installedParent, installedChild := e.Catalog.State.MustRelation(parent.Address), e.Catalog.State.MustRelation(child.Address)
	if installedChild.Inheritance == nil || !reflect.DeepEqual(oldChild.Attributes, []int32{1}) || installedParent.ColumnMeta.Generations[2].OID == installedChild.ColumnMeta.Generations[2].OID {
		t.Fatal("recursive definition ownership")
	}
}
