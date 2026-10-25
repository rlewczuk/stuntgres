package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func extensionVersionUnitCreate(version string) *parser.CreateExtension {
	return &parser.CreateExtension{Name: parser.Identifier{Text: "fuzzystrmatch"}, Options: []parser.ExtensionOption{{Name: "version", Value: parser.Identifier{Text: version}}}}
}

func extensionVersionUnitUpdate() *parser.AlterExtensionUpdate {
	return &parser.AlterExtensionUpdate{Name: parser.Identifier{Text: "fuzzystrmatch"}, Options: []parser.ExtensionOption{{Name: "version", Value: parser.Identifier{Text: "1.2"}}}}
}

func extensionVersionUnitInstalled(t *testing.T, version string) (*core.Engine, *session.Operation, *catalog.Extension) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	if err := extensionUnitExecute(t, e, op, extensionVersionUnitCreate(version)); err != nil {
		t.Fatal(err)
	}
	ext := e.CatalogRead().MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "fuzzystrmatch"}))
	return e, op, ext
}
