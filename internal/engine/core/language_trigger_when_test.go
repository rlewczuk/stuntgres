package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
)

func TestLanguageTriggerWhenDeferredOwnership(t *testing.T) {
	e := engine.New()
	owner := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE TRUSTED LANGUAGE language_trigger_when HANDLER pg_catalog.plpgsql_call_handler",
		"CREATE FUNCTION pg_catalog.language_trigger_when_fn() RETURNS trigger LANGUAGE language_trigger_when AS 'BEGIN RETURN NEW;'",
		"CREATE TABLE language_trigger_when_data(v integer)",
		"CREATE TRIGGER language_trigger_when_before BEFORE INSERT ON language_trigger_when_data FOR EACH ROW WHEN (NEW.v > 0) EXECUTE FUNCTION pg_catalog.language_trigger_when_fn()",
	} {
		enginetest.SessionSQL(t, owner, q)
	}
	read := e.CatalogRead()
	var declaration *catalog.Procedure
	for d := range catalog.DefinitionProceduresNamedAt(read, nil, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, "language_trigger_when_fn") {
		declaration = d
	}
	if declaration == nil || declaration.SQL == nil || declaration.SQL.Kind != proc.PLpgSQLBody || declaration.SQL.PL != nil || declaration.SQL.PLSyntax != nil || !catalog.SupportsDMLTrigger(declaration) {
		t.Fatal("missing genuine source-only trigger declaration")
	}
	definition := declaration.SQL
	before := *definition
	revision, language, ownerID := declaration.ObjectRevision, declaration.Language, declaration.Owner
	check := func() {
		t.Helper()
		current := e.CatalogRead()
		if current.State() != read.State() || current.Get(declaration.Address()) != declaration || read.Get(declaration.Address()) != declaration || declaration.SQL != definition || declaration.ObjectRevision != revision || declaration.Language != language || declaration.Owner != ownerID {
			t.Fatal("row firing changed catalog generation or retained declaration backing")
		}
		// A zero-input trigger has no formal backing to borrow or replace.
		if definition.Source != "BEGIN RETURN NEW;" || len(definition.Formals) != 0 || definition.PL != nil || definition.PLSyntax != nil || !reflect.DeepEqual(*definition, before) {
			t.Fatal("row firing compiled or mutated published source/formals/program backing")
		}
		if err := (stored.ProcedureProvider{}).Validate(current, declaration); err != nil {
			t.Fatal("source-only trigger lost stored admission", err)
		}
	}
	check()
	for _, q := range []string{"INSERT INTO language_trigger_when_data VALUES (-1)", "INSERT INTO language_trigger_when_data VALUES (NULL)"} {
		if r := enginetest.SessionSQL(t, owner, q); r.Count != 1 {
			t.Fatal("skipped trigger row affected count", r.Count)
		}
		check()
	}
	want := [][]driver.Value{{int64(-1)}, {nil}}
	rows := func() {
		t.Helper()
		if got := enginetest.Tuples(enginetest.SessionSQL(t, owner, "SELECT v FROM language_trigger_when_data ORDER BY v")); !reflect.DeepEqual(got, want) {
			t.Fatal("trigger statement row atomicity", got, want)
		}
	}
	rows()
	for range 2 {
		op, err := owner.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.Execute(op, enginetest.Statement(t, "INSERT INTO language_trigger_when_data VALUES (-2),(1),(NULL)"), 0, nil)
		op.Done()
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != "syntax error at end of input" || diagnostic.Position != "" || diagnostic.InternalPosition != "18" || diagnostic.InternalQuery != "BEGIN RETURN NEW;" || diagnostic.Context != "compilation of PL/pgSQL function \"language_trigger_when_fn\" near line 1" {
			t.Fatal("true WHEN did not reach exact deferred compiler diagnostic", err)
		}
		check()
		rows()
	}
	if r := enginetest.SessionSQL(t, owner, "INSERT INTO language_trigger_when_data VALUES (-3),(NULL)"); r.Count != 2 {
		t.Fatal("failed private compilation poisoned later skipped rows", r.Count)
	}
	check()
	want = [][]driver.Value{{int64(-3)}, {int64(-1)}, {nil}, {nil}}
	rows()
}
