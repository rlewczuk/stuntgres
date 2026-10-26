package enginetests_test

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"testing"
	"time"
)

// Cancellation at a deterministic descriptor visit, including a hidden member.
// This is a context, not a replacement catalog/provider.
type amprocVisitContext struct {
	calls    int
	cancelAt int
}

func (*amprocVisitContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*amprocVisitContext) Done() <-chan struct{}       { return nil }
func (c *amprocVisitContext) Err() error {
	c.calls++
	if c.calls >= c.cancelAt {
		return context.Canceled
	}
	return nil
}
func (*amprocVisitContext) Value(any) any { return nil }

func TestPGAmprocImplementedProjection(t *testing.T) {
	e := engine.New()
	base := e.CatalogRead()
	state := catalog.CloneState(e.Catalog.State)
	// Retain real engine-owned type descriptors and relation declaration while
	// isolating the memberships whose owner publication is under test.
	for a, d := range base.Descriptors() {
		if _, ok := d.(*catalog.OperatorFamilySupport); ok {
			copy := *d.(*catalog.OperatorFamilySupport)
			copy.CatalogVisible = false
			state.SetDescriptor(a, &copy)
		}
	}
	visible := &catalog.OperatorFamilySupport{ObjectAddress: ids.ObjectAddress{ClassID: 2603, ObjectID: 90001}, ObjectRevision: 1, Family: 1976, Left: 23, Right: 23, Number: 1, Function: 351, CatalogVisible: true}
	hidden := *visible
	hidden.ObjectAddress.ObjectID = 90002
	hidden.Number = 2
	hidden.Function = 3130
	hidden.CatalogVisible = false
	brin := *visible
	brin.ObjectAddress.ObjectID = 90003
	brin.Family = 4054
	brin.Function = 3383
	for _, d := range []*catalog.OperatorFamilySupport{visible, &hidden, &brin} {
		state.SetDescriptor(d.Address(), d)
	}
	view := e.Catalog.Read(state)
	source, _ := view.Get(ids.ObjectAddress{ClassID: 1259, ObjectID: 2603}).(*catalog.Relation)
	if source == nil {
		t.Fatal("missing pg_amproc")
	}
	in := pgcatalog.Input{Context: t.Context(), Catalog: view, Source: source}
	produce, _ := pgcatalog.ProducerFor(2603)
	rows, err := produce(in)
	if err != nil || len(rows) != 2 {
		t.Fatal("owner projection", rows, err)
	}
	for i, row := range rows {
		if len(row) != 6 {
			t.Fatal("width", row)
		}
		for j, v := range row {
			if v.Type() != source.Columns[j].Usage.Type {
				t.Fatal("owned type", i, j)
			}
		}
	}
	if rows[0][0].Type().ID() != 26 || rows[0][4].Type().ID() != 21 || rows[0][5].Type().ID() != 24 {
		t.Fatal("typed shape", rows)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*pgcatalog.Input)
	}{
		{"shape", func(in *pgcatalog.Input) { s := *source; s.Columns = s.Columns[:5]; in.Source = &s }},
		{"owned type", func(in *pgcatalog.Input) {
			s := *source
			s.Columns = append([]catalog.Column(nil), source.Columns...)
			s.Columns[0].Usage.Type, _ = engine.New().CatalogRead().LookupType(26)
			in.Source = &s
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := in
			tc.mutate(&bad)
			ctx := &amprocVisitContext{cancelAt: 2}
			bad.Context = ctx
			if got, err := produce(bad); got != nil || err == nil || !strings.Contains(err.Error(), "operator family source") || ctx.calls != 1 {
				t.Fatal("validation before enumeration", got, err, ctx.calls)
			}
		})
	}
	// With every descriptor denied, cancellation must still be checked during
	// enumeration, rather than only during admitted-row cell construction.
	for a, d := range view.Descriptors() {
		if s, ok := d.(*catalog.OperatorFamilySupport); ok {
			copy := *s
			copy.CatalogVisible = false
			state.SetDescriptor(a, &copy)
		}
	}
	in.Catalog = e.Catalog.Read(state)
	if got, err := produce(in); err != nil || got == nil || len(got) != 0 {
		t.Fatal("default-denied projection", got, err)
	}
	bad := in
	malformed := *source
	malformed.Columns = malformed.Columns[:5]
	bad.Source = &malformed
	if got, err := produce(bad); got != nil || err == nil {
		t.Fatal("empty projection bypassed shape validation", got, err)
	}
	in.Context = &amprocVisitContext{cancelAt: 3}
	if got, err := produce(in); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("hidden enumeration cancellation", got, err)
	}
	if !visible.CatalogVisible || hidden.CatalogVisible || !brin.CatalogVisible {
		t.Fatal("mutated owner descriptors")
	}
}
