package typedef

import (
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestMACADDR8StorageAdmission(t *testing.T) {
	if UnsupportedColumnType(parser.TypeName{Name: "macaddr8"}) {
		t.Fatal("scalar unsupported")
	}
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Macaddr8 && (StoredArrayElement(d) || !sqltype.StoredScalarCastType(d.ID())) {
			t.Fatal("scalar/array boundary", d)
		}
		if d.ID() == 775 && (d.Concrete() || StoredArrayElement(d)) {
			t.Fatal("array executable", d)
		}
	}
}
