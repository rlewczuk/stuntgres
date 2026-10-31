package core_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
)

func checkedDefaultOwnerProcedure(t *testing.T, read catalog.Read, name string) *catalog.Procedure {
	t.Helper()
	for _, d := range read.Descriptors() {
		if p, ok := d.(*catalog.Procedure); ok && p.Name() == name {
			return p
		}
	}
	t.Fatal("missing genuine checked declaration", name)
	return nil
}
