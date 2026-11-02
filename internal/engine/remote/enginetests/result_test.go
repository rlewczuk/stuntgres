package remote_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/remote"
)

func TestExternalSQLProviderResultValidation(t *testing.T) {
	column := catalog.ExternalSQLColumn{Name: "v", TypeOID: 23, TypeModifier: -1}
	for _, result := range []catalog.ExternalSQLResult{
		{CommandTag: "CREATE TABLE"},
		{Tuple: true, Columns: []catalog.ExternalSQLColumn{column}},
		{Tuple: true, RowCount: 3}, // Genuine zero-width tuples still have multiplicity.
		{Tuple: true, Columns: []catalog.ExternalSQLColumn{column}, RowCount: 2, Cells: []catalog.ExternalSQLCell{{Text: "7"}, {Null: true}}},
	} {
		if err := remote.ValidateResult(result); err != nil {
			t.Fatal("valid owned result rejected", result, err)
		}
	}
	for _, result := range []catalog.ExternalSQLResult{
		{Tuple: true, RowCount: -1},
		{RowsAffected: -1},
		{Columns: []catalog.ExternalSQLColumn{column}},
		{Tuple: true, Cells: []catalog.ExternalSQLCell{{Text: "7"}}},
		{Tuple: true, Columns: []catalog.ExternalSQLColumn{column}, RowCount: 2, Cells: []catalog.ExternalSQLCell{{Text: "7"}}},
		{Tuple: true, Columns: []catalog.ExternalSQLColumn{{Name: "v"}}, RowCount: 1, Cells: []catalog.ExternalSQLCell{{Text: "7"}}},
		{Tuple: true, Columns: []catalog.ExternalSQLColumn{{Name: "v", TypeOID: 23, Format: 1}}},
		{Tuple: true, Columns: []catalog.ExternalSQLColumn{column}, RowCount: 1, Cells: []catalog.ExternalSQLCell{{Text: "not NULL", Null: true}}},
	} {
		if err := remote.ValidateResult(result); err == nil {
			t.Fatal("malformed owned result accepted", result)
		}
	}
}
