package core_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
)

func TestLanguageDeferredSignatureOwnership(t *testing.T) {
	source, destination := engine.New(), engine.New()
	owner := enginetest.StartSession(t, source, nil)
	enginetest.SessionSQL(t, owner, "CREATE TRUSTED LANGUAGE language_signature HANDLER pg_catalog.plpgsql_call_handler")
	enginetest.SessionSQL(t, owner, "CREATE FUNCTION pg_catalog.language_signature_result() RETURNS cstring LANGUAGE language_signature AS 'BEGIN RETURN NULL; END'")
	enginetest.SessionSQL(t, owner, "CREATE FUNCTION pg_catalog.language_signature_argument(x cstring) RETURNS integer LANGUAGE language_signature AS 'BEGIN RETURN NULL; END'")
	find := func(read catalog.Read, name string) *catalog.Procedure {
		t.Helper()
		for d := range catalog.DefinitionProceduresNamedAt(read, nil, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, name) {
			return d
		}
		t.Fatal("missing source-only signature declaration", name)
		return nil
	}
	names := []string{"language_signature_result", "language_signature_argument"}
	original := source.CatalogRead()
	retained := []*catalog.Procedure{find(original, names[0]), find(original, names[1])}
	definitions := []*proc.Definition{retained[0].SQL, retained[1].SQL}
	argument := &definitions[1].Formals[0]
	check := func(e *core.Engine, previous []*catalog.Procedure) []*catalog.Procedure {
		t.Helper()
		read := e.CatalogRead()
		current := make([]*catalog.Procedure, len(names))
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		for i, name := range names {
			d := find(read, name)
			current[i] = d
			definition := d.SQL
			if definition == nil || definition.Kind != proc.PLpgSQLBody || definition.Source != "BEGIN RETURN NULL; END" || definition.PL != nil || definition.PLSyntax != nil {
				t.Fatal("published validator-sensitive source was eagerly compiled", name)
			}
			if err := (stored.ProcedureProvider{}).Validate(read, d); err != nil {
				t.Fatal("stored admission rejected genuine source-only signature", name, err)
			}
			if e != source && (d == retained[i] || definition == definitions[i] || d.Owner == retained[i].Owner) {
				t.Fatal("independent engine borrowed source descriptor/definition/principal", name)
			}
			if previous != nil && (d == previous[i] || definition == previous[i].SQL) {
				t.Fatal("replacement reused prior definition backing", name)
			}
			if i == 1 && e != source && &definition.Formals[0] == argument {
				t.Fatal("destination borrowed source formal backing")
			}
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			var executable *catalog.Procedure
			if err == nil {
				executable, err = e.PreparePLRoutineLocked(op, auth, d)
			}
			e.Mu.Unlock()
			message := "PL/pgSQL functions cannot return type cstring"
			if i == 1 {
				message = "PL/pgSQL functions cannot accept type cstring"
			}
			var diagnostic *sqlstate.Error
			if executable != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != message || diagnostic.Position != "" || diagnostic.Context != "compilation of PL/pgSQL function \""+name+"\" near line 1" {
				t.Fatal("reached compiler did not enforce current PL signature contract", executable, err)
			}
			if e.CatalogRead().State() != read.State() || read.Get(d.Address()) != d || d.SQL != definition || definition.PL != nil || definition.PLSyntax != nil {
				t.Fatal("rejected private compilation changed published source backing", name)
			}
		}
		return current
	}
	check(source, nil)
	var saved bytes.Buffer
	if err := engine.Dump(context.Background(), source, &saved); err != nil {
		t.Fatal(err)
	}
	var previous []*catalog.Procedure
	for range 2 {
		if err := engine.Restore(context.Background(), destination, bytes.NewReader(saved.Bytes())); err != nil {
			t.Fatal(err)
		}
		previous = check(destination, previous)
	}
	if source.CatalogRead().State() != original.State() || retained[0].SQL != definitions[0] || retained[1].SQL != definitions[1] || &definitions[1].Formals[0] != argument {
		t.Fatal("destination replay mutated source publication/backing")
	}
	// A changed current validator cannot retroactively invalidate catalog data.
	enginetest.SessionSQL(t, owner, "CREATE OR REPLACE TRUSTED LANGUAGE language_signature HANDLER pg_catalog.plpgsql_call_handler VALIDATOR pg_catalog.plpgsql_validator")
	check(source, nil)
	for range 2 {
		if err := engine.Reset(context.Background(), source); err != nil {
			t.Fatal(err)
		}
		if err := engine.Restore(context.Background(), source, bytes.NewReader(saved.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(source, nil)
	}
	for i, d := range retained {
		if original.Get(d.Address()) != d || d.SQL != definitions[i] || d.SQL.PL != nil || d.SQL.PLSyntax != nil {
			t.Fatal("source reset/restore mutated retained immutable declaration")
		}
	}
}
