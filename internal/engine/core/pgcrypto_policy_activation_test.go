package core_test

import (
	"encoding/hex"
	"errors"
	"testing"

	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPgcryptoPolicyActivation(t *testing.T) {
	e := rootengine.New()
	installer := enginetest.StartSession(t, e, nil)
	enginetest.PreparedDescription(t, e, installer, "CREATE EXTENSION pgcrypto")
	if installer.Settings.PgcryptoInitialized() { t.Fatal("Describe initialized a physical module") }
	enginetest.SessionSQL(t, installer, "CREATE EXTENSION pgcrypto")
	if !installer.Settings.PgcryptoInitialized() { t.Fatal("actual C-member validation did not initialize installer") }
	peer := enginetest.StartSession(t, e, nil)
	if peer.Settings.PgcryptoInitialized() { t.Fatal("extension catalog presence initialized physical peer") }
	op, err := peer.Acquire(t.Context())
	if err != nil { t.Fatal(err) }
	defer op.Done()
	query := enginetest.BindSelect(t, e, op, "SELECT digest('abc','sha256')")
	projection, ok := query.Root.(*plan.ProjectNode)
	if !ok || len(projection.Expressions) != 1 { t.Fatal("authentic installed graph", query.Root) }
	call, ok := projection.Expressions[0].(plan.InstalledCryptoExpr)
	if !ok || call.Procedure == nil || call.Ref.Address != call.Procedure.Address() { t.Fatal("installed carrier lost procedure/ref", projection.Expressions[0]) }
	if peer.Settings.PgcryptoInitialized() { t.Fatal("analysis-only binding initialized module") }
	scope, err := e.StartStatement(op)
	if err != nil { t.Fatal(err) }
	runtime, ok := core.HostOf(scope).(interface{ InitializePgcrypto(*catalog.Procedure, ids.ObjectRef) error })
	if !ok { t.Fatal("missing real statement initializer") }
	foreign := *call.Procedure
	foreign.Implementation = nil
	if err := runtime.InitializePgcrypto(&foreign, call.Ref); err == nil || peer.Settings.PgcryptoInitialized() { t.Fatal("untrusted provider initialized module", err) }
	wrongRef := call.Ref
	wrongRef.Address.ObjectID++
	if err := runtime.InitializePgcrypto(call.Procedure, wrongRef); err == nil || peer.Settings.PgcryptoInitialized() { t.Fatal("foreign procedure reference initialized module", err) }
	enginetest.SessionSQL(t, installer, "CREATE ROLE pgcrypto_activation_owner")
	enginetest.SessionSQL(t, installer, "ALTER FUNCTION digest(text,text) OWNER TO pgcrypto_activation_owner")
	capturedOwner := call.Procedure.Owner
	before := e.CatalogRead().Get(call.Procedure.Address())
	if before == call.Procedure || !exec.SameRoutineDeclaration(before.(*catalog.Procedure), call.Procedure) { t.Fatal("missing genuine compatible COW declaration") }
	if err := runtime.InitializePgcrypto(call.Procedure, call.Ref); err != nil { t.Fatal(err) }
	if !peer.Settings.PgcryptoInitialized() || e.CatalogRead().Get(call.Procedure.Address()) != before || call.Procedure.Owner != capturedOwner { t.Fatal("initialization changed installed identity/catalog or retained declaration") }
	if err := scope.Finish(nil); err != nil { t.Fatal(err) }
}

func TestPgcryptoPolicyDormantPlanning(t *testing.T) {
	for _, tc := range []struct{ name, query, text, binary string; pruned, null bool }{
		{name: "Direct", query: "SELECT crypt('foox','NB')"},
		{name: "Composite", query: "SELECT 'prefix:' || crypt('foox','NB')"},
		{name: "Coercion", query: "SELECT crypt('foox','NB')::varchar"},
		{name: "Array", query: "SELECT ARRAY[crypt('foox','NB')]"},
		{name: "VolatileSalt", query: "SELECT gen_salt('des')"},
		{name: "UnrelatedDigest", query: "SELECT digest('abc','sha256')", binary: "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{name: "StrictNull", query: "SELECT crypt(NULL::text,'malformed')", pruned: true, null: true},
		{name: "DeadCase", query: "SELECT CASE WHEN false THEN crypt('foox','NB') ELSE 'safe' END", pruned: true, text: "safe"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := rootengine.New()
			installer := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, installer, "CREATE EXTENSION pgcrypto")
			peer := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, peer, "SET pgcrypto.builtin_crypto_enabled=off")
			op, err := peer.Acquire(t.Context())
			if err != nil { t.Fatal(err) }
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil { t.Fatal(err) }
			defer scope.Finish(errors.New("unfinished dormant planning witness"))
			raw := enginetest.Statement(t, tc.query).(*parser.Select)
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			if err != nil { e.Mu.Unlock(); t.Fatal(err) }
			binder := core.NewExpressionBinder(op.Context(), e, scope.Input, op, nil)
			binder.Query.Auth = &auth
			query, err := binder.BindSelect(raw, false)
			e.Mu.Unlock()
			if err != nil { t.Fatal(err) }
			projection, ok := query.Root.(*plan.ProjectNode)
			if !ok || len(projection.Expressions) != 1 { t.Fatal("missing original installed graph", query.Root) }
			original := projection.Expressions[0]
			dormant := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Catalog: auth.Catalog, Input: binder.Input}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: binder.Dependencies}}
			preflight, _, err := exec.PreflightCoercionsWithPolicy(&dormant, original, true)
			if err != nil { t.Fatal("dormant coercion capture evaluated crypto", err) }
			cooked, _, err := exec.SimplifyConditional(&dormant, preflight, true)
			if err != nil { t.Fatal("dormant conditional capture evaluated crypto", err) }
			calls := 0
			plan.WalkExpression(cooked, func(node expr.Expr) {
				if call, ok := node.(plan.InstalledCryptoExpr); ok {
					calls++
					if call.Procedure == nil || call.Ref != auth.Ref(call.Procedure.Address()) { t.Fatal("dormant capture lost installed authority") }
				}
			})
			wantCalls := 1
			if tc.pruned { wantCalls = 0 }
			if calls != wantCalls || peer.Settings.PgcryptoInitialized() { t.Fatal("dormant capture initialized/evaluated provider or lost pruning", calls) }
			if value, _ := peer.Setting("pgcrypto.builtin_crypto_enabled"); value != "off" { t.Fatal("dormant capture changed raw policy", value) }
			if tc.name == "Direct" {
				if _, err := exec.FoldPlanningExpression(&dormant, original); err == nil || peer.Settings.PgcryptoInitialized() { t.Fatal("direct computation acquired missing live authority", err) }
			}
			live := dormant
			live.Host, live.Context = core.HostOf(scope), scope.ActivateRuntime(op.Context())
			prepared, _, err := exec.PreflightCoercionsWithPolicy(&live, cooked, true)
			var value sqltype.Value
			if err == nil {
				plan.WalkExpression(prepared, func(node expr.Expr) {
					if err == nil { err = exec.AdmitRoutineCall(&live, node, false, false) }
				})
			}
			if err == nil { value, err = prepared.Eval(&live) }
			if !tc.pruned && tc.binary == "" {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "XX000" || diagnostic.Message != "use of built-in crypto functions is disabled" { t.Fatal("live call bypassed current policy", err) }
			} else {
				if err != nil || value.IsNull() != tc.null { t.Fatal("live pruned/unrelated result", value, err) }
				if tc.text != "" { if text, err := value.Text(); err != nil || text != tc.text { t.Fatal(text, err) } }
				if tc.binary != "" { raw, err := value.Binary(); if err != nil || hex.EncodeToString([]byte(raw)) != tc.binary { t.Fatal(raw, err) } }
			}
			if peer.Settings.PgcryptoInitialized() == tc.pruned { t.Fatal("live initialization ignored surviving-call demand") }
			if err := scope.Finish(nil); err != nil { t.Fatal(err) }
		})
	}
}

func TestPgcryptoPolicyClosedEvaluation(t *testing.T) {
	e := rootengine.New()
	installer := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, installer, "CREATE EXTENSION pgcrypto")
	peer := enginetest.StartSession(t, e, nil)
	op, err := peer.Acquire(t.Context())
	if err != nil { t.Fatal(err) }
	defer op.Done()
	query := enginetest.BindSelect(t, e, op, "SELECT crypt('foox','NB')")
	projection := query.Root.(*plan.ProjectNode)
	call := projection.Expressions[0].(plan.InstalledCryptoExpr)
	if exec.ParallelQueryExpression(call, projection.Frame, 0) { t.Fatal("installed crypto entered the closed-worker allowlist") }
	scope, err := e.StartStatement(op)
	if err != nil { t.Fatal(err) }
	defer scope.Finish(errors.New("unfinished closed evaluation witness"))
	live := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.ActivateRuntime(op.Context()), Host: core.HostOf(scope)}}
	closed := live // The incidental lexical host must not be a fallback.
	closed.ClosedEvaluation = &exec.EvaluationCapture{}
	requireClosed := func() {
		t.Helper()
		if err := closed.InitializePgcrypto(call.Procedure, call.Ref); err == nil || err.Error() != "stuntgres: pgcrypto initialization is unavailable in closed evaluation" { t.Fatal("closed initializer borrowed lexical authority", err) }
		if err := closed.CheckPgcryptoBuiltinCrypto(); err == nil || err.Error() != "stuntgres: pgcrypto policy is unavailable in closed evaluation" { t.Fatal("closed policy borrowed lexical authority", err) }
	}
	requireClosed()
	if peer.Settings.PgcryptoInitialized() { t.Fatal("closed evaluation initialized physical module") }
	if err := live.InitializePgcrypto(call.Procedure, call.Ref); err != nil { t.Fatal(err) }
	if err := live.CheckPgcryptoBuiltinCrypto(); err != nil { t.Fatal("normal live on policy", err) }
	if !peer.Settings.PgcryptoInitialized() { t.Fatal("live initializer did not activate module") }
	requireClosed() // Even an already-loaded/on host cannot bypass closed input.
	if value, _ := peer.Setting("pgcrypto.builtin_crypto_enabled"); value != "on" { t.Fatal("closed capability changed effective policy", value) }
	if err := scope.Finish(nil); err != nil { t.Fatal(err) }
}
