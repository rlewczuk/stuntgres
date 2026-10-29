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
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
)

func TestLanguageDeferredDefinitionOwnership(t *testing.T) {
	e := engine.New()
	owner := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, owner, "CREATE TRUSTED LANGUAGE language_deferred HANDLER pg_catalog.plpgsql_call_handler")
	enginetest.SessionSQL(t, owner, "CREATE FUNCTION pg_catalog.language_source(x integer) RETURNS integer LANGUAGE language_deferred AS $$BEGIN RETURN x+1; END$$")
	read := e.CatalogRead()
	var declaration *catalog.Procedure
	for d := range catalog.DefinitionProceduresNamedAt(read, nil, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, "language_source") {
		declaration = d
	}
	if declaration == nil || declaration.SQL == nil || declaration.SQL.Kind != proc.PLpgSQLBody || declaration.SQL.PL != nil || declaration.SQL.PLSyntax != nil {
		t.Fatal("missing legitimate source-only declaration")
	}
	definition := declaration.SQL
	formal := &definition.Formals[0]
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var first, second *catalog.Procedure
	if err == nil {
		first, err = e.PreparePLRoutineLocked(op, auth, declaration)
	}
	if err == nil {
		second, err = e.PreparePLRoutineLocked(op, auth, declaration)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if first == declaration || second == declaration || first == second || first.SQL == definition || first.SQL == second.SQL || first.SQL.PL == nil || first.SQL.PL == second.SQL.PL || first.SQL.PLSyntax == nil {
		t.Fatal("private invocation compilation reused published/shared executable")
	}
	if first.SQL.PL.ValidateDefinition(first.SQL) != nil || second.SQL.PL.ValidateDefinition(second.SQL) != nil {
		t.Fatal("private compiler did not produce genuine executable definitions")
	}
	if &first.SQL.Formals[0] == formal || &second.SQL.Formals[0] == formal || first.SQL.Position != definition.Position {
		t.Fatal("private compiler borrowed mutable formals or lost source coordinates")
	}
	if read.Get(declaration.Address()) != declaration || e.CatalogRead().Get(declaration.Address()) != declaration || declaration.SQL != definition || definition.PL != nil || definition.PLSyntax != nil || &definition.Formals[0] != formal {
		t.Fatal("invocation compilation mutated published source-only backing")
	}
	mapping := make(map[ids.ObjectAddress]ids.ObjectAddress)
	for _, source := range []ids.ObjectAddress{declaration.Address(), declaration.Language} {
		destination, twice := source, source
		destination.ObjectID += 100000
		twice.ObjectID += 200000
		mapping[source], mapping[destination] = destination, twice
	}
	remap := stored.NewRoutineRemap(read, mapping, nil, nil, false)
	mapped, err := remap.Procedure(declaration)
	if err != nil {
		t.Fatal(err)
	}
	if err := remap.Finish(); err != nil {
		t.Fatal(err)
	}
	if mapped == declaration || mapped.Address() != mapping[declaration.Address()] || mapped.Language != mapping[declaration.Language] || mapped.SQL.PL != nil || mapped.SQL.PLSyntax != nil || mapped.SQL.Source != definition.Source || mapped.SQL.Kind != proc.PLpgSQLBody {
		t.Fatal("source-only payload lost state or remapped identity more than once")
	}
	if declaration.Language == mapped.Language || read.Get(declaration.Address()) != declaration || definition.PL != nil {
		t.Fatal("destination remap changed source authority/backing")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	previousContext := op.Ctx
	op.Ctx = ctx
	defer func() { op.Ctx = previousContext }()
	e.Mu.Lock()
	_, err = e.PreparePLRoutineLocked(op, auth, declaration)
	e.Mu.Unlock()
	if !errors.Is(err, context.Canceled) || declaration.SQL != definition || definition.PL != nil || definition.PLSyntax != nil {
		t.Fatal("canceled compilation changed published source", err)
	}
}

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
