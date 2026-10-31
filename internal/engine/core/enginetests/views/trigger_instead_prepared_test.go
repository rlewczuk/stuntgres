package views_test

import (
	"context"
	"errors"
	"testing"

	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func TestTriggerInsteadPreparedOwnership(t *testing.T) {
	e := rootengine.New()
	owner := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, owner, "CREATE TABLE instead_owner_base(id integer,v integer)")
	enginetest.SessionSQL(t, owner, "CREATE VIEW instead_owner_view AS SELECT id,v FROM instead_owner_base")
	enginetest.SessionSQL(t, owner, "CREATE FUNCTION instead_owner_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$")
	enginetest.SessionSQL(t, owner, "CREATE TRIGGER body INSTEAD OF INSERT ON instead_owner_view FOR EACH ROW EXECUTE FUNCTION instead_owner_body()")
	enginetest.SessionSQL(t, owner, "BEGIN")
	var routine *catalog.Procedure
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	for candidate := range catalog.DefinitionProceduresNamedAt(e.CatalogRead(), nil, namespace, "instead_owner_body") {
		routine = candidate
	}
	if routine == nil {
		t.Fatal("missing installed executable")
	}
	program := routine.SQL.PL
	cycle := owner.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) {
		enginetest.PreparedFoldProtocolCommand(t, e, owner, cycle, fn)
	}
	registry := core.PayloadOf(owner).PreparedSQL
	command(func(s *core.StatementScope) error {
		_, err := s.ParseProtocol("insteadparams", "INSERT INTO instead_owner_view VALUES(COALESCE(-(7::integer),0),$1) RETURNING id,v", nil)
		return err
	})
	parameters := registry.Lookup("insteadparams")
	if parameters == nil || parameters.Description.Analysis == nil || registry.Binding("insteadparams_old") != nil {
		t.Fatal("Prepare lost actual definition or installed activation")
	}
	bindParameters := func(name string) {
		command(func(s *core.StatementScope) error {
			_, err := s.BindProtocolPortal(name, "insteadparams", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 1, 0, 0, 0, 1, '2', 0, 0}})
			return err
		})
	}
	bindParameters("insteadparams_old")
	old := registry.Binding("insteadparams_old")
	if old == nil || old.Resource == nil || old.Resource.Closer == nil || old.Resource.Root != nil || old.Entry.Body != parameters.Body || old.Folds == nil || len(old.Folds.Entries) == 0 {
		t.Fatal("parameter mutation lost actual resource or retained an execution graph")
	}
	if parameters.Description.Folds != nil && len(parameters.Description.Folds.Current().Entries) != 0 {
		t.Fatal("parameter Bind published definition artifacts")
	}
	oldFolds := *old.Folds
	oldBody, oldResource, oldAnalysis := old.Entry.Body, old.Resource, old.Entry.Description.Analysis
	// A real zero-parameter mutation owns reusable definition folds. Parameterized
	// mutations above instead own private accepted portal backing by design.
	command(func(s *core.StatementScope) error {
		_, err := s.ParseProtocol("insteadownership", "INSERT INTO instead_owner_view VALUES(COALESCE(-(7::integer),0),2) RETURNING id,v", nil)
		return err
	})
	definition := registry.Lookup("insteadownership")
	command(func(s *core.StatementScope) error {
		_, err := s.BindProtocolPortal("insteadownership_portal", "insteadownership", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
		return err
	})
	accepted := registry.Binding("insteadownership_portal")
	if accepted == nil || accepted.Resource == nil || accepted.Resource.Root != nil || accepted.Folds == nil {
		t.Fatal("mutation companion lost actual resource")
	}
	originalFolds := *accepted.Folds
	originalDescriptionFolds := enginetest.SnapshotFoldDescription(definition.Description)
	originalBody, originalResource := accepted.Entry.Body, accepted.Resource
	sqlExecutionPreparedGenerations(t, e, owner, definition, accepted, "insteadownership", "insteadownership_portal")
	// This invokes actual FoldPublication operations over the real registry owner:
	// presentation invalidates analysis only; executable codec replacement retires
	// the generation and rejects distinct candidate backing and the old portal.
	if accepted.Resource != originalResource || accepted.Entry.Body != originalBody || accepted.Resource.Root != nil || accepted.Folds.Generation != originalFolds.Generation || &accepted.Folds.Entries[0] != &originalFolds.Entries[0] || &accepted.Folds.Dependencies[0] != &originalFolds.Dependencies[0] {
		t.Fatal("generation rejection mutated immutable portal ownership")
	}
	enginetest.SessionSQL(t, owner, "SAVEPOINT instead_definitions")
	enginetest.SessionSQL(t, owner, "CREATE OR REPLACE FUNCTION instead_owner_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM 1; RETURN NEW; END$$")
	enginetest.SessionSQL(t, owner, "CREATE OR REPLACE TRIGGER body INSTEAD OF INSERT ON instead_owner_view FOR EACH ROW EXECUTE FUNCTION instead_owner_body('new')")
	if preparedRebindValid(t, e, owner, oldAnalysis) {
		t.Fatal("changed trigger payload failed to invalidate actual analysis")
	}
	bindParameters("insteadparams_new")
	fresh := registry.Binding("insteadparams_new")
	if fresh == nil || fresh.Folds == nil || len(fresh.Folds.Entries) == 0 || &fresh.Folds.Entries[0] == &oldFolds.Entries[0] || fresh.Entry.Description.Analysis == oldAnalysis || fresh.Resource.Root != nil {
		t.Fatal("fresh parameter activation reused stale backing or retained a graph")
	}
	enginetest.SessionSQL(t, owner, "DROP TRIGGER body ON instead_owner_view")
	bindParameters("insteadparams_auto")
	automatic := registry.Binding("insteadparams_auto")
	if automatic == nil || automatic.Resource == nil || automatic.Resource.Root != nil || automatic.Entry.Description.Analysis == fresh.Entry.Description.Analysis {
		t.Fatal("automatic cutover failed to rebind actual owner")
	}
	for _, name := range []string{"insteadparams_new", "insteadparams_auto"} {
		command(func(s *core.StatementScope) error { return s.CloseProtocolPortal(name) })
	}
	enginetest.SessionSQL(t, owner, "ROLLBACK TO instead_definitions")
	bindParameters("insteadparams_rollback")
	restored := registry.Binding("insteadparams_rollback")
	if restored == nil || restored.Resource.Root != nil || restored.Folds == nil || &restored.Folds.Entries[0] == &oldFolds.Entries[0] {
		t.Fatal("rollback activation retained prior private backing")
	}
	if routine.SQL.PL != program || registry.Lookup("insteadparams") != parameters || old.Resource != oldResource || old.Entry.Body != oldBody || old.Entry.Description.Analysis != oldAnalysis || old.Folds.Generation != oldFolds.Generation || &old.Folds.Entries[0] != &oldFolds.Entries[0] || &old.Folds.Dependencies[0] != &oldFolds.Dependencies[0] {
		t.Fatal("replacement/drop/rollback mutated old accepted owner")
	}
	// Canceled publication with distinct candidate backing cannot publish or
	// resurrect this definition's retired generation.
	op, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		op.Done()
		t.Fatal(err)
	}
	environment, err := e.PreparedEnvironmentLocked(auth, true)
	if err != nil {
		e.Mu.Unlock()
		op.Done()
		t.Fatal(err)
	}
	publication, err := core.BeginFoldPublication(&definition.Description, definition.Body, environment, auth.Catalog, nil)
	if err != nil || publication == nil {
		e.Mu.Unlock()
		op.Done()
		t.Fatal("missing publication owner", err)
	}
	before := enginetest.SnapshotFoldDescription(definition.Description)
	publication.Group.Entries = append([]exec.FoldConstant(nil), originalFolds.Entries...)
	publication.Group.Dependencies = append([]exec.FoldDependency(nil), originalFolds.Dependencies...)
	publication.Group.Private, publication.Group.DependenciesPrivate = true, true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = publication.Accept(ctx, auth.Catalog, environment, e.Catalog.State.Epoch())
	after := definition.Description.Folds.Current()
	e.Mu.Unlock()
	op.Done()
	if !errors.Is(err, context.Canceled) || definition.Description.Analysis != before.Analysis || definition.Description.Folds != before.Folds || after.Generation != before.Accepted.Generation || &after.Entries[0] != &before.Accepted.Entries[0] || &after.Dependencies[0] != &before.Accepted.Dependencies[0] || registry.Lookup("insteadownership") != definition || registry.Binding("insteadownership_portal") != accepted {
		t.Fatal("canceled candidate replaced registry publication", err)
	}
	enginetest.PreparedFoldEntry(t, originalDescriptionFolds, sqltype.Int4, "-7")
	if enginetest.FoldRootsReusable(definition.Description.Folds) {
		t.Fatal("canceled publication resurrected retired root folds")
	}
	for _, name := range []string{"insteadparams_old", "insteadparams_rollback", "insteadownership_portal"} {
		command(func(s *core.StatementScope) error { return s.CloseProtocolPortal(name) })
		if registry.Binding(name) != nil {
			t.Fatal("closed resource remained registered")
		}
	}
	for _, name := range []string{"insteadparams", "insteadownership"} {
		command(func(s *core.StatementScope) error { return s.CloseProtocolStatement(name) })
		if registry.Lookup(name) != nil {
			t.Fatal("closed definition remained registered")
		}
	}
	enginetest.SessionSQL(t, owner, "ROLLBACK")
}

// Exercise semantic revisions against the actual registry definition and its
// accepted portal, without publishing a synthetic catalog into the engine.
func sqlExecutionPreparedGenerations(t *testing.T, e *core.Engine, owner *session.Session, definition *core.PreparedDefinition, accepted *core.PreparedBinding, statementName, portalName string) {
	t.Helper()
	description := &definition.Description
	before := enginetest.SnapshotFoldDescription(*description)
	enginetest.PreparedFoldEntry(t, before, sqltype.Int4, "-7")
	if accepted.Folds == nil || len(accepted.Folds.Entries) == 0 || len(accepted.Folds.Dependencies) == 0 || accepted.Entry.Description.Folds != description.Folds {
		t.Fatal("PL SQL/CALL portal lacks composed accepted fold ownership")
	}
	bindingFolds := *accepted.Folds
	if bindingFolds.Generation != before.Accepted.Generation || &bindingFolds.Entries[0] != &before.Accepted.Entries[0] {
		t.Fatal("portal did not accept the definition's immutable generation")
	}
	op, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := e.PreparedEnvironmentLocked(auth, true)
	if err != nil {
		t.Fatal(err)
	}
	integer := auth.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)})
	presentation := *integer
	presentation.ObjectRevision = integer.Revision() + 1
	state := catalog.CloneState(auth.Catalog.State())
	state.SetDescriptor(presentation.Address(), &presentation)
	presented := e.Catalog.Read(state)
	if presentation.Revision() == integer.Revision() || description.Analysis.Valid(presented, environment) || !description.Analysis.FoldsValid(presented, environment) {
		t.Fatal("presentation revision did not distinguish analysis from executable validity")
	}
	publication, err := core.BeginFoldPublication(description, definition.Body, environment, presented, nil)
	if err != nil || publication == nil {
		t.Fatal("presentation publication", err)
	}
	if err := publication.Accept(op.Ctx, presented, environment, e.Catalog.State.Epoch()); err != nil {
		t.Fatal("unchanged codec rejected", err)
	}
	if description.Folds.Current().Generation != before.Accepted.Generation || &description.Folds.Current().Entries[0] != &before.Accepted.Entries[0] || !enginetest.FoldRootsReusable(description.Folds) {
		t.Fatal("presentation-only revision replaced executable backing")
	}
	// The same address/revision with a distinct executable codec must retire
	// this exact definition and reject this exact portal's fixed fold input.
	codec := *integer.SQLType
	presentation.SQLType = &codec
	state = catalog.CloneState(auth.Catalog.State())
	state.SetDescriptor(presentation.Address(), &presentation)
	replaced := e.Catalog.Read(state)
	if description.Analysis.FoldsValid(replaced, environment) || core.FoldDependenciesValid(replaced, environment, bindingFolds.Dependencies) {
		t.Fatal("effective codec replacement left the PL accepted artifact valid")
	}
	if portal, err := core.BeginFoldPublication(&accepted.Entry.Description, accepted.Entry.Body, environment, replaced, accepted); !errors.Is(err, ids.ErrCatalogReplaced) || portal != nil {
		t.Fatal("codec replacement admitted old portal", err)
	}
	publication, err = core.BeginFoldPublication(description, definition.Body, environment, replaced, nil)
	if err != nil || publication == nil {
		t.Fatal("codec invalidation publication", err)
	}
	retired := description.Folds.Current()
	if retired.Generation <= before.Accepted.Generation || enginetest.FoldRootsReusable(description.Folds) || len(publication.Group.Entries) != 0 {
		t.Fatal("codec replacement did not retire demanded root folds")
	}
	// Give the rejected attempt distinct candidate backing. A publisher that
	// incorrectly installs it cannot pass an equality-only value assertion.
	publication.Group.Entries = append([]exec.FoldConstant(nil), before.Accepted.Entries...)
	publication.Group.Dependencies = append([]exec.FoldDependency(nil), before.Accepted.Dependencies...)
	publication.Group.Private, publication.Group.DependenciesPrivate = true, true
	if err := publication.Accept(op.Ctx, replaced, environment, e.Catalog.State.Epoch()); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("stale codec publication was not rejected", err)
	}
	current := description.Folds.Current()
	if registry := core.PayloadOf(owner).PreparedSQL; registry.Lookup(statementName) != definition || registry.Binding(portalName) != accepted {
		t.Fatal("rejection replaced registry ownership")
	}
	if description.Analysis != before.Analysis || description.Folds != before.Folds || description.Folds.Analysis != before.Analysis || current.Generation != retired.Generation || &current.Entries[0] != &before.Accepted.Entries[0] || &current.Dependencies[0] != &before.Accepted.Dependencies[0] || enginetest.FoldRootsReusable(description.Folds) {
		t.Fatal("rejected publication changed its actual owner or resurrected backing")
	}
	if accepted.Folds.Generation != bindingFolds.Generation || &accepted.Folds.Entries[0] != &bindingFolds.Entries[0] || &accepted.Folds.Dependencies[0] != &bindingFolds.Dependencies[0] {
		t.Fatal("invalidation or rejection mutated prior accepted portal backing")
	}
	enginetest.PreparedFoldEntry(t, before, sqltype.Int4, "-7")
}

func preparedRebindValid(t *testing.T, e *core.Engine, s *session.Session, cell *core.Analysis) bool {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := e.PreparedEnvironmentLocked(auth, true)
	if err != nil {
		t.Fatal(err)
	}
	return cell.Valid(auth.Catalog, environment)
}
