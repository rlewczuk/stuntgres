package core_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqlstate"
)

func TestLanguageDeferredReplayOwnership(t *testing.T) {
	source, destination := engine.New(), engine.New()
	owner := enginetest.StartSession(t, source, nil)
	enginetest.SessionSQL(t, owner, "CREATE TRUSTED LANGUAGE language_replay_source HANDLER pg_catalog.plpgsql_call_handler")
	enginetest.SessionSQL(t, owner, "CREATE FUNCTION pg_catalog.language_replay_good(x integer) RETURNS integer LANGUAGE language_replay_source AS $$BEGIN RETURN x+1; END$$")
	enginetest.SessionSQL(t, owner, "CREATE FUNCTION pg_catalog.language_replay_bad() RETURNS integer LANGUAGE language_replay_source AS 'BEGIN RETURN 1;'")
	enginetest.SessionSQL(t, owner, "CREATE FUNCTION pg_catalog.language_replay_checked() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 5; END$$")
	find := func(read catalog.Read, name string) *catalog.Procedure {
		t.Helper()
		for d := range catalog.DefinitionProceduresNamedAt(read, nil, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, name) {
			return d
		}
		t.Fatal("missing replay declaration", name)
		return nil
	}
	original := source.CatalogRead()
	bad := find(original, "language_replay_bad")
	badDefinition := bad.SQL
	if badDefinition.PL != nil || badDefinition.PLSyntax != nil {
		t.Fatal("malformed source was not legitimately deferred")
	}
	var saved bytes.Buffer
	if err := engine.Dump(context.Background(), source, &saved); err != nil {
		t.Fatal(err)
	}
	var previous *catalog.Procedure
	for range 2 {
		if err := engine.Restore(context.Background(), destination, bytes.NewReader(saved.Bytes())); err != nil {
			t.Fatal(err)
		}
		read := destination.CatalogRead()
		for _, name := range []string{"language_replay_good", "language_replay_bad"} {
			d := find(read, name)
			if d.SQL == nil || d.SQL.Kind != proc.PLpgSQLBody || d.SQL.PL != nil || d.SQL.PLSyntax != nil {
				t.Fatal("restore eagerly compiled source-only declaration", name)
			}
			if _, err := exec.ValidateStoredRoutineDescriptor(read, d); err != nil {
				t.Fatal("destination source declaration lost current authority", err)
			}
		}
		current := find(read, "language_replay_bad")
		if current == bad || current.SQL == badDefinition || current.Owner == bad.Owner || current.SQL.Source != badDefinition.Source {
			t.Fatal("independent destination borrowed source declaration/principal backing")
		}
		if previous != nil && (previous == current || previous.SQL == current.SQL || previous.SQL.PL != nil || previous.SQL.PLSyntax != nil) {
			t.Fatal("second replacement reused or mutated previous immutable source-only backing")
		}
		if checked := find(read, "language_replay_checked"); checked.SQL.PL == nil || checked.SQL.PLSyntax == nil || checked.SQL.PL.ValidateDefinition(checked.SQL) != nil {
			t.Fatal("source-only replay bypassed genuine validated declaration reconstruction")
		}
		previous = current
	}
	op, done := enginetest.RoleOperation(t, destination, "postgres")
	defer done()
	destination.Mu.Lock()
	auth, err := destination.AuthorizationLocked(op)
	var executable *catalog.Procedure
	if err == nil {
		executable, err = destination.PreparePLRoutineLocked(op, auth, previous)
	}
	destination.Mu.Unlock()
	var diagnostic *sqlstate.Error
	if executable != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Position != "" || diagnostic.InternalPosition != "16" || diagnostic.InternalQuery != "BEGIN RETURN 1;" {
		t.Fatal("restored malformed source lost deferred invocation failure", executable, err)
	}
	if destination.CatalogRead().Get(previous.Address()) != previous || previous.SQL.PL != nil || previous.SQL.PLSyntax != nil {
		t.Fatal("failed reached compilation changed destination publication")
	}
	if source.CatalogRead().State() != original.State() || original.Get(bad.Address()) != bad || bad.SQL != badDefinition || badDefinition.PL != nil || badDefinition.PLSyntax != nil {
		t.Fatal("destination replay or failed invocation changed source publication/backing")
	}
}
