package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestBackendCurrentBindingAndRecordIdentity(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for _, tc := range []struct {
		name        string
		oid         uint32
		fields      int
		set, strict bool
		rows        float32
	}{
		{"pg_stat_get_activity", 2022, 31, true, false, 100},
		{"pg_stat_get_backend_io", 6386, 20, true, true, 5},
		{"pg_stat_get_backend_wal", 6313, 5, false, true, 0},
	} {
		node, _, evaluation := strictNullUnitBind(t, e, op, scope, tc.name+"(NULL::integer)")
		n, ok := node.(functions.BackendCurrent)
		if !ok {
			t.Fatalf("immutable typed recipe %T", node)
		}
		d, err := exec.ExpressionRoutineDescriptor(n)
		if err != nil || d == nil || uint32(d.Address().ObjectID) != tc.oid || d.Strict != tc.strict || d.ReturnsSet() != tc.set || d.Result != 2249 || d.Signature != "23" || d.Builtin.Parallel != 'r' || d.RowEstimate() != tc.rows || d.CostEstimate() != 1 {
			t.Fatal("released declaration", d, err)
		}
		out := d.OutputDeclaration()
		if out == nil || out.Inputs() != 1 || out.Len() != tc.fields || n.Usage.Type.CompositeLen() != tc.fields || !exec.SourceAdmitted(n) || exec.SourceReturnsSet(n) != tc.set {
			t.Fatal("fixed source identity", n, out)
		}
		for i := range tc.fields {
			m := n.Usage.Type.CompositeMember(i)
			if m.Name != out.Name(i+1) || uint32(m.Usage.Type.ID()) != uint32(out.Type(i)) || m.Attribute != int32(i+1) || m.Usage.Typmod != -1 {
				t.Fatal("exact output declaration", i, m)
			}
		}
		if n.Validate() != nil || n.ValidateStoredProvider() != nil || n.PlanningFold() != expr.FoldNever {
			t.Fatal("invalid typed recipe")
		}
		before := n
		if tc.name == "pg_stat_get_backend_wal" {
			v, err := n.Eval(&evaluation)
			if err != nil || !v.IsNull() || v.Type() != n.Usage.Type {
				t.Fatal("strict whole RECORD NULL", v, err)
			}
			unknown, _, ctx := strictNullUnitBind(t, e, op, scope, tc.name+"(0)")
			v, err = unknown.Eval(&ctx)
			if err != nil || !v.IsNull() || v.CompositeLen() != 0 {
				t.Fatal("unknown must be whole NULL not all-NULL composite", v, err)
			}
		} else if tc.name == "pg_stat_get_backend_io" {
			cursor, err := n.OpenSource(&evaluation)
			if err != nil || cursor.Type != n.Usage.Type || len(cursor.Values) != 0 {
				t.Fatal("typed empty SRF", cursor, err)
			}
			folded, _, err := exec.PreflightCoercions(&evaluation, n)
			if err != nil || !exec.SourceReturnsSet(folded) {
				t.Fatal("strict IO became scalar NULL", folded, err)
			}
		}
		rebuilt, err := n.Rebuild(n.Info, n.Children())
		if err != nil || !reflect.DeepEqual(rebuilt, n) {
			t.Fatal("rebuild", err)
		}
		if _, err := n.Render(); err != nil {
			t.Fatal(err)
		}
		capture, ok, err := n.Capture(expr.CaptureFunction)
		if err != nil || !ok || capture.Count != 1 {
			t.Fatal("capture", capture, ok, err)
		}
		if child, err := n.CaptureOperand(0); err != nil || child != n.Argument {
			t.Fatal("typed child provenance", child, err)
		}
		if _, changed, err := stored.RetagTypes(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }); err != nil || changed {
			t.Fatal("stored remap", changed, err)
		}
		if !reflect.DeepEqual(before, n) {
			t.Fatal("protocol mutated retained recipe")
		}
	}
	node, _, evaluation := strictNullUnitBind(t, e, op, scope, "pg_stat_get_backend_io(pg_backend_pid())")
	n := node.(functions.BackendCurrent)
	cursor, err := n.OpenSource(&evaluation)
	if err != nil || len(cursor.Values) != 8 {
		t.Fatal("actual live source cursor", cursor, err)
	}
	first, present, err := cursor.Next(context.Background())
	if err != nil || !present || first.Type() != n.Usage.Type {
		t.Fatal(first, present, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if value, present, err := cursor.Next(ctx); !errors.Is(err, context.Canceled) || present || value.Type() != nil || cursor.Values != nil || !cursor.Exhausted {
		t.Fatal("canceled cursor retained pending results", cursor, err)
	}
	if first.IsNull() || first.CompositeLen() != 20 {
		t.Fatal("cancel mutated detached returned value")
	}
	cursor, err = n.OpenSource(&evaluation)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for {
		_, present, err := cursor.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			break
		}
		count++
	}
	if count != 8 || cursor.Values != nil || !cursor.Exhausted {
		t.Fatal("EOF did not release owned rows", count, cursor)
	}
}
