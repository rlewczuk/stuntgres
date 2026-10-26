package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func routineExtendedMetadata(t *testing.T, b fixture.Backend, column *sql.ColumnType, typ string, nullable, known bool) {
	t.Helper()
	wantScan := reflect.TypeFor[string]()
	pseudoNull := typ == "ANYARRAY" || typ == "ANYRANGE" || typ == "ANYMULTIRANGE" || typ == "ANYCOMPATIBLEARRAY" || typ == "ANYCOMPATIBLERANGE" || typ == "ANYCOMPATIBLEMULTIRANGE" || typ == "PG_DDL_COMMAND"
	if pseudoNull || b.PG && typ != "TEXT" { wantScan = reflect.TypeFor[any]() }
	if b.PG { nullable,known = false,false }
	n,k := column.Nullable()
	if column.ScanType() != wantScan || n != nullable || k != known { t.Fatalf("%s raw metadata: scan=%v nullable=(%v,%v), want scan=%v nullable=(%v,%v)",typ,column.ScanType(),n,k,wantScan,nullable,known) }
	length,lengthKnown := column.Length()
	if typ == "TEXT" {
		if !lengthKnown || length != math.MaxInt64 { t.Fatalf("TEXT length (%d,%v)",length,lengthKnown) }
	} else if lengthKnown { t.Fatalf("%s unexpected length (%d,%v)",typ,length,lengthKnown) }
	precision,scale,decimalKnown := column.DecimalSize()
	if (typ == "NUMERIC" || typ == "_NUMERIC") && b.PG {
		if !decimalKnown || precision != 65535 || scale != 65531 { t.Fatal("numeric decimal metadata",precision,scale,decimalKnown) }
	} else if decimalKnown { t.Fatal("unexpected decimal metadata",typ,precision,scale,decimalKnown) }
}
