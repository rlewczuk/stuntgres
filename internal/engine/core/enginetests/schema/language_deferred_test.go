package schema_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
)

func TestLanguageInvocationCurrentCapability(t *testing.T) {
	e := engine.New()
	owner := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, owner, "CREATE FUNCTION pg_catalog.language_current() RETURNS integer LANGUAGE sql AS 'SELECT 5'")
	read := e.CatalogRead()
	var declaration *catalog.Procedure
	for d := range catalog.DefinitionProceduresNamedAt(read, nil, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, "language_current") {
		declaration = d
	}
	if declaration == nil {
		t.Fatal("missing SQL declaration")
	}
	language, err := catalog.LookupLanguage(read, "sql")
	if err != nil {
		t.Fatal(err)
	}
	pl, err := catalog.LookupLanguage(read, "plpgsql")
	if err != nil {
		t.Fatal(err)
	}
	candidate := catalog.NewCandidate(read)
	replacement := *language
	replacement.ObjectRevision++
	replacement.Procedural, replacement.Call, replacement.Inline, replacement.Validator = true, pl.Call, pl.Inline, pl.Validator
	if err := candidate.Replace(&replacement); err != nil {
		t.Fatal(err)
	}
	private := read.WithState(candidate.State())
	if _, err := catalog.ResolveLanguageExecution(private, &replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.ValidateStoredRoutineDescriptor(private, declaration); err == nil {
		t.Fatal("current procedural replacement incorrectly retained captured SQL execution authority")
	}
	if _, err := exec.ValidateStoredRoutineDescriptor(read, declaration); err != nil {
		t.Fatal("private rejected invocation changed original authority", err)
	}
	if read.Get(language.Address()) != language || language.Procedural || declaration.SQL.PL != nil {
		t.Fatal("replacement mutated retained descriptor/body backing")
	}
}
