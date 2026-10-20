package exec

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Without the engine's install, every preparation route panics instead of
// silently skipping preflight, domain checks or provider validation.
func TestMissingPreparationFailsLoudly(t *testing.T) {
	var c Context
	routes := map[string]func(){
		"CheckDomain":        func() { _ = c.CheckDomain(context.Background(), nil, sqltype.Value{}) },
		"PreflightCoercions": func() { _, _, _ = preparation.PreflightCoercions(&c, nil) },
		"DomainCatalog":      func() { _ = preparation.DomainCatalog(&c) },
		"ValidInstalledText": func() { _ = preparation.ValidInstalledText(plan.InstalledTextExpr{}) },
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

func (p *recordingPreparation) PreflightCoercions(*Context, expr.Expr) (expr.Expr, bool, error) {
	return nil, false, nil
}

func (p *recordingPreparation) CheckDomain(context.Context, *Context, *sqltype.Descriptor, sqltype.Value) error {
	p.calls++
	return nil
}

func (p *recordingPreparation) DomainCatalog(*Context) catalog.Read { return catalog.Read{} }

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
}
