package exec

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

// Without the engine's install, every preparation route panics instead of
// silently skipping preflight, domain checks or provider validation.
func TestMissingPreparationFailsLoudly(t *testing.T) {
	var c Context
	routes := map[string]func(){
		"CheckDomain":        func() { _ = c.CheckDomain(context.Background(), nil, sqltype.Value{}) },
		"DomainCatalog":      func() { _ = preparation.DomainCatalog(&c) },
		"ValidInstalledText": func() { _ = preparation.ValidInstalledText(plan.InstalledTextExpr{}) },
		"AdmitExpression":    func() { _ = AdmitExpression(&c, nil, catalog.Read{}) },
		"PreflightQuery":     func() { _, _ = preflightQuery(&c, nil) },
		"PrepareDomainConversion": func() {
			_ = prepareDomainConversion(&c, nil, nil)
		},
		"SpecializeRuleParameters": func() { _, _, _ = specializeRuleParameters(&c, nil) },
		"ReturnsSet":               func() { _ = preparation.ReturnsSet(nil) },
		"StoredReferenceValid": func() {
			_ = preparation.StoredReferenceValid(catalog.Read{}, nil, ids.ObjectRef{})
		},
		"AcceptedDefaultSource":      func() { _ = preparation.AcceptedDefaultSource(catalog.Read{}, nil) },
		"AcceptedDefaultRoutine":     func() { _, _ = preparation.AcceptedDefaultRoutine(catalog.Read{}, nil, AcceptedDefaultCall{}) },
		"MapAcceptedDefaultChildren": func() { _, _, _ = preparation.MapAcceptedDefaultChildren(catalog.Read{}, nil, nil) },
	}
	for name, route := range routes {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != "exec: preparation not installed" {
					t.Fatalf("recovered %v", r)
				}
			}()
			route()
			t.Fatal("route returned without an installed preparation")
		})
	}
}

type recordingPreparation struct{ calls int }

func (p *recordingPreparation) CheckDomain(context.Context, *Context, *sqltype.Descriptor, sqltype.Value) error {
	p.calls++
	return nil
}

func (p *recordingPreparation) AdmitExpression(*Context, expr.Expr, catalog.Read) error {
	p.calls++
	return nil
}

func (p *recordingPreparation) PreflightQuery(_ *Context, n plan.Node) (plan.Node, error) {
	p.calls++
	return n, nil
}

func (p *recordingPreparation) PrepareDomainConversion(*Context, *sqltype.Descriptor, *sqltype.Descriptor) error {
	p.calls++
	return nil
}

func (p *recordingPreparation) SpecializeRuleParameters(_ *Context, e expr.Expr) (expr.Expr, bool, error) {
	p.calls++
	return e, true, nil
}

func (p *recordingPreparation) DomainCatalog(*Context) catalog.Read { return catalog.Read{} }

func (p *recordingPreparation) ReturnsSet(*catalog.Procedure) bool { return false }

func (p *recordingPreparation) StoredReferenceValid(catalog.Read, *catalog.Relation, ids.ObjectRef) bool {
	return false
}

func (p *recordingPreparation) ValidInstalledText(plan.InstalledTextExpr) bool { return true }

func (*recordingPreparation) AcceptedDefaultSource(catalog.Read, *AcceptedDefault) error {
	return ids.ErrCatalogReplaced
}
func (*recordingPreparation) AcceptedDefaultRoutine(catalog.Read, *AcceptedDefault, AcceptedDefaultCall) (*catalog.Procedure, error) {
	return nil, ids.ErrCatalogReplaced
}
func (*recordingPreparation) MapAcceptedDefaultChildren(catalog.Read, expr.Expr, func(expr.Expr) (expr.Expr, bool, error)) (expr.Expr, bool, error) {
	return nil, false, ids.ErrCatalogReplaced
}

// The installed value receives the runtime's calls; a cancelled context
// stops a domain check before preparation runs.
func TestInstallPreparation(t *testing.T) {
	previous := preparation
	defer func() { preparation = previous }()
	recorder := &recordingPreparation{}
	InstallPreparation(recorder)
	var c Context
	if err := c.CheckDomain(context.Background(), nil, sqltype.Value{}); err != nil || recorder.calls != 1 {
		t.Fatal(err, recorder.calls)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.CheckDomain(cancelled, nil, sqltype.Value{}); err != context.Canceled || recorder.calls != 1 {
		t.Fatal(err, recorder.calls)
	}
	if err := AdmitExpression(&c, nil, catalog.Read{}); err != nil || recorder.calls != 2 {
		t.Fatal(err, recorder.calls)
	}
	// The forwarders and the preflight hooks reach the installed value and return its results.
	if node, err := preflightQuery(&c, nil); err != nil || node != nil || recorder.calls != 3 {
		t.Fatal(err, node, recorder.calls)
	}
	if err := prepareDomainConversion(&c, nil, nil); err != nil || recorder.calls != 4 {
		t.Fatal(err, recorder.calls)
	}
	if _, changed, err := specializeRuleParameters(&c, nil); err != nil || !changed || recorder.calls != 5 {
		t.Fatal(err, changed, recorder.calls)
	}
}

// Private invalid values exercise the real token/wrapper/invocation rejection
// boundary before authority lookup. Genuine mint/provider proof is covered by
// the composed source-bound tests; this fixture grants no provider authority.
func TestAcceptedDefaultInvalidModes(t *testing.T) {
	for _, mode := range []defaultInvocationMode{0, 255, defaultPredecessor} {
		definition := &proc.Definition{Kind: proc.PLpgSQLBody}
		procedure := &catalog.Procedure{RoutineKind: catalog.FunctionRoutine, SQL: definition}
		source := plan.SQLRoutineExpr{Procedure: procedure}
		attempt := &defaultPreparation{active: true}
		attempt.self = attempt
		token := &defaultPreparationCall{attempt: attempt, source: source, prepared: source, mode: mode, definition: definition}
		token.self = token
		if token.valid() {
			t.Fatal("invalid or recombined source mode validated", mode)
		}
		if _, _, err := token.preflight(nil, true, false); err == nil {
			t.Fatal("invalid mode entered preparation", mode)
		}
		owner := NewAcceptedDefaultSet()
		if err := owner.Seal(1); err != nil {
			t.Fatal(err)
		}
		artifact := &AcceptedDefault{owner: owner}
		artifact.self = artifact
		root := &acceptedDefaultRoot{artifact: artifact}
		root.self, artifact.root = root, root
		call := &acceptedDefaultCall{artifact: artifact, source: source, original: source, mode: mode, definition: definition}
		call.self = call
		artifact.calls = []*acceptedDefaultCall{call}
		occurrence := AcceptedDefaultCall{node: call}
		if _, _, ok := occurrence.Original(); ok {
			t.Fatal("invalid mode acquired source-path evidence", mode)
		}
		if _, ok := occurrence.prepared(); ok {
			t.Fatal("invalid mode acquired prepared invocation evidence", mode)
		}
		activation := &defaultActivation{artifact: artifact, context: context.Background(), active: true}
		invocation := &AcceptedDefaultInvocation{activation: activation, occurrence: occurrence}
		invocation.self = invocation
		if environment, executable, err := invocation.Validate(RoutineAccess{}, nil, procedure); err == nil || environment != nil || executable != nil {
			t.Fatal("invalid mode chose an executable or environment", mode, err)
		}
	}
	// An otherwise correctly typed mode cannot be recombined with another
	// source definition, a SQL source, or a separately prepared definition.
	pl := &proc.Definition{Kind: proc.PLpgSQLBody}
	sql := &proc.Definition{Kind: proc.SQLBody}
	original := plan.SQLRoutineExpr{Procedure: &catalog.Procedure{RoutineKind: catalog.FunctionRoutine, SQL: pl}}
	other := plan.SQLRoutineExpr{Procedure: &catalog.Procedure{RoutineKind: catalog.FunctionRoutine, SQL: sql}}
	if defaultCurrentPL.valid(pl, original, other) || defaultCurrentPL.valid(sql, other, other) || defaultCurrentPL.valid(&proc.Definition{Kind: proc.PLpgSQLBody}, original, original) {
		t.Fatal("recombined definition acquired current-provider mode")
	}
}
