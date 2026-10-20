package engine

import (
	"context"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestBetweenMixedTemporalCaptureIdentity(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE temporal_bounds(d DATE,ts TIMESTAMP)")
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	first := sqltype.InputEnvironment{TransactionStart: time.Date(2003, 7, 7, 4, 5, 6, 0, time.UTC)}
	later := sqltype.InputEnvironment{TransactionStart: time.Date(2004, 8, 8, 12, 13, 14, 0, time.UTC)}
	for _, form := range []string{"BETWEEN", "NOT BETWEEN", "BETWEEN SYMMETRIC", "NOT BETWEEN SYMMETRIC"} {
		e.mu.Lock()
		auth, err := e.authorizationLocked(op)
		if err != nil {
			e.mu.Unlock()
			t.Fatal(err)
		}
		ast := statement(t, "SELECT '2000-01-02' "+form+" d AND ts FROM temporal_bounds")
		ref, err := e.resolveReference(auth, ast.(*parser.Select).From.(*parser.TableReference).Name)
		if err != nil {
			e.mu.Unlock()
			t.Fatal(err)
		}
		bound, err := describe(op.Ctx, e.analysisHost(op), ast, auth, ref, first, nil, nil)
		if err != nil {
			e.mu.Unlock()
			t.Fatal(err)
		}
		if len(bound.QuerySources) != 1 || bound.QuerySources[0].Ref != ref || !reflect.DeepEqual(bound.QuerySources[0].Uses, []int{0, 1}) || len(bound.Parameters.Types) != 0 {
			e.mu.Unlock()
			t.Fatal("mixed temporal dependencies changed", bound.QuerySources, bound.Parameters)
		}
		desc := StatementDescription{temporals: bound.Prepared.Temporals}
		before := append([]analyze.TemporalConstant(nil), desc.temporals...)
		counts := map[sqltype.ID]int{}
		for _, capture := range before {
			if capture.Offset != 7 || capture.Typmod != -1 {
				e.mu.Unlock()
				t.Fatal(capture)
			}
			counts[capture.TypeID]++
			wantScalar := map[sqltype.ID]int64{sqltype.Date: 1, sqltype.Timestamp: 86400000000}[capture.TypeID]
			if capture.Scalar != wantScalar {
				e.mu.Unlock()
				t.Fatal("incorrect frozen scalar", capture, wantScalar)
			}
		}
		wantEach := 1
		if form == "BETWEEN SYMMETRIC" || form == "NOT BETWEEN SYMMETRIC" {
			wantEach = 2
		}
		if len(counts) != 2 || counts[sqltype.Date] != wantEach || counts[sqltype.Timestamp] != wantEach {
			e.mu.Unlock()
			t.Fatal("independent temporal contexts lost", before)
		}
		for range 3 {
			rebound, err := describe(op.Ctx, e.analysisHost(op), ast, auth, ref, later, &desc, nil)
			if err != nil {
				e.mu.Unlock()
				t.Fatal(form, err)
			}
			if len(rebound.QuerySources) != 1 || rebound.QuerySources[0].Ref != bound.QuerySources[0].Ref || !reflect.DeepEqual(rebound.QuerySources[0].Uses, bound.QuerySources[0].Uses) || !reflect.DeepEqual(rebound.Parameters, bound.Parameters) {
				e.mu.Unlock()
				t.Fatal("rebind dependencies changed")
			}
			if !reflect.DeepEqual(desc.temporals, before) || len(rebound.Prepared.Temporals) != 0 {
				e.mu.Unlock()
				t.Fatal("frozen captures replaced", desc.temporals, rebound.Prepared.Temporals)
			}
		}
		e.mu.Unlock()
	}
}

func TestBetweenTemporalCaptureContextKey(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	captures := []analyze.TemporalConstant{
		{Offset: 7, TypeID: sqltype.Date, Typmod: -1, Scalar: 1},
		{Offset: 7, TypeID: sqltype.Timestamp, Typmod: -1, Scalar: 86400000000},
		{Offset: 7, TypeID: sqltype.Timestamp, Typmod: 3, Scalar: 86400123000},
		{Offset: 7, TypeID: sqltype.Timestamp, Typmod: 6, Scalar: 86400123456},
		{Offset: 7, TypeID: sqltype.Timestamp, Typmod: 3, Scalar: 86400123000},
	}
	desc := StatementDescription{temporals: captures}
	b.Expected = expectedOf(&desc)
	before := append([]analyze.TemporalConstant(nil), captures...)
	for _, expected := range captures {
		usage := b.Type(expected.TypeID).Usage()
		usage.Typmod = expected.Typmod
		for range 2 {
			got, found, err := b.Captured(7, usage)
			if err != nil || !found || got != expected {
				t.Fatal(got, found, err, expected)
			}
			value, err := b.InputConstant("invalid input must not be reparsed", usage, parser.Position{Offset: 7})
			if err != nil {
				t.Fatal(err)
			}
			var scalar int64
			if expected.TypeID == sqltype.Date {
				scalar, err = value.Date()
			} else {
				scalar, err = value.Timestamp()
			}
			if err != nil || scalar != expected.Scalar {
				t.Fatal(scalar, err, expected)
			}
		}
	}
	missing := b.Type(sqltype.Timestamp).Usage()
	missing.Typmod = 2
	if _, _, err := b.Captured(7, missing); err == nil {
		t.Fatal("missing contextual key accepted")
	}
	if _, err := b.InputConstant("2000-01-02", missing, parser.Position{Offset: 7}); err == nil {
		t.Fatal("missing key silently reparsed")
	}
	if _, _, err := b.CaptureAt(7); err == nil {
		t.Fatal("ambiguous typed-literal metadata accepted")
	}
	if _, found, err := b.Captured(8, missing); err != nil || found {
		t.Fatal(found, err)
	}
	if !reflect.DeepEqual(before, desc.temporals) || len(b.Prepared.Temporals) != 0 {
		t.Fatal("capture contract mutated", desc.temporals, b.Prepared.Temporals)
	}
	b.Expected = expectedOf(&StatementDescription{temporals: []analyze.TemporalConstant{captures[2], captures[4]}})
	if got, found, err := b.CaptureAt(7); err != nil || !found || got != captures[2] {
		t.Fatal("uniform frozen metadata rejected", got, found, err)
	}
}
