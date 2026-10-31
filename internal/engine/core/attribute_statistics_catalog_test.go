package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestAttributeStatisticsPolymorphicOrderingAdmission(t *testing.T) {
	read := engine.New().CatalogRead()
	for _, tc := range []struct {
		name string
		typeID sqltype.ID
		want bool
	}{
		{"integer", 23, true},
		{"integer array", 1007, true},
		{"name array", 1003, true},
		{"json", 114, false},
		{"json array", 199, false},
		{"integer range", 3904, true},
		{"integer multirange", 4451, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			typ, found := read.LookupType(tc.typeID)
			if !found {
				t.Fatal("missing released type", tc.typeID)
			}
			got, err := storage.AttributeStatisticsHasLessThan(read, typ.Usage())
			if err != nil || got != tc.want {
				t.Fatalf("less-than availability = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}
