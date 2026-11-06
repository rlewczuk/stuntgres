package reference_test

import (
	"slices"
	"stuntgres/dual"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDualConnectorRowOrder(t *testing.T) {
	p := fixture.NewScenario(t)
	ctx := p.Ctx
	byClause := newDualPair(t, p, dual.Options{}, nil)
	never := newDualPair(t, p, dual.Options{Order: dual.OrderNever}, nil)
	always := newDualPair(t, p, dual.Options{Order: dual.OrderAlways}, nil)
	dualExec(t, ctx, byClause.DB, "CREATE TABLE dual_o (id int, k int)")
	dualExec(t, ctx, p.Native, "INSERT INTO dual_o VALUES (1, 1), (2, 2)")
	dualExec(t, ctx, byClause.Ref, "INSERT INTO dual_o VALUES (1, 2), (2, 1)")

	const (
		topLevel = "SELECT id FROM dual_o ORDER BY k"
		nested   = "SELECT id FROM (SELECT id, k FROM dual_o ORDER BY k) s"
		line     = "#0 rows: same multiset, different order (first at row 0)"
	)
	run := func(d *dualPair, query string) dualRecord {
		t.Helper()
		if got := dualStrings(t, ctx, d.DB, query); !slices.Equal(got, []string{"1", "2"}) {
			t.Fatalf("%s: caller rows %q, want [1 2]", query, got)
		}
		return lastQuery(t, d.Log.records(t), "query", query)
	}
	for _, tc := range []struct {
		name  string
		d     *dualPair
		query string
		diff  []string
	}{
		{"default top-level", byClause, topLevel, []string{line}},
		{"default nested", byClause, nested, nil},
		{"never", never, topLevel, nil},
		{"always nested", always, nested, []string{line}},
	} {
		r := run(tc.d, tc.query)
		if !r.OrderOnly {
			t.Errorf("%s: not flagged order_only", tc.name)
		}
		if !slices.Equal(r.ResponseDiff, tc.diff) {
			t.Errorf("%s: response_diff %q, want %q", tc.name, r.ResponseDiff, tc.diff)
		}
	}

	dualExec(t, ctx, byClause.Ref, "UPDATE dual_o SET id = 9 WHERE id = 2")
	r := run(byClause, topLevel)
	if r.OrderOnly {
		t.Error("value difference flagged order_only")
	}
	wantDiff(t, r, `#0 row 1 (reference 0) id: stuntgres "2", reference "9"`)
}
