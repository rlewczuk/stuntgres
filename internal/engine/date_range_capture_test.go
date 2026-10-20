package engine

import (
	"context"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestDateRangeTemporalCapture(t *testing.T) {
	ctx := context.Background()
	e := New()
	d, ok := e.CatalogRead().LookupType(sqltype.DateRange)
	if !ok || !d.Concrete() { t.Fatal("missing daterange") }
	first := sqltype.InputEnvironment{TransactionStart: time.Date(2000, 2, 29, 12, 0, 0, 0, time.UTC)}
	later := sqltype.InputEnvironment{TransactionStart: time.Date(2001, 1, 1, 12, 0, 0, 0, time.UTC)}
	position := parser.Position{Offset: 7, Character: 8}
	b := newTestExpressionBinder(t, ctx, e, first, nil)
	v, err := b.InputConstant("[today,tomorrow)", d.Usage(), position)
	if err != nil { t.Fatal(err) }
	out, err := d.Output(ctx, v)
	if err != nil || out != "[2000-02-29,2000-03-01)" || len(b.Prepared.Temporals) != 1 || !sqltype.SameDatum(v, b.Prepared.Temporals[0].Value) { t.Fatal("initial compound capture", out, b.Prepared.Temporals, err) }
	description := &StatementDescription{temporals: append([]analyze.TemporalConstant(nil), b.Prepared.Temporals...)}
	retained := newTestExpressionBinder(t, ctx, e, later, description)
	frozen, err := retained.InputConstant("invalid input must not be reparsed", d.Usage(), position)
	if err != nil || !sqltype.SameDatum(v, frozen) { t.Fatal("literal capture moved", err) }
	fresh, err := d.InputWithEnvironment(ctx, "[today,tomorrow)", -1, later)
	if err != nil { t.Fatal(err) }
	out, err = d.Output(ctx, fresh)
	if err != nil || out != "[2001-01-01,2001-01-02)" || sqltype.SameDatum(v, fresh) { t.Fatal("fresh bound environment", out, err) }
	if !analyze.RefreshableTemporal(b.Prepared.Temporals[0]) { t.Fatal("builtin daterange not refreshable") }
	description.refreshTemporals = true
	refreshed := newTestExpressionBinder(t, ctx, e, later, description)
	candidate, err := refreshed.InputConstant("[today,tomorrow)", d.Usage(), position)
	if err != nil || !sqltype.SameDatum(fresh, candidate) || len(refreshed.Prepared.Temporals) != 1 { t.Fatal("valid invalidation did not refresh", err) }
	if !sqltype.SameDatum(description.temporals[0].Value, v) { t.Fatal("candidate mutated retained capture") }
	domain, err := sqltype.Domain(20000, "span", "span", d.Usage())
	if err != nil { t.Fatal(err) }
	logical, err := domain.Wrap(v)
	if err != nil { t.Fatal(err) }
	if analyze.RefreshableTemporal(analyze.TemporalConstant{TypeID: domain.ID(), Typmod: -1, Value: logical}) { t.Fatal("logical domain capture lost ownership fence") }
}
