package exec

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
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
