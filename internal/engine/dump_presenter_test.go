package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/control"
	"testing"
)

func TestDumpPresenterDomainExpressions(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.posint AS integer DEFAULT 7 CHECK (VALUE > 0)"))
	view := e.CatalogRead()
	d := domainUnitType(t, view, "posint")
	if !d.DomainDefaultOwn || d.DomainDefault == nil || len(d.DomainChecks) != 1 {
		t.Fatalf("fixture domain: default=%v checks=%d", d.DomainDefault, len(d.DomainChecks))
	}
	var p control.Presenter
	ctx := context.Background()
	if text, err := p.DomainDefault(ctx, view, d.DomainDefault.Expression); err != nil || text != "7" {
		t.Fatalf("DomainDefault = %q, %v", text, err)
	}
	if text, err := p.DomainCheck(ctx, view, d.SQLType.DomainBase(), d.DomainChecks[0].Expression); err != nil || text != "(VALUE > 0)" {
		t.Fatalf("DomainCheck = %q, %v", text, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := p.DomainDefault(cancelled, view, d.DomainDefault.Expression); !errors.Is(err, context.Canceled) {
		t.Fatalf("DomainDefault after cancel: %v", err)
	}
	if _, err := p.DomainCheck(cancelled, view, d.SQLType.DomainBase(), d.DomainChecks[0].Expression); !errors.Is(err, context.Canceled) {
		t.Fatalf("DomainCheck after cancel: %v", err)
	}
}
