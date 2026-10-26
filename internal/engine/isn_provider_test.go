package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// isnUnitExecute runs one SQL statement that returns no rows on op.
func isnUnitExecute(t *testing.T, e *core.Engine, op *session.Operation, q string) {
	t.Helper()
	r, err := e.Execute(op, statement(t, q), 0, nil)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	if r.Rows != nil {
		if err := r.Rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// isnUnitInstalled returns a fresh engine with the isn extension created
// through SQL, its catalog entry and its first member type.
func isnUnitInstalled(t *testing.T) (*core.Engine, *catalog.Extension, *catalog.Type) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	isnUnitExecute(t, e, op, "CREATE EXTENSION isn")
	ext := e.Catalog.State.MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "isn"}))
	return e, ext, e.Catalog.State.MustType(ext.Members[0])
}
