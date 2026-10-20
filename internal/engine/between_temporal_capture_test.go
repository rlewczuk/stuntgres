package engine

import (
	"context"
	"reflect"
	"testing"
	"time"

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
		bound, err := e.describe(op.ctx, ast, auth, ref, first, op, nil, nil)
		if err != nil {
			e.mu.Unlock()
			t.Fatal(err)
		}
		if len(bound.querySources) != 1 || bound.querySources[0].ref != ref || !reflect.DeepEqual(bound.querySources[0].uses, []int{0, 1}) || len(bound.parameters.Types) != 0 {
			e.mu.Unlock()
			t.Fatal("mixed temporal dependencies changed", bound.querySources, bound.parameters)
		}
		desc := StatementDescription{temporals: bound.prepared.temporals}
		before := append([]temporalConstant(nil), desc.temporals...)
		counts := map[sqltype.ID]int{}
		for _, capture := range before {
			if capture.offset != 7 || capture.typmod != -1 {
				e.mu.Unlock()
				t.Fatal(capture)
			}
			counts[capture.typeID]++
			wantScalar := map[sqltype.ID]int64{sqltype.Date: 1, sqltype.Timestamp: 86400000000}[capture.typeID]
			if capture.scalar != wantScalar {
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
			rebound, err := e.describe(op.ctx, ast, auth, ref, later, op, &desc, nil)
			if err != nil {
				e.mu.Unlock()
				t.Fatal(form, err)
			}
			if len(rebound.querySources) != 1 || rebound.querySources[0].ref != bound.querySources[0].ref || !reflect.DeepEqual(rebound.querySources[0].uses, bound.querySources[0].uses) || !reflect.DeepEqual(rebound.parameters, bound.parameters) {
				e.mu.Unlock()
				t.Fatal("rebind dependencies changed")
			}
			if !reflect.DeepEqual(desc.temporals, before) || len(rebound.prepared.temporals) != 0 {
				e.mu.Unlock()
				t.Fatal("frozen captures replaced", desc.temporals, rebound.prepared.temporals)
			}
		}
		e.mu.Unlock()
	}
}

func TestBetweenTemporalCaptureContextKey(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	captures := []temporalConstant{
		{offset: 7, typeID: sqltype.Date, typmod: -1, scalar: 1},
		{offset: 7, typeID: sqltype.Timestamp, typmod: -1, scalar: 86400000000},
		{offset: 7, typeID: sqltype.Timestamp, typmod: 3, scalar: 86400123000},
		{offset: 7, typeID: sqltype.Timestamp, typmod: 6, scalar: 86400123456},
		{offset: 7, typeID: sqltype.Timestamp, typmod: 3, scalar: 86400123000},
	}
	desc := StatementDescription{temporals: captures}
	b.expected = &desc
	before := append([]temporalConstant(nil), captures...)
	for _, expected := range captures {
		usage := b.typ(expected.typeID).Usage()
		usage.Typmod = expected.typmod
		for range 2 {
			got, found, err := b.captured(7, usage)
			if err != nil || !found || got != expected {
				t.Fatal(got, found, err, expected)
			}
			value, err := b.inputConstant("invalid input must not be reparsed", usage, parser.Position{Offset: 7})
			if err != nil {
				t.Fatal(err)
			}
			var scalar int64
			if expected.typeID == sqltype.Date {
				scalar, err = value.Date()
			} else {
				scalar, err = value.Timestamp()
			}
			if err != nil || scalar != expected.scalar {
				t.Fatal(scalar, err, expected)
			}
		}
	}
	missing := b.typ(sqltype.Timestamp).Usage()
	missing.Typmod = 2
	if _, _, err := b.captured(7, missing); err == nil {
		t.Fatal("missing contextual key accepted")
	}
	if _, err := b.inputConstant("2000-01-02", missing, parser.Position{Offset: 7}); err == nil {
		t.Fatal("missing key silently reparsed")
	}
	if _, _, err := b.captureAt(7); err == nil {
		t.Fatal("ambiguous typed-literal metadata accepted")
	}
	if _, found, err := b.captured(8, missing); err != nil || found {
		t.Fatal(found, err)
	}
	if !reflect.DeepEqual(before, desc.temporals) || len(b.prepared.temporals) != 0 {
		t.Fatal("capture contract mutated", desc.temporals, b.prepared.temporals)
	}
	b.expected = &StatementDescription{temporals: []temporalConstant{captures[2], captures[4]}}
	if got, found, err := b.captureAt(7); err != nil || !found || got != captures[2] {
		t.Fatal("uniform frozen metadata rejected", got, found, err)
	}
}
