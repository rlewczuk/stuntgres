package engine

import (
	"testing"

	"stuntgres/internal/sqltype"
)

func cteUnitType(t *testing.T) *sqltype.Descriptor {
	t.Helper()
	typ, ok := New().CatalogRead().LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("INT4 descriptor not found")
	}
	return typ
}

func cteUnitValue(t *testing.T, typ *sqltype.Descriptor, n int64) sqltype.Value {
	t.Helper()
	value, err := sqltype.Integer(typ, n)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
