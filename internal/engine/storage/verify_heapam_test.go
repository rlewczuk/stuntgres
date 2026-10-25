package storage

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestVerifyHeapamLogicalRows(t *testing.T) {
	var int4, text *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		switch d.ID() { case sqltype.Int4: int4 = d; case sqltype.Text: text = d }
	}
	integer := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(int4, n)
		if err != nil { t.Fatal(err) }
		return v
	}
	good := func() HeapVerification {
		return HeapVerification{Columns: []catalog.Column{{Name: "a", Usage: int4.Usage()}, {Name: "b", Usage: text.Usage()}}, Attributes: []int32{1, 3}, Committed: []RowVersion{
			{Row: 11, Version: 101, Born: 5, Values: []sqltype.Value{integer(7), sqltype.String(text, "ok")}},
			{Row: 12, Version: 102, Born: 6, Values: []sqltype.Value{integer(7), sqltype.String(text, "ok")}},
		}}
	}
	check := func(t *testing.T, in HeapVerification, stop bool, want []HeapFinding) {
		t.Helper()
		before := append([]RowVersion(nil), in.Committed...)
		ownBefore := append([]RowVersion(nil), in.Own...)
		ownValues := make([][]sqltype.Value, len(in.Own))
		for i, row := range in.Own { ownValues[i] = append([]sqltype.Value(nil), row.Values...) }
		values := make([][]sqltype.Value, len(in.Committed))
		for i, row := range in.Committed { values[i] = append([]sqltype.Value(nil), row.Values...) }
		got, err := VerifyHeapam(context.Background(), in, stop)
		if err != nil || !reflect.DeepEqual(got, want) { t.Fatalf("stop=%v findings=%+v err=%v want=%+v", stop, got, err, want) }
		if !reflect.DeepEqual(in.Committed, before) { t.Fatal("committed headers changed") }
		for i, row := range in.Committed { if !reflect.DeepEqual(row.Values, values[i]) { t.Fatal("captured values changed", i) } }
		if !reflect.DeepEqual(in.Own, ownBefore) { t.Fatal("private headers changed") }
		for i, row := range in.Own { if !reflect.DeepEqual(row.Values, ownValues[i]) { t.Fatal("private values changed", i) } }
	}
	t.Run("healthy-retained-and-private", func(t *testing.T) {
		in := good()
		in.Committed[0].Frozen, in.Committed[0].ImportFrozen = true, true
		in.Committed[1].Born = 999
		in.Committed[1].Values = []sqltype.Value{sqltype.Null(int4), sqltype.Null(text)}
		in.Own = []RowVersion{{Row: 11, Version: 201, Base: 101, Values: []sqltype.Value{integer(8), sqltype.Null(text)}}, {Row: 19, Version: 209, Values: []sqltype.Value{integer(9), sqltype.String(text, "new")}}}
		in.Ends = &DeletionRoot{Rows: map[ids.RowID]RowEnd{11: {Version: 101, End: 6}, 12: {Version: 99, End: 0}}}
		check(t, in, false, nil)
		check(t, in, true, nil)
		in.Committed, in.Own = nil, nil
		check(t, in, false, nil)
	})
	for _, null := range []bool{false, true} {
		t.Run(map[bool]string{false: "wrong-typed-value", true: "wrong-typed-null"}[null], func(t *testing.T) {
			in := good()
			in.Committed[0].Values[1] = integer(8)
			if null { in.Committed[0].Values[1] = sqltype.Null(int4) }
			check(t, in, false, []HeapFinding{{Row: 11, Version: 101, Attribute: 3, Message: "logical row 11 version 101: attribute 3 has an invalid typed value"}})
		})
	}
	for _, multiple := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop-at-version", true: "finish-first-version"}[multiple], func(t *testing.T) {
			in := good()
			in.Committed[0].Values = in.Committed[0].Values[:1]
			in.Committed[1].Values[0] = sqltype.String(text, "bad")
			first := []HeapFinding{{Row: 11, Version: 101, Message: "logical row 11 version 101: row has 1 values but relation has 2 columns"}}
			if multiple {
				in.Committed[0].Values[0] = sqltype.String(text, "bad")
				first = append(first, HeapFinding{Row: 11, Version: 101, Attribute: 1, Message: "logical row 11 version 101: attribute 1 has an invalid typed value"})
			}
			check(t, in, true, first)
			all := append(append([]HeapFinding(nil), first...), HeapFinding{Row: 12, Version: 102, Attribute: 1, Message: "logical row 12 version 102: attribute 1 has an invalid typed value"})
			check(t, in, false, all)
		})
	}
	for _, tc := range []struct {
		name string
		mutate func(*HeapVerification)
		want HeapFinding
	}{
		{"row-zero", func(in *HeapVerification) { in.Committed[0].Row = 0 }, HeapFinding{Version: 101, Message: "logical row 0 version 101: row identity is zero"}},
		{"version-zero", func(in *HeapVerification) { in.Committed[0].Version = 0 }, HeapFinding{Row: 11, Message: "logical row 11 version 0: version identity is zero"}},
		{"duplicate-version", func(in *HeapVerification) { in.Committed[1].Version = 101 }, HeapFinding{Row: 12, Version: 101, Message: "logical row 12 version 101: duplicate version identity"}},
		{"birth-zero", func(in *HeapVerification) { in.Committed[0].Born = 0 }, HeapFinding{Row: 11, Version: 101, Message: "logical row 11 version 101: committed version has no birth sequence"}},
		{"private-birth", func(in *HeapVerification) { in.Own = in.Committed[:1]; in.Committed = nil }, HeapFinding{Row: 11, Version: 101, Message: "logical row 11 version 101: private version has a committed birth sequence"}},
		{"committed-base", func(in *HeapVerification) { in.Committed[0].Base = 99 }, HeapFinding{Row: 11, Version: 101, Message: "logical row 11 version 101: committed version retains a private predecessor"}},
		{"end-before-birth", func(in *HeapVerification) { in.Ends = &DeletionRoot{Rows: map[ids.RowID]RowEnd{11: {Version: 101, End: 4}}} }, HeapFinding{Row: 11, Version: 101, Message: "logical row 11 version 101: invalid committed end sequence"}},
		{"end-zero", func(in *HeapVerification) { in.Ends = &DeletionRoot{Rows: map[ids.RowID]RowEnd{11: {Version: 101}}} }, HeapFinding{Row: 11, Version: 101, Message: "logical row 11 version 101: invalid committed end sequence"}},
	} {
		t.Run(tc.name, func(t *testing.T) { in := good(); tc.mutate(&in); check(t, in, false, []HeapFinding{tc.want}) })
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if findings, err := VerifyHeapam(ctx, good(), false); !errors.Is(err, context.Canceled) || findings != nil { t.Fatal("canceled scan", findings, err) }
}
