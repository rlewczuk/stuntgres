package core_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/core"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestBetweenTemporalCaptureContextKey(t *testing.T) {
	b := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
	captures := []analyze.TemporalConstant{
		{Offset: 7, TypeID: sqltype.Date, Typmod: -1, Scalar: 1},
		{Offset: 7, TypeID: sqltype.Timestamp, Typmod: -1, Scalar: 86400000000},
		{Offset: 7, TypeID: sqltype.Timestamp, Typmod: 3, Scalar: 86400123000},
		{Offset: 7, TypeID: sqltype.Timestamp, Typmod: 6, Scalar: 86400123456},
		{Offset: 7, TypeID: sqltype.Timestamp, Typmod: 3, Scalar: 86400123000},
	}
	desc := core.Description{Temporals: captures}
	b.Expected = core.ExpectedOf(&desc)
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
	if !reflect.DeepEqual(before, desc.Temporals) || len(b.Prepared.Temporals) != 0 {
		t.Fatal("capture contract mutated", desc.Temporals, b.Prepared.Temporals)
	}
	b.Expected = core.ExpectedOf(&core.Description{Temporals: []analyze.TemporalConstant{captures[2], captures[4]}})
	if got, found, err := b.CaptureAt(7); err != nil || !found || got != captures[2] {
		t.Fatal("uniform frozen metadata rejected", got, found, err)
	}
}
