package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/workadmission"
)

const sepgsqlRuntimeGeneration = "CASE WHEN payload IS NULL THEN abs(-3) ELSE json_array_length(json_strip_nulls(payload)) END"

// The descriptor, rows, expression manifests and session are all genuine SQL
// products. The direct table owner probes borrow a real admitted statement;
// they never install receipts or turn a copied descriptor into positive proof.
func sepgsqlGenerationRuntimeFixture(t *testing.T, mode string) (*core.Engine, *session.Operation, *catalog.Relation) {
	t.Helper()
	e, _ := sepgsqlGenerationEngine(t)
	var op *session.Operation
	if mode == "import" {
		e, op = sepgsqlTrustedReplayOperation(t, e)
	} else {
		var done func()
		op, done = enginetest.RoleOperation(t, e, "postgres")
		t.Cleanup(done)
	}
	persistence := ""
	if mode == "retained_temp" {
		persistence = "TEMP "
	}
	enginetest.RoleExecute(t, e, op, "CREATE "+persistence+"TABLE generation_runtime(id integer,payload json,a integer GENERATED ALWAYS AS("+sepgsqlRuntimeGeneration+") STORED,b integer GENERATED ALWAYS AS(abs(-5)) STORED,c integer GENERATED ALWAYS AS("+sepgsqlRuntimeGeneration+") STORED)")
	var d *catalog.Relation
	for _, descriptor := range e.CatalogRead().Descriptors() {
		if r, ok := descriptor.(*catalog.Relation); ok && r.Name() == "generation_runtime" {
			d = r
		}
	}
	if d == nil {
		t.Fatal("missing actual generation runtime descriptor")
	}
	sepgsqlInitializationCommand(t, e, op, "INSERT INTO generation_runtime(id,payload) VALUES(1,NULL),(2,'[1,2]')", nil, func(scope *core.StatementScope) {
		sepgsqlGenerationRuntimeSites(t, scope, d, 2)
		if mode == "import" {
			sepgsqlAssertImportInitialization(t, e, scope)
		} else {
			sepgsqlGenerationLiveReceipts(t, e, scope)
		}
	})
	if mode == "retained_temp" {
		epoch := e.CatalogRead().Epoch()
		if _, err := e.ReplaceCatalog(t.Context(), e, e.CatalogRead()); err != nil {
			t.Fatal("genuine retained TEMP replacement", err)
		}
		if e.CatalogRead().Epoch() <= epoch || e.CatalogRead().Get(d.Address()) != d {
			t.Fatal("replacement did not retain original TEMP descriptor in a new epoch")
		}
		oldReference := false
		for _, ref := range d.ColumnMeta.Generations[3].Dependencies {
			oldReference = oldReference || ref.Epoch < e.CatalogRead().Epoch()
		}
		if !oldReference {
			t.Fatal("retained TEMP witness has no genuine historical generation reference")
		}
	}
	return e, op, d
}

// Receipts for the two independently declared JSON callsites share the actual
// execution owner, not each other's original source or relation consumer.
func sepgsqlGenerationRuntimeSites(t *testing.T, scope *core.StatementScope, consumer *catalog.Relation, wantSources int) {
	t.Helper()
	owners := map[uint64]bool{}
	sources := map[[3]uint64]bool{}
	calls := reflect.ValueOf(scope).Elem().FieldByName("routineInitializations").MapRange()
	for calls.Next() {
		call := calls.Key()
		site := call.FieldByName("Site")
		address := site.FieldByName("Reference").FieldByName("Address")
		if address.FieldByName("ObjectID").Uint() != 3956 || site.FieldByName("Consumer").IsNil() {
			continue
		}
		if site.FieldByName("Consumer").Pointer() != reflect.ValueOf(consumer).Pointer() {
			t.Fatal("stored generation borrowed a prospective or foreign consumer")
		}
		owner := call.FieldByName("Owner")
		owners[owner.FieldByName("instance").Uint()] = true
		source := site.FieldByName("Source")
		sources[[3]uint64{source.FieldByName("Frame").Uint(), uint64(source.FieldByName("Position").FieldByName("Character").Int()), site.FieldByName("Occurrence").Uint()}] = true
	}
	if len(owners) != 1 || owners[0] || len(sources) != wantSources {
		t.Fatal("generation runtime lost same-owner/distinct-source preparation", owners, sources)
	}
}

func sepgsqlGenerationRuntimeBinding(t *testing.T, e *core.Engine, source *session.Operation) (*session.Operation, *core.StatementScope, *exec.Context) {
	t.Helper()
	op, err := source.Session.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if core.OperationReplayOrZero(source).StagingEffects {
		core.OperationReplay(op).StagingEffects = true
	}
	t.Cleanup(func() { core.OperationReplay(op).StagingEffects = false; op.Done() })
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(nil) })
	if err = scope.Gate(enginetest.Statement(t, "INSERT INTO generation_runtime(id,payload) VALUES(3,'[1,2]')")); err != nil {
		t.Fatal(err)
	}
	var auth core.AuthorizationView
	func() {
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth, err = e.AuthorizationLocked(op)
		if err == nil {
			err = scope.CaptureIdentityLocked(auth)
			scope.RoutineAuth = auth
		}
	}()
	if err != nil {
		t.Fatal(err)
	}
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: scope.ActivateRuntime(op.Context()), Host: core.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth}}
	return op, scope, evaluation
}

func sepgsqlGenerationSourceRow(t *testing.T, e *core.Engine, d *catalog.Relation, id int64) []sqltype.Value {
	t.Helper()
	for _, row := range e.Storage.Relations[d.Address()].Versions {
		if n, err := row.Values[0].Integer(); err == nil && n == id {
			return append([]sqltype.Value(nil), row.Values...)
		}
	}
	t.Fatal("missing genuine runtime source row", id)
	return nil
}

func sepgsqlGenerationRuntimeOwner(t *testing.T, mode string) {
	t.Helper()
	e, source, d := sepgsqlGenerationRuntimeFixture(t, mode)
	enginetest.RoleExecute(t, e, source, "CREATE TABLE generation_virtual_unused(payload json,g integer GENERATED ALWAYS AS(json_array_length(payload)) VIRTUAL)")
	virtualRef := enginetest.CatalogRef(t, e, e.CatalogRead(), "generation_virtual_unused")
	virtual := e.CatalogRead().MustRelation(virtualRef.Address)
	op, scope, evaluation := sepgsqlGenerationRuntimeBinding(t, e, source)
	previousContext := op.Ctx
	work := 0
	op.Ctx = workadmission.With(op.Context(), func(_ context.Context, class workadmission.Class) error {
		if class == workadmission.WorkJSONStripNulls {
			work++
		}
		return nil
	})
	defer func() { op.Ctx = previousContext }()
	evaluation.Context = scope.ActivateRuntime(op.Context())
	if evaluation.RoutineInitialization.Active() {
		t.Fatal("runtime fixture supplied a fabricated initialization owner")
	}
	if _, err := table.PrepareGenerationProgram(evaluation, virtual, nil, virtual); err != nil || evaluation.RoutineInitialization.Active() || len(evaluation.PreparedGenerations) != 0 {
		t.Fatal("virtual-only program minted or cached a stored initializer", err)
	}
	if _, err := table.PrepareGenerationProgram(evaluation, d, []int{0}, d); err != nil || evaluation.RoutineInitialization.Active() || len(evaluation.PreparedGenerations) != 0 {
		t.Fatal("unused stored slots minted or cached an initializer", err)
	}
	if _, err := table.PrepareGenerationProgram(evaluation, d, nil, d); err != nil {
		t.Fatal("first demanded retained generation preparation", err)
	}
	owner := evaluation.RoutineInitialization
	if !owner.Active() {
		t.Fatal("generation preparation lost its parent owner")
	}
	sepgsqlGenerationRuntimeSites(t, scope, d, 2)
	receipts := reflect.ValueOf(scope).Elem().FieldByName("routineInitializations").Len()
	owners := reflect.ValueOf(scope).Elem().FieldByName("routineInitializationOwners").Len()
	sequence := reflect.ValueOf(e).Elem().FieldByName("routineInitializationSequence").Uint()
	assertUnchanged := sepgsqlGenerationNoEffects(e)
	for _, id := range []int64{1, 2, 2, 1} {
		if _, err := table.PrepareGenerationProgram(evaluation, d, nil, d); err != nil {
			t.Fatal("same-owner generation cache hit", err)
		}
		row := sepgsqlGenerationSourceRow(t, e, d, id)
		beforeWork := work
		if err := table.EvaluateGenerations(evaluation.Context, d, row, evaluation); err != nil {
			t.Fatal("retained generation row invocation", err)
		}
		if id == 2 && work == beforeWork {
			t.Fatal("retained generation row did not demand real JSON stripping")
		}
		want := int64(2)
		if id == 1 {
			want = 3
		}
		for slot, expected := range map[int]int64{2: want, 3: 5, 4: want} {
			if got, err := row[slot].Integer(); err != nil || got != expected {
				t.Fatal("retained generation value", slot, got, expected, err)
			}
		}
		if evaluation.RoutineInitialization != owner || reflect.ValueOf(scope).Elem().FieldByName("routineInitializations").Len() != receipts || reflect.ValueOf(scope).Elem().FieldByName("routineInitializationOwners").Len() != owners || reflect.ValueOf(e).Elem().FieldByName("routineInitializationSequence").Uint() != sequence {
			t.Fatal("generation cache/row invocation reminted initialization")
		}
		if evaluation.RoutineRecipe || evaluation.RoutineGraft || evaluation.RoutineChecks != nil || evaluation.RoutineReferences != nil || evaluation.RoutineConsumer != nil {
			t.Fatal("generation row invocation mutated its enclosing authority")
		}
	}
	assertUnchanged(t)
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	sepgsqlInitializationCommand(t, e, source, "UPDATE generation_runtime SET payload='[1,2]' WHERE id=1", nil, func(scope *core.StatementScope) {
		sepgsqlGenerationRuntimeSites(t, scope, d, 2)
	})
	enginetest.RoleExecute(t, e, source, "PREPARE generation_runtime_update(json,integer) AS UPDATE generation_runtime SET payload=$1 WHERE id=$2")
	sepgsqlInitializationCommand(t, e, source, "EXECUTE generation_runtime_update(NULL,2)", nil, func(scope *core.StatementScope) {
		sepgsqlGenerationRuntimeSites(t, scope, d, 2)
	})
	sepgsqlPhaseRows(t, e, source, "SELECT id,a,b,c FROM generation_runtime WHERE id=2", [][]driver.Value{{int64(2), int64(3), int64(5), int64(3)}})
	sepgsqlInitializationCommand(t, e, source, "EXECUTE generation_runtime_update('[1,2]',2)", nil, func(scope *core.StatementScope) {
		sepgsqlGenerationRuntimeSites(t, scope, d, 2)
	})
	enginetest.RoleExecute(t, e, source, "DEALLOCATE generation_runtime_update")
	sepgsqlPhaseRows(t, e, source, "SELECT id,a,b,c FROM generation_runtime ORDER BY id", [][]driver.Value{{int64(1), int64(2), int64(5), int64(2)}, {int64(2), int64(2), int64(5), int64(2)}})
}

func sepgsqlGenerationRuntimeReject(t *testing.T, invalid string) {
	t.Helper()
	mode := "live"
	if invalid == "origin" || invalid == "staging" || invalid == "import_seal" {
		mode = "import"
	}
	if invalid == "prospective_consumer" {
		mode = "retained_temp"
	}
	e, source, d := sepgsqlGenerationRuntimeFixture(t, mode)
	op, scope, evaluation := sepgsqlGenerationRuntimeBinding(t, e, source)
	// Prepare only a. The same procedure in c has its own original source
	// occurrence, which this genuine owner has not initialized.
	program, err := table.PrepareGenerationProgram(evaluation, d, []int{2}, d)
	if err != nil {
		t.Fatal(err)
	}
	definition := d.ColumnMeta.Generations[3]
	node := table.GenerationRecipeExpression(definition)
	if program != nil {
		node = program[2]
	}
	recipe := *evaluation
	recipe.RoutineRecipe, recipe.RoutineGraft = true, false
	recipe.RoutineChecks = nil
	recipe.RoutineReferences = definition.Dependencies
	recipe.RoutineConsumer = d
	recipe.Row = sepgsqlGenerationSourceRow(t, e, d, 2)
	leaf := sepgsqlGenerationJSONLeaf(t, definition)
	site := sepgsqlInitializationSite(t, &recipe, leaf)
	site.Consumer, site.FollowIdentity = d, true
	host := core.HostOf(scope)
	auth, dependencies, err := exec.RoutineAdmissionAuthority(&recipe)
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.CheckRecipeRoutineDescriptor(&recipe, auth, dependencies, site.Routine); err != nil {
		t.Fatal("genuine original manifest rejected", err)
	}
	if err := host.ValidateRoutineInitializationOwner(recipe.RoutineInitialization, recipe.Catalog); err != nil {
		t.Fatal("genuine prepared owner rejected", err)
	}
	if _, err := host.ValidateRoutineInitialization(recipe.RoutineInitialization, site); err != nil {
		t.Fatal("genuine completed source rejected", err)
	}
	work := 0
	previousContext := op.Ctx
	op.Ctx = workadmission.With(op.Context(), func(_ context.Context, class workadmission.Class) error {
		if class == workadmission.WorkJSONStripNulls {
			work++
		}
		return nil
	})
	defer func() { op.Ctx = previousContext }()
	// With attaches an inert request policy. The genuine unlocked statement
	// owner, rather than a raw expression context, activates runtime admission.
	recipe.Context = scope.ActivateRuntime(op.Context())
	// Raw original INTERNAL kernels deliberately have no extra FHET/receipt
	// entry check. Prove this selected node/row really reaches its kernel, then
	// exercise the manifest or completed-site boundary which promises rejection.
	value, err := node.Eval(&recipe)
	if err != nil {
		t.Fatal("untampered retained runtime source", err)
	}
	got, err := value.Integer()
	if err != nil || got != 2 || work == 0 {
		t.Fatal("negative fixture did not demand its real JSON kernel", got, work, err)
	}
	work = 0
	manifest := false
	message := "stuntgres: missing initialized routine callsite"
	cause := errors.New("retained generation source canceled")
	switch invalid {
	case "missing_refs":
		recipe.RoutineReferences = nil
		manifest, message = true, "stuntgres: empty owning routine lifetime manifest"
	case "other_column_refs":
		recipe.RoutineReferences = d.ColumnMeta.Generations[4].Dependencies
		manifest, message = true, "stuntgres: missing stored routine lifetime manifest"
	case "other_column_source":
		site = sepgsqlInitializationSite(t, &recipe, sepgsqlGenerationJSONLeaf(t, d.ColumnMeta.Generations[5]))
		site.Consumer, site.FollowIdentity = d, true
	case "foreign_consumer":
		_, _, foreign := sepgsqlGenerationRuntimeFixture(t, "live")
		site.Consumer = foreign
	case "prospective_consumer":
		prospective := *d
		site.Consumer = &prospective
	case "other_command":
		other, release := enginetest.RoleOperation(t, e, "postgres")
		t.Cleanup(release)
		_, _, otherEvaluation := sepgsqlGenerationRuntimeBinding(t, e, other)
		if _, err := table.PrepareGenerationProgram(otherEvaluation, d, []int{2}, d); err != nil {
			t.Fatal(err)
		}
		recipe.RoutineInitialization = otherEvaluation.RoutineInitialization
		message = "stuntgres: missing actual routine initialization owner"
	case "origin":
		restore := core.SubstituteRestoreInitializationOrigin(e, nil, "copy")
		defer restore()
		message = "stuntgres: missing actual routine initialization owner"
	case "staging":
		core.OperationReplay(op).StagingEffects = false
		defer func() { core.OperationReplay(op).StagingEffects = true }()
		message = "stuntgres: missing actual routine initialization policy"
	case "finished":
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		message = "stuntgres: inactive statement scope"
	case "canceled":
		op.Cancel(cause)
	}
	assertUnchanged := sepgsqlGenerationNoEffects(e)
	sequence := reflect.ValueOf(e).Elem().FieldByName("routineInitializationSequence").Uint()
	receipts := reflect.ValueOf(scope).Elem().FieldByName("routineInitializations").Len()
	if manifest {
		err = exec.CheckRecipeRoutineDescriptor(&recipe, auth, dependencies, site.Routine)
	} else if invalid == "import_seal" {
		_, err = host.SealRoutineInitialization(recipe.RoutineInitialization, recipe.Catalog)
	} else {
		_, err = host.ValidateRoutineInitialization(recipe.RoutineInitialization, site)
	}
	if err == nil {
		t.Fatal("invalid stored source passed its actual admission boundary", invalid)
	}
	if invalid == "canceled" {
		if err != context.Canceled || context.Cause(op.Context()) != cause {
			t.Fatal("stored source lost exact cancellation cause", err, context.Cause(op.Context()))
		}
	} else if invalid == "import_seal" {
		if !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("stored import seal rejected at an unrelated boundary", err)
		}
	} else if err.Error() != message {
		t.Fatal("stored source rejected at an unrelated boundary", invalid, err)
	}
	if work != 0 || reflect.ValueOf(e).Elem().FieldByName("routineInitializationSequence").Uint() != sequence || reflect.ValueOf(scope).Elem().FieldByName("routineInitializations").Len() != receipts {
		t.Fatal("invalid stored source entered kernel or minted authority", invalid, work)
	}
	assertUnchanged(t)
}

func sepgsqlGenerationJSONLeaf(t *testing.T, definition catalog.ColumnGeneration) expr.Expr {
	t.Helper()
	var selected expr.Expr
	count := 0
	plan.WalkExpression(table.GenerationRecipeExpression(definition), func(node expr.Expr) {
		routine, err := exec.ExpressionRoutineDescriptor(node)
		if err != nil {
			t.Fatal(err)
		}
		if routine != nil && routine.Name() == "json_array_length" && routine.Signature == "114" {
			selected = node
			count++
		}
	})
	if count != 1 {
		t.Fatal("missing unique genuinely selected JSON routine leaf", count)
	}
	return selected
}

func sepgsqlGenerationPhysicalSource(t *testing.T, e *core.Engine, op *session.Operation) (*core.StatementScope, core.Result, *exec.Source) {
	t.Helper()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	raw := enginetest.Statement(t, "SELECT id FROM generation_runtime ORDER BY id")
	result, err := scope.Execute(raw, 0, nil)
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	var captured *exec.Source
	t.Cleanup(func() {
		if result.Rows != nil {
			if err := result.Rows.Close(); err != nil {
				t.Error(err)
			}
		} else if captured != nil {
			captured.Close()
		}
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	})
	header := core.ScopeAuthority(scope)
	if header == nil || !header.RoutineInitialization.Active() || header.RoutineCatalog.Owner() == nil || header.RoutineCatalog.State() == nil || result.Rows == nil {
		t.Fatal("missing genuine prepared command header")
	}
	owner, read := header.RoutineInitialization, header.RoutineCatalog
	sequence := reflect.ValueOf(e).Elem().FieldByName("routineInitializationSequence").Uint()
	receipts := reflect.ValueOf(scope).Elem().FieldByName("routineInitializations").Len()
	assertHeader := func() {
		t.Helper()
		if core.ScopeAuthority(scope) != header || header.RoutineInitialization != owner || header.RoutineCatalog != read || reflect.ValueOf(e).Elem().FieldByName("routineInitializationSequence").Uint() != sequence || reflect.ValueOf(scope).Elem().FieldByName("routineInitializations").Len() != receipts {
			t.Fatal("physical capture changed the original prepared owner/catalog")
		}
	}
	// Eager FinalRows has deliberately detached every live source header.
	// Capture a new physical prefix through the real builder, borrowing this
	// still-admitted command's SAME prepared leaves and header, not its output.
	if err := result.Rows.Close(); err != nil {
		t.Fatal(err)
	}
	result.Rows = nil
	var binding analyze.Binding
	func() {
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth, failure := e.AuthorizationLocked(op)
		err = failure
		if err == nil {
			binding, err = core.Describe(op.Context(), e.AnalysisHost(op), raw, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
		}
		if err == nil {
			assertHeader()
			captured, err = e.CaptureQuerySourcesLocked(scope, scope.QuerySources, false)
		}
	}()
	if err != nil {
		t.Fatal(err)
	}
	assertHeader()
	physical := captured
	for physical.View == nil && physical.Input != nil {
		physical = physical.Input
	}
	if physical.View == nil || physical.Authority != header {
		t.Fatal("missing genuine captured physical source/header")
	}
	view := physical.View.Catalog()
	if view.Owner() != read.Owner() || view.Domain() != read.Domain() || view.Epoch() != read.Epoch() {
		t.Fatal("physical visibility capture lost the prepared catalog")
	}
	ctx := scope.ActivateRuntime(op.Context())
	if err := exec.SelectQuerySources(ctx, captured); err != nil {
		t.Fatal(err)
	}
	// This live producer owns the newly captured prefix. Restore its header
	// after the probes and drain this very producer before ending the command.
	result.Rows, err = exec.OpenNestedQueryRows(ctx, binding.Query.Root, captured, nil)
	captured = nil // OpenNestedQueryRows owns cleanup on success and failure.
	if err != nil {
		t.Fatal(err)
	}
	assertHeader()
	return scope, result, physical
}

func sepgsqlGenerationSourceHeader(t *testing.T) {
	t.Helper()
	e, op, _ := sepgsqlGenerationRuntimeFixture(t, "live")
	scope, result, physical := sepgsqlGenerationPhysicalSource(t, e, op)
	foreignEngine, foreignOp, _ := sepgsqlGenerationRuntimeFixture(t, "live")
	_, _, foreign := sepgsqlGenerationPhysicalSource(t, foreignEngine, foreignOp)
	header := physical.Authority
	owner, read := header.RoutineInitialization, header.RoutineCatalog
	defer func() { physical.Authority = header }()
	if header != core.ScopeAuthority(scope) || !header.RoutineInitialization.Active() {
		t.Fatal("physical source did not borrow the exact prepared command header")
	}
	state := exec.State{Source: physical}
	frame := state.Evaluation(op.Context())
	if err := exec.EvaluationContext(&frame); err != nil || frame.RoutineInitialization != header.RoutineInitialization || frame.Catalog != header.RoutineCatalog {
		t.Fatal("physical source lost its explicit prepared owner/catalog", err)
	}
	if err := core.HostOf(scope).ValidateRoutineInitializationOwner(frame.RoutineInitialization, frame.Catalog); err != nil {
		t.Fatal("physical source header has no genuine live command owner", err)
	}
	foreignState := exec.State{Source: foreign}
	foreignFrame := foreignState.Evaluation(foreignOp.Context())
	if err := exec.EvaluationContext(&foreignFrame); err != nil {
		t.Fatal(err)
	}
	sequence := reflect.ValueOf(e).Elem().FieldByName("routineInitializationSequence").Uint()
	receipts := reflect.ValueOf(scope).Elem().FieldByName("routineInitializations").Len()
	assertUnchanged := sepgsqlGenerationNoEffects(e)
	for _, invalid := range []string{"missing", "foreign", "conflicting_owner", "conflicting_catalog"} {
		physical.Authority = header
		state := exec.State{Source: physical}
		message := ""
		switch invalid {
		case "missing":
			physical.Authority = nil
			message = "stuntgres: missing explicit routine source initialization owner"
		case "foreign":
			physical.Authority = foreign.Authority
		case "conflicting_owner":
			state.RoutineInitialization = foreign.Authority.RoutineInitialization
			message = "stuntgres: conflicting routine source initialization owners"
		case "conflicting_catalog":
			state.Outer = &foreignFrame
		}
		frame := state.Evaluation(op.Context())
		err := exec.EvaluationContext(&frame)
		if err == nil || message != "" && err.Error() != message || message == "" && !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("source opening adopted absent/foreign/conflicting authority", invalid, err)
		}
	}
	physical.Authority = header
	if reflect.ValueOf(e).Elem().FieldByName("routineInitializationSequence").Uint() != sequence || reflect.ValueOf(scope).Elem().FieldByName("routineInitializations").Len() != receipts {
		t.Fatal("source-opening rejection minted an ambient initializer")
	}
	assertUnchanged(t)
	var got [][]driver.Value
	values := make([]sqltype.Value, len(result.Rows.Schema()))
	for {
		present, err := result.Rows.Next(scope.ActivateRuntime(op.Context()), values)
		if err != nil {
			t.Fatal("healthy captured producer failed after source guard probes", err)
		}
		if !present {
			break
		}
		got = append(got, []driver.Value{enginetest.Export(values[0])})
	}
	if !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}, {int64(2)}}) {
		t.Fatal("source guard probes corrupted the healthy captured producer", got)
	}
	if physical.Authority != header || core.ScopeAuthority(scope) != header || header.RoutineInitialization != owner || header.RoutineCatalog != read || reflect.ValueOf(e).Elem().FieldByName("routineInitializationSequence").Uint() != sequence || reflect.ValueOf(scope).Elem().FieldByName("routineInitializations").Len() != receipts {
		t.Fatal("source probes or healthy drain changed the prepared owner/catalog")
	}
	assertUnchanged(t)
}
