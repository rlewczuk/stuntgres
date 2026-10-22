package typedef

import (
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestMACADDRStorageAdmission(t *testing.T) {
	if UnsupportedColumnType(parser.TypeName{Name: "macaddr"}) {
		t.Fatal("scalar still unsupported")
	}
	if UnsupportedColumnType(parser.TypeName{Name: "macaddr8"}) {
		t.Fatal("macaddr8 scalar still unsupported")
	}
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Macaddr && (StoredArrayElement(d) || !sqltype.StoredScalarCastType(d.ID())) {
			t.Fatal("scalar/array storage boundary", d)
		}
		if d.ID() == 1040 && (d.Concrete() || StoredArrayElement(d)) {
			t.Fatal("array became executable", d)
		}
	}
}
