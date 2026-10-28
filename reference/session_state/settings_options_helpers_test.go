package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func settingsOptionsFlags(pg bool, query string, want any, args ...any) fixture.Step {
	s := fixture.ArrayReferenceSelect(query, []string{"pg_settings_get_flags"}, []string{"_TEXT"}, []string{"_TEXT"}, [][]any{{want}}, pg, args...)
	base := s.CheckMetadata
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		base(t, rows)
		types,err := rows.ColumnTypes()
		if err != nil || len(types) != 1 { t.Fatal(types,err) }
		n,known := types[0].Nullable()
		if pg && (n || known) || !pg && (!n || !known) { t.Fatal("flags nullability",n,known) }
		if p,s,ok := types[0].DecimalSize(); p != 0 || s != 0 || ok { t.Fatal(p,s,ok) }
	}
	return s
}

func settingsOptionsRows(pg bool, query string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, []string{"option_name","option_value"}, []string{"TEXT","TEXT"}, want, args...)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { settingsOptionsTextMetadata(t,rows,pg,[]string{"option_name","option_value"}) }
	return s
}

func settingsOptionsTextMetadata(t *testing.T, rows *sql.Rows, pg bool, names []string) {
	t.Helper()
	columns,err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns,names) { t.Fatal(columns,err) }
	types,err := rows.ColumnTypes()
	if err != nil || len(types) != len(names) { t.Fatal(types,err) }
	for i,typ := range types {
		if typ.Name() != names[i] || typ.DatabaseTypeName() != "TEXT" || typ.ScanType() != reflect.TypeFor[string]() { t.Fatal(typ) }
		if length,known := typ.Length(); length != math.MaxInt64 || !known { t.Fatal(length,known) }
		if p,s,known := typ.DecimalSize(); p != 0 || s != 0 || known { t.Fatal(p,s,known) }
		// Fixed OUT members are computed source columns, not declared table
		// attributes. Both adapters expose unknown nullability (false,false);
		// this does not claim that option_value cannot contain SQL NULL.
		n,known := typ.Nullable()
		if n || known { t.Fatal("computed options nullability",pg,n,known) }
	}
}

func settingsOptionsNullElement(query string, args ...any) fixture.Step {
	return settingsLabelFailure(query,fixture.Diagnostic{Code:"22004",Message:"null array element not allowed in this context"},args...)
}
