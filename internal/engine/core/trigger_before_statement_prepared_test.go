package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestTriggerBeforeStatementPreparedOwnership(t *testing.T) {
	t.Run("retained_mutation", beforeStatementPreparedOwner)
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rejected_publication", true: "accepted_publication"}[commit], func(t *testing.T) {
			e, s, read, relation, d := triggerUnitFixture(t, "BEGIN CREATE OR REPLACE FUNCTION trigger_unit_replaced() RETURNS integer LANGUAGE plpgsql AS $body$BEGIN RETURN 8; END$body$; RETURN NULL; END")
			integer, _ := read.LookupType(sqltype.Int4)
			original := triggerUnitProcedure(t, e, s, "trigger_unit_replaced", 9500004, integer, proc.OrdinaryInvocation, "BEGIN RETURN 7; END")
			p := triggerUnitDefinition(t, read, relation, d, "replace", stored.TriggerInsert).Parts()
			p.Level = stored.TriggerStatement
			definition, err := stored.NewTrigger(p)
			if err != nil {
				t.Fatal(err)
			}
			relation = triggerUnitInstall(t, e, relation, definition)
			triggerUnitAdmit(t, s, relation)
			ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
			if err := s.WriteHost().BeforeStatement(ref, stored.TriggerInsert, nil); err != nil {
				t.Fatal(err)
			}
			if s.Catalog == nil || e.CatalogRead().Get(original.Address()) != original {
				t.Fatal("body bypassed private definition journal")
			}
			e.Mu.Lock()
			private := e.PrivateCatalogLocked(s.PendingCatalog())
			replacement, _ := private.Get(original.Address()).(*catalog.Procedure)
			e.Mu.Unlock()
			if replacement == nil || replacement == original || replacement.SQL == original.SQL || replacement.Revision() <= original.Revision() {
				t.Fatal("private body did not install distinct executable backing")
			}
			var primary error
			if !commit {
				primary = errors.New("reject outer command")
			}
			if err := s.Finish(primary); !errors.Is(err, primary) {
				t.Fatal(err)
			}
			installed := e.CatalogRead().Get(original.Address())
			if commit && installed != replacement || !commit && installed != original {
				t.Fatal("definition publication escaped outer Finish decision")
			}
		})
	}
}

func beforeStatementPreparedOwner(t *testing.T) {
	e := engine.New()
	owner := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE TABLE bst_owner(id integer,v integer)",
		"CREATE FUNCTION bst_owner_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$",
		"CREATE TRIGGER s BEFORE INSERT ON bst_owner EXECUTE FUNCTION bst_owner_body()",
		"BEGIN",
	} {
		enginetest.SessionSQL(t, owner, q)
	}
	cycle := owner.NewProtocolCycle()
	registry := core.PayloadOf(owner).PreparedSQL
	command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, owner, cycle, fn) }
	command(func(s *core.StatementScope) error {
		_, err := s.ParseProtocol("bst_definition", "INSERT INTO bst_owner VALUES(COALESCE(-(7::integer),0),2)", nil)
		return err
	})
	definition := registry.Lookup("bst_definition")
	if definition == nil || definition.Description.Analysis == nil {
		t.Fatal("missing actual mutation definition owner")
	}
	bind := func(name string) {
		command(func(s *core.StatementScope) error {
			if core.ScopeBeforeTestMap(s) != nil || len(s.BeforeCaptures) != 0 {
				t.Fatal("Bind inherited a firing activation")
			}
			_, err := s.BindProtocolPortal(name, "bst_definition", &protocolUnitInput{data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
	}
	bind("bst_first")
	first := registry.Binding("bst_first")
	if first == nil || first.Resource == nil || first.Resource.Root != nil || first.Folds == nil || len(first.Folds.Entries) == 0 || len(first.Folds.Dependencies) == 0 {
		t.Fatal("mutation lost accepted immutable folds or retained graph")
	}
	accepted := *first.Folds
	analysis := first.Entry.Description.Analysis
	body, resource := first.Entry.Body, first.Resource
	var prior map[core.BeforeStatementTestKey]struct{}
	execute := func(name string) {
		command(func(s *core.StatementScope) error {
			if core.ScopeBeforeTestMap(s) != nil {
				t.Fatal("new Execute retained mutable fired state")
			}
			_, err := s.ExecuteProtocolPortal(name, 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
			if err != nil {
				return err
			}
			fired := core.ScopeBeforeTestMap(s)
			if len(fired) != 1 {
				t.Fatal("real mutation did not allocate one activation")
			}
			if prior != nil {
				for key := range fired {
					delete(fired, key)
				}
				if len(prior) != 1 {
					t.Fatal("Execute activations share fired backing")
				}
			}
			prior = fired
			return nil
		})
	}
	execute("bst_first")
	bind("bst_second")
	execute("bst_second")
	if first.Resource != resource || first.Entry.Body != body || first.Entry.Description.Analysis != analysis || first.Folds.Generation != accepted.Generation || &first.Folds.Entries[0] != &accepted.Entries[0] || &first.Folds.Dependencies[0] != &accepted.Dependencies[0] {
		t.Fatal("Execute mutated retained fold owner or generation")
	}
	command(func(s *core.StatementScope) error { return s.CloseProtocolPortal("bst_second") })
	enginetest.SessionSQL(t, owner, "SAVEPOINT definitions")
	enginetest.SessionSQL(t, owner, "CREATE OR REPLACE FUNCTION bst_owner_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM 1; RETURN NEW; END$$")
	enginetest.SessionSQL(t, owner, "CREATE OR REPLACE TRIGGER s BEFORE INSERT ON bst_owner EXECUTE FUNCTION bst_owner_body('new')")
	if preparedRebindValid(t, e, owner, analysis) {
		t.Fatal("trigger replacement did not invalidate retained analysis")
	}
	bind("bst_replaced")
	replacement := registry.Binding("bst_replaced")
	if replacement == nil || replacement.Resource.Root != nil || replacement.Entry.Description.Analysis == analysis {
		t.Fatal("replacement reused old accepted mutation")
	}
	command(func(s *core.StatementScope) error { return s.CloseProtocolPortal("bst_replaced") })
	enginetest.SessionSQL(t, owner, "ROLLBACK TO definitions")
	// Exercise rejected FoldPublication over the real definition, with distinct
	// candidate backing. A canceled acceptance must publish no new generation.
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
	before := snapshotFoldDescription(definition.Description)
	publication.Group.Entries = append([]exec.FoldConstant(nil), accepted.Entries...)
	publication.Group.Dependencies = append([]exec.FoldDependency(nil), accepted.Dependencies...)
	publication.Group.Private, publication.Group.DependenciesPrivate = true, true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = publication.Accept(ctx, auth.Catalog, environment, e.Catalog.State.Epoch())
	after := definition.Description.Folds.Current()
	e.Mu.Unlock()
	op.Done()
	if !errors.Is(err, context.Canceled) || definition.Description.Analysis != before.Analysis || after.Generation != before.Accepted.Generation || &after.Entries[0] != &before.Accepted.Entries[0] || &after.Dependencies[0] != &before.Accepted.Dependencies[0] || registry.Lookup("bst_definition") != definition {
		t.Fatal("rejected publication changed real definition backing", err)
	}
	if first.Entry.Body != body || first.Entry.Description.Analysis != analysis || first.Folds.Generation != accepted.Generation || &first.Folds.Entries[0] != &accepted.Entries[0] {
		t.Fatal("replacement or rejection mutated old portal ownership")
	}
	command(func(s *core.StatementScope) error { return s.CloseProtocolPortal("bst_first") })
	command(func(s *core.StatementScope) error { return s.CloseProtocolStatement("bst_definition") })
	enginetest.SessionSQL(t, owner, "ROLLBACK")
}
