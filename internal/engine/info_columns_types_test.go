package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestInfoColumnsTypeFacts(t *testing.T) {
	f := newColumnsUnitFixture(t)
	view := f.dc.f.e.catalog.Read(f.state)
	// Dimension order: max characters, max octets, numeric precision/radix/
	// scale, datetime precision. -1 is inapplicable, never zero/MAXINT.
	cases := []struct {
		id         sqltype.ID
		mods       []int32
		name       string
		dimensions [6]int64
	}{
		{sqltype.Int2, nil, "smallint", [6]int64{-1, -1, 16, 2, 0, -1}},
		{sqltype.Int4, nil, "integer", [6]int64{-1, -1, 32, 2, 0, -1}},
		{sqltype.Int8, nil, "bigint", [6]int64{-1, -1, 64, 2, 0, -1}},
		{sqltype.Float4, nil, "real", [6]int64{-1, -1, 24, 2, -1, -1}},
		{sqltype.Float8, nil, "double precision", [6]int64{-1, -1, 53, 2, -1, -1}},
		{sqltype.Numeric, nil, "numeric", [6]int64{-1, -1, -1, 10, -1, -1}},
		{sqltype.Numeric, []int32{12, 3}, "numeric", [6]int64{-1, -1, 12, 10, 3, -1}},
		{sqltype.Numeric, []int32{5, -2}, "numeric", [6]int64{-1, -1, 5, 10, 2046, -1}},
		{sqltype.Numeric, []int32{3, 5}, "numeric", [6]int64{-1, -1, 3, 10, 5, -1}},
		{sqltype.BPChar, []int32{3}, "character", [6]int64{3, 12, -1, -1, -1, -1}},
		{sqltype.BPChar, nil, "character", [6]int64{-1, 1073741824, -1, -1, -1, -1}},
		{sqltype.VarChar, []int32{7}, "character varying", [6]int64{7, 28, -1, -1, -1, -1}},
		{sqltype.VarChar, nil, "character varying", [6]int64{-1, 1073741824, -1, -1, -1, -1}},
		{sqltype.Text, nil, "text", [6]int64{-1, 1073741824, -1, -1, -1, -1}},
		{sqltype.Bit, []int32{5}, "bit", [6]int64{5, -1, -1, -1, -1, -1}},
		{sqltype.VarBit, []int32{9}, "bit varying", [6]int64{9, -1, -1, -1, -1, -1}},
		{sqltype.VarBit, nil, "bit varying", [6]int64{-1, -1, -1, -1, -1, -1}},
		{sqltype.Date, nil, "date", [6]int64{-1, -1, -1, -1, -1, 0}},
		{sqltype.Time, nil, "time without time zone", [6]int64{-1, -1, -1, -1, -1, 6}},
		{sqltype.Timetz, nil, "time with time zone", [6]int64{-1, -1, -1, -1, -1, 6}},
		{sqltype.Timestamp, []int32{2}, "timestamp without time zone", [6]int64{-1, -1, -1, -1, -1, 2}},
		{sqltype.Timestamptz, nil, "timestamp with time zone", [6]int64{-1, -1, -1, -1, -1, 6}},
		{sqltype.Interval, nil, "interval", [6]int64{-1, -1, -1, -1, -1, 6}},
		{sqltype.Bool, nil, "boolean", [6]int64{-1, -1, -1, -1, -1, -1}},
		{sqltype.Name, nil, "name", [6]int64{-1, -1, -1, -1, -1, -1}},
		{sqltype.Bytea, nil, "bytea", [6]int64{-1, -1, -1, -1, -1, -1}},
		{sqltype.JSON, nil, "json", [6]int64{-1, -1, -1, -1, -1, -1}},
		{sqltype.UUID, nil, "uuid", [6]int64{-1, -1, -1, -1, -1, -1}},
		{1007, nil, "ARRAY", [6]int64{-1, -1, -1, -1, -1, -1}},
		{22002, nil, "integer", [6]int64{-1, -1, 32, 2, 0, -1}},
		{22005, nil, "USER-DEFINED", [6]int64{-1, -1, -1, -1, -1, -1}},
	}
	for _, tc := range cases {
		typ, ok := view.LookupType(tc.id)
		if !ok {
			t.Fatal("missing type", tc.id)
		}
		u := typ.Usage()
		if len(tc.mods) != 0 {
			var err error
			u, _, err = typ.ResolveModifiers(tc.mods...)
			if err != nil {
				t.Fatal(err)
			}
		}
		facts, err := pgcatalog.InfoColumnTypeFacts(view, u)
		got := [6]int64{facts.CharacterMaximumLength, facts.CharacterOctetLength, facts.NumericPrecision, facts.NumericPrecisionRadix, facts.NumericScale, facts.DatetimePrecision}
		if err != nil || facts.DataType != tc.name || got != tc.dimensions {
			t.Fatal("type facts", tc.id, tc.mods, facts, got, tc.dimensions, err)
		}
		if tc.id == 22005 && (facts.Domain.Name() != "nn_child" || facts.UDT.Name() != "nn" || facts.Domain.SQLType.OwnDomainNotNull() || !facts.Domain.SQLType.DomainNotNull()) {
			t.Fatal("one-level metadata vs effective enforcement", facts)
		}
	}
	interval, _ := view.LookupType(sqltype.Interval)
	for _, tc := range []struct {
		mask int32
		name string
	}{
		{sqltype.IntervalYear, "YEAR"}, {sqltype.IntervalMonth, "MONTH"}, {sqltype.IntervalDay, "DAY"}, {sqltype.IntervalHour, "HOUR"}, {sqltype.IntervalMinute, "MINUTE"}, {sqltype.IntervalSecond, "SECOND"},
		{sqltype.IntervalYear | sqltype.IntervalMonth, "YEAR TO MONTH"},
		{sqltype.IntervalDay | sqltype.IntervalHour, "DAY TO HOUR"},
		{sqltype.IntervalDay | sqltype.IntervalHour | sqltype.IntervalMinute, "DAY TO MINUTE"},
		{sqltype.IntervalDay | sqltype.IntervalHour | sqltype.IntervalMinute | sqltype.IntervalSecond, "DAY TO SECOND"},
		{sqltype.IntervalHour | sqltype.IntervalMinute, "HOUR TO MINUTE"},
		{sqltype.IntervalHour | sqltype.IntervalMinute | sqltype.IntervalSecond, "HOUR TO SECOND"},
		{sqltype.IntervalMinute | sqltype.IntervalSecond, "MINUTE TO SECOND"},
	} {
		u, _, err := interval.ResolveModifiers(tc.mask)
		if err != nil {
			t.Fatal(err)
		}
		facts, err := pgcatalog.InfoColumnTypeFacts(view, u)
		if err != nil || facts.IntervalType != tc.name || facts.DatetimePrecision != 6 {
			t.Fatal(tc, facts, err)
		}
		if tc.mask&sqltype.IntervalSecond != 0 {
			u, _, err = interval.ResolveModifiers(tc.mask, 3)
			if err != nil {
				t.Fatal(err)
			}
			facts, err = pgcatalog.InfoColumnTypeFacts(view, u)
			if err != nil || facts.IntervalType != tc.name+"(3)" || facts.DatetimePrecision != 3 {
				t.Fatal(tc, facts, err)
			}
		}
	}
	foreign, _ := New().CatalogRead().LookupType(sqltype.Int4)
	if _, err := pgcatalog.InfoColumnTypeFacts(view, foreign.Usage()); err == nil {
		t.Fatal("foreign type generation accepted")
	}
	state := catalog.CloneState(f.state)
	integer := *view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)})
	integer.SQLType = foreign
	state.SetDescriptor(integer.Address(), &integer)
	usage := f.dc.nn.SQLType.Usage()
	if _, err := pgcatalog.InfoColumnTypeFacts(f.dc.f.e.catalog.Read(state), usage); err == nil {
		t.Fatal("stale immediate domain base accepted")
	}
}
