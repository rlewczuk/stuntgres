package core_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqltype"
)

func TestUpdateFromOuterReturningDescriptors(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id INT NOT NULL,v INT NOT NULL)", "CREATE TABLE s(k INT NOT NULL,n INT NOT NULL)", "CREATE TABLE r(k INT NOT NULL,z INT NOT NULL)"} {
		enginetest.SessionSQL(t, s, q)
	}
	op := enginetest.TransactionOperation(t, s)
	defer op.Done()
	physical := enginetest.Statement(t, "SELECT s.*,r.* FROM s CROSS JOIN r")
	before, err := e.Describe(op, physical)
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range before.Results {
		if column.Nullable || !column.NullabilityKnown {
			t.Fatal("physical source NOT NULL", column)
		}
	}
	for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
		description, err := e.Describe(op, enginetest.Statement(t, "UPDATE t x SET v=r.z FROM s "+kind+" JOIN r USING(k) WHERE x.id=k RETURNING x.id,old.v,new.v,s.k,s.n,r.k,r.z,k"))
		if err != nil {
			t.Fatal(kind, err)
		}
		if len(description.Results) != 8 {
			t.Fatal(kind, description)
		}
		for i, column := range description.Results {
			if column.Usage.Type.ID() != sqltype.Int4 {
				t.Fatal(kind, i, column)
			}
			if i < 3 && (column.Nullable || !column.NullabilityKnown || column.OriginTable == 0) {
				t.Fatal("destination declaration changed", kind, i, column)
			}
			if i >= 3 && i < 7 {
				wantNullable := kind == "FULL" || kind == "RIGHT" && i < 5 || kind == "LEFT" && i >= 5
				if !column.NullabilityKnown || column.Nullable != wantNullable || column.OriginTable == 0 {
					t.Fatal("outward source nullability", kind, i, column)
				}
			}
		}
		merged := description.Results[7]
		if kind == "FULL" {
			if merged.OriginTable != 0 || merged.OriginAttribute != 0 {
				t.Fatal("computed merged key fabricated physical origin", merged)
			}
		} else {
			chosen := description.Results[3]
			if kind == "RIGHT" {
				chosen = description.Results[5]
			}
			if merged.OriginTable != chosen.OriginTable || merged.OriginAttribute != chosen.OriginAttribute || merged.Nullable || !merged.NullabilityKnown {
				t.Fatal("chosen merged key descriptor", kind, merged, chosen)
			}
		}
	}
	after, err := e.Describe(op, physical)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("UPDATE outward nullability mutated physical catalog descriptors", before, after, err)
	}
	enginetest.LockEmpty(t, e)
}
