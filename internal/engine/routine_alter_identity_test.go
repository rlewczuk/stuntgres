package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func routineAlterBuiltin(t *testing.T, e *core.Engine) *catalog.Procedure {
	t.Helper()
	view := e.CatalogRead()
	for _, address := range view.State().ProceduresNamed(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, "abs") {
		p := view.MustProcedure(address)
		if p.Signature == "23" {
			return p
		}
	}
	t.Fatal("missing released integer abs")
	return nil
}

func TestRoutineAlterDeclarationIdentity(t *testing.T) {
	e := New()
	captured := routineAlterBuiltin(t, e)
	current := *captured
	current.ObjectName = "renamed_abs"
	current.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	current.ObjectRevision++
	if !exec.SameRoutineDeclaration(&current, captured) {
		t.Fatal("mutable labels/revision changed declaration identity")
	}
	for _, mutate := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.ObjectAddress.SubID = 1 },
		func(p *catalog.Procedure) { p.ObjectAddress.ObjectID++ },
		func(p *catalog.Procedure) { p.Signature = "20" },
		func(p *catalog.Procedure) { p.RoutineKind = catalog.WindowRoutine },
		func(p *catalog.Procedure) { p.Result = 20 },
		func(p *catalog.Procedure) { p.Builtin = nil },
		func(p *catalog.Procedure) { p.Volatility = expr.Stable },
		func(p *catalog.Procedure) { p.Strict = !p.Strict },
		func(p *catalog.Procedure) { p.Foreign = &catalog.ForeignProcedure{} },
		func(p *catalog.Procedure) {
			p.Extension = ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 99001}
		},
	} {
		forged := current
		mutate(&forged)
		if exec.SameRoutineDeclaration(&forged, captured) {
			t.Fatal("forged declaration accepted", forged)
		}
	}
	if exec.SameRoutineDeclaration(nil, captured) || exec.SameRoutineDeclaration(&current, nil) {
		t.Fatal("nil declaration accepted")
	}
	bodyAddress := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 1811}
	body := e.CatalogRead().MustProcedure(bodyAddress)
	freshBody := New().CatalogRead().MustProcedure(bodyAddress)
	if len(body.Body) == 0 || !exec.SameRoutineDeclaration(freshBody, body) {
		t.Fatal("equivalent bootstrap body links depend on allocator pointers")
	}
	forgedBody := *body
	forgedBody.Body = append([]catalog.RoutineBodyEdge(nil), body.Body...)
	forgedBody.Body[0].Routine = captured
	if exec.SameRoutineDeclaration(&forgedBody, body) {
		t.Fatal("forged immutable body link accepted")
	}
}

func TestRoutineAlterProviderOverrideIndex(t *testing.T) {
	e := New()
	base := e.CatalogRead()
	original := routineAlterBuiltin(t, e)
	if base.State().HasProviderProcedureOverride("abs") {
		t.Fatal("unchanged builtin requires provider override")
	}
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	reader := &sqlRoutineLookupReader{sqlRoutineLookupSource: core.StageDefinitions(stage)}
	renamed := *original
	renamed.ObjectName = "routine_alter_abs"
	renamed.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	renamed.ObjectRevision++
	stage.UpdateDescriptor(&renamed)
	for _, name := range []string{"abs", renamed.Name()} {
		if !reader.HasProviderProcedureOverride(name) {
			t.Fatal("lost staged provider spelling", name)
		}
	}
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	binderOperation(b).Definition = reader
	raw := statement(t, "SELECT public.routine_alter_abs(-4::integer)").(*parser.Select).Targets[0].(parser.FunctionExpr)
	node, handled, err := b.BindSQLCallable(raw)
	if err != nil || !handled {
		t.Fatal("renamed provider lookup", handled, err)
	}
	math, ok := node.(functions.Math)
	if !ok || math.Plan.Name() != "abs" || math.RoutineReference != &renamed {
		t.Fatal("public label replaced executable identity", node)
	}
	edit := catalog.ProcedureIndexEdit{Index: base.State().ProcedureNames()}
	edit.Replace(original, &renamed)
	if !edit.Index.ProviderOverrides[original.Name()] || !edit.Index.ProviderOverrides[renamed.Name()] {
		t.Fatal("committed index lost source/destination override")
	}
	if base.State().HasProviderProcedureOverride(original.Name()) || base.State().HasProviderProcedureOverride(renamed.Name()) || base.Get(original.Address()) != original {
		t.Fatal("COW index changed retained root")
	}
	for _, address := range edit.Index.Buckets[catalog.ProcedureNameKey{Namespace: original.Namespace(), Name: original.Name()}] {
		if address == original.Address() {
			t.Fatal("vacated signature remains discoverable")
		}
	}
}

func TestRoutineAlterRevisionAtomicity(t *testing.T) {
	e := New()
	original := routineAlterBuiltin(t, e)
	before := e.CatalogRead()
	e.Catalog.InstallRevisionFrontierLocked(ids.DefinitionRevision(^uint64(0)))
	oid := e.Catalog.NextOID()
	command := &parser.AlterRoutine{
		Kind: "function", Action: "rename", Target: parser.Identifier{Text: "routine_overflow_abs"},
		Routine: parser.GrantRoutineTarget{
			Name:               parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog"}, {Text: "abs"}}},
			SignatureSpecified: true,
			Arguments:          []parser.GrantRoutineArgument{{Type: parser.TypeName{Name: "int4", System: true}}},
		},
	}
	_, err := testExecute(e, context.Background(), command, nil)
	if err == nil || err.Error() != "stuntgres: catalog definition revision exhausted" {
		t.Fatal("wrong revision overflow", err)
	}
	if e.Catalog.State != before.State() || e.Catalog.NextOID() != oid || e.Catalog.State.Get(original.Address()) != original || e.Catalog.State.HasProviderProcedureOverride("abs") || e.Catalog.State.HasProviderProcedureOverride(command.Target.Text) {
		t.Fatal("failed ALTER published descriptor/index or consumed OID")
	}
	if e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: original.Namespace(), Name: command.Target.Text, Signature: string(original.Signature)}) != (ids.ObjectAddress{}) {
		t.Fatal("failed ALTER published destination name")
	}
}

func TestRoutineAlterHardCaptureAdmission(t *testing.T) {
	e := New()
	captured := routineAlterBuiltin(t, e)
	base := e.CatalogRead()
	current := *captured
	current.ObjectName = "routine_checked_abs"
	current.ObjectRevision++
	state := catalog.CloneState(base.State())
	state.UnbindName(catalog.DescriptorNameKey(captured))
	state.SetDescriptor(current.Address(), &current)
	state.BindName(catalog.DescriptorNameKey(&current), current.Address())
	index := catalog.ProcedureIndexEdit{Index: state.ProcedureNames()}
	index.Replace(captured, &current)
	state.SetProcedureNames(index.Index)
	read := e.Catalog.Read(state)
	auth := catalogAuthorization(e, read)
	original := []catalog.ExpressionDependency{{Ref: e.Catalog.Domain.Ref(base.Epoch(), captured.Address()), Revision: captured.Revision()}}
	if err := exec.AdmitRoutineDescriptor(auth.AdmissionAccess(), original, captured, nil); err == nil {
		t.Fatal("original-source revision fence bypassed")
	}
	context := exec.Context{EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: original}}
	if err := exec.CheckRecipeRoutineDescriptor(&context, auth.AdmissionAccess(), original, captured); err != nil {
		t.Fatal("hard captured identity did not follow rename", err)
	}
	if err := exec.AdmitRoutineReference(&context, original[0].Ref, captured, "function"); err != nil {
		t.Fatal("captured aggregate/reference phase did not follow identity", err)
	}
	if err := exec.AdmitRoutineReference(&context, original[0].Ref, nil, "function"); err == nil {
		t.Fatal("ref-only phase acquired immutable declaration authority")
	}
	var hard, direct exec.RoutineProgram
	if err := hard.RecordActor(&context, original, captured, auth.State.CurrentUser, nil); err != nil {
		t.Fatal(err)
	}
	context.RoutineRecipe = false
	context.RoutineChecks = nil
	if err := exec.AdmitRoutineReference(&context, original[0].Ref, captured, "function"); err == nil {
		t.Fatal("direct reference phase lost its original-source revision fence")
	}
	if err := direct.RecordActor(&context, original, captured, auth.State.CurrentUser, nil); err != nil {
		t.Fatal(err)
	}
	if err := hard.Readmit(auth.AdmissionAccess()); err != nil {
		t.Fatal("hard policy lost in readmission", err)
	}
	if err := direct.Readmit(auth.AdmissionAccess()); err == nil {
		t.Fatal("direct policy lost in readmission")
	}
	for _, order := range [][2]*exec.RoutineProgram{{&hard, &direct}, {&direct, &hard}} {
		var union, phase exec.RoutineProgram
		union.Include(order[0])
		union.Include(order[1])
		union.Include(order[0])
		if len(union.Calls) != 2 {
			t.Fatal("union coalesced distinct source/recipe policies", len(union.Calls))
		}
		if err := union.Readmit(auth.AdmissionAccess()); err == nil {
			t.Fatal("union discarded direct-source revision fence")
		}
		if err := phase.InitializePhase(&union, auth.AdmissionAccess()); err == nil {
			t.Fatal("phase initialization discarded direct-source revision fence")
		}
	}
	context.RoutineRecipe = true
	forged := append([]catalog.ExpressionDependency(nil), original...)
	forged[0].Revision++
	context.RoutineChecks = forged
	if err := exec.CheckRecipeRoutineDescriptor(&context, auth.AdmissionAccess(), original, captured); err == nil {
		t.Fatal("forged original manifest accepted")
	}
	if err := exec.AdmitRoutineReference(&context, original[0].Ref, captured, "function"); err == nil {
		t.Fatal("captured reference phase accepted forged original manifest")
	}
	context.RoutineChecks = []catalog.ExpressionDependency{}
	if err := exec.CheckRecipeRoutineDescriptor(&context, auth.AdmissionAccess(), original, captured); err == nil {
		t.Fatal("empty stored manifest borrowed source dependencies")
	}
	if err := exec.AdmitRoutineReference(&context, original[0].Ref, captured, "function"); err == nil {
		t.Fatal("captured reference phase borrowed an empty stored manifest")
	}
	context.RoutineChecks = original
	incompatible := current
	incompatible.Result = 20
	state.SetDescriptor(incompatible.Address(), &incompatible)
	if err := exec.CheckRecipeRoutineDescriptor(&context, auth.AdmissionAccess(), original, captured); err == nil {
		t.Fatal("mutable identity policy admitted changed declaration")
	}
	if err := exec.AdmitRoutineReference(&context, original[0].Ref, captured, "function"); err == nil {
		t.Fatal("captured reference phase admitted changed immutable declaration")
	}
}
