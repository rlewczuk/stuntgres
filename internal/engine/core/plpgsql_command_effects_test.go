package core_test

import (
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPLExceptionCommandCompletion(t *testing.T) {
	t.Run("unrelated_dml_keeps_bookkeeping_unallocated", func(t *testing.T) {
		for _, query := range []string{
			"INSERT INTO unrelated VALUES(2),(3),(4)",
			"UPDATE unrelated SET v=2",
			"DELETE FROM unrelated",
		} {
			t.Run(query, func(t *testing.T) {
				e := engine.New()
				sess := sessionStart(t, e, nil)
				sessionSQL(t, sess, "CREATE TABLE unrelated(v integer)")
				sessionSQL(t, sess, "INSERT INTO unrelated VALUES(1)")
				sessionSQL(t, sess, "CREATE TABLE parent(id integer PRIMARY KEY)")
				sessionSQL(t, sess, "CREATE TABLE child(id integer REFERENCES parent(id))")
				scope := foreignActionUnitScope(t, e, sess, query)
				if core.ScopeForeignActionsSlot(scope) != nil {
					t.Fatal("unconstrained DML allocated exception/FK effect bookkeeping")
				}
				if err := core.ScopeCompleteCommandEffects(scope, false, false); err != nil {
					t.Fatal(err)
				}
				if core.ScopeForeignActionsSlot(scope) != nil {
					t.Fatal("completion allocated bookkeeping for unrelated DML")
				}
			})
		}
	})
	t.Run("relevant_accepted_events_keep_command_and_versions", func(t *testing.T) {
		e := engine.New()
		sess := sessionStart(t, e, nil)
		sessionSQL(t, sess, "CREATE TABLE relevant(id integer PRIMARY KEY,parent integer REFERENCES relevant(id))")
		scope := foreignActionUnitScope(t, e, sess, "INSERT INTO relevant VALUES(1,NULL),(2,1)")
		ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "relevant")
		events := core.ScopeForeignActionsOrZero(scope).Transitions
		rows := scope.Stage.Rows[ref.Address]
		if len(events) != 2 || len(rows) != 2 {
			t.Fatal("relevant accepted insert events missing", events, rows)
		}
		for i, event := range events {
			if event.Relation != ref.Address || event.Command != core.ScopeEffectCommand(scope) || event.Old.Version != 0 || event.Next.Version != rows[i].Version || event.Next.Row != rows[i].Row {
				t.Fatal("accepted event lost exact version/command ownership", event, rows[i])
			}
		}
	})
	t.Run("incoming_not_valid_fk_using_unique_index_remains_relevant", func(t *testing.T) {
		e := engine.New()
		sess := sessionStart(t, e, nil)
		for _, query := range []string{
			"CREATE TABLE parent(id integer)",
			"CREATE UNIQUE INDEX parent_key ON parent(id)",
			"CREATE TABLE child(id integer)",
			"ALTER TABLE child ADD CONSTRAINT child_fk FOREIGN KEY(id) REFERENCES parent(id) NOT VALID",
			"INSERT INTO parent VALUES(1)",
			"INSERT INTO child VALUES(1)",
		} {
			sessionSQL(t, sess, query)
		}
		scope := foreignActionUnitScope(t, e, sess, "DELETE FROM parent")
		events := core.ScopeForeignActionsOrZero(scope).Transitions
		if len(events) != 1 || events[0].Old.Version == 0 || events[0].Next.Version != 0 {
			t.Fatal("incoming FK deletion lost its exact predecessor event", events)
		}
		var diagnostic *sqlstate.Error
		if err := core.ScopeCompleteCommandEffects(scope, false, false); !errors.As(err, &diagnostic) || diagnostic.Code != "23503" {
			t.Fatal("NOT VALID incoming FK was ignored at completion", err)
		}
	})
	t.Run("exclusion_only_keeps_bounded_command_ownership", func(t *testing.T) {
		e := engine.New()
		sess := sessionStart(t, e, nil)
		sessionSQL(t, sess, "CREATE TABLE guarded(v integer,CONSTRAINT guarded_ex EXCLUDE USING btree(v WITH =) DEFERRABLE INITIALLY DEFERRED)")
		sessionSQL(t, sess, "INSERT INTO guarded VALUES(1)")
		scope := foreignActionUnitScope(t, e, sess, "INSERT INTO guarded VALUES(1)")
		events := core.ScopeForeignActionsOrZero(scope).Transitions
		if len(events) != 1 || events[0].Command != core.ScopeEffectCommand(scope) {
			t.Fatal("exclusion-only accepted row lost command ownership", events)
		}
		var diagnostic *sqlstate.Error
		if err := core.ScopeCheckConstraintCommandEvents(scope, true, nil, true); !errors.As(err, &diagnostic) || diagnostic.Code != "23P01" {
			t.Fatal("bounded exclusion check skipped its accepted row", err)
		}
	})
	t.Run("exclusion_events_keep_unowned_obligations", func(t *testing.T) {
		e, s := plExceptionUnitScope(t)
		view := e.CatalogRead()
		a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90003}
		stale := view.Domain().Ref(view.Epoch()+1, a)
		current := view.Domain().Ref(view.Epoch(), a)
		core.SetScopeEffectCommand(s, 2)
		core.SetScopeIndexesSlot(s, &index.Scope{ConstraintsStarted: true, ConstraintModes: index.ConstraintModes{Events: []index.ConstraintEvent{
			{Parent: stale, Row: 1, Version: 1},
			{Parent: current, Row: 2, Version: 2},
		}}})
		core.SetScopeForeignActionsSlot(s, &dml.ForeignActionScope{Transitions: []dml.ForeignActionTransition{{Relation: a, Next: storage.RowVersion{Row: 2, Version: 2}, Command: 2}}})
		// Missing current descriptors retire an owned event; an unowned stale
		// event must not even be probed while its outer producer is suspended.
		if err := core.ScopeCheckConstraintCommandEvents(s, false, nil, true); err != nil {
			t.Fatal(err)
		}
		if len(core.ScopeIndexesSlot(s).ConstraintModes.Events) != 1 || core.ScopeIndexesSlot(s).ConstraintModes.Events[0].Row != 1 {
			t.Fatal("unowned exclusion event consumed")
		}
		if err := core.ScopeCheckConstraintEvents(s, true, nil); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("root lost its original exclusion obligation", err)
		}
	})
	t.Run("same_row_distinct_command_versions", func(t *testing.T) {
		_, s := plExceptionUnitScope(t)
		a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90002}
		root := storage.RowVersion{Row: 1, Version: 1}
		child := storage.RowVersion{Row: 1, Version: 2}
		s.Stage.Rows = map[ids.ObjectAddress][]storage.RowVersion{a: {child}}
		core.SetScopeForeignActionsSlot(s, &dml.ForeignActionScope{Transitions: []dml.ForeignActionTransition{{Relation: a, Next: root, Command: 0}, {Relation: a, Old: root, Next: child, Command: 2}}})
		core.SetScopeEffectCommand(s, 2)
		if err := core.ScopeCompleteCommandEffects(s, false, true); err != nil {
			t.Fatal(err)
		}
		events := core.ScopeForeignActionsSlot(s).Transitions
		if events[0].Checked || events[0].ActionsDone || !events[1].Checked || !events[1].ActionsDone || s.Stage.Rows[a][0].Version != 2 {
			t.Fatal("command ownership was a global suffix/RowID filter", events)
		}
		core.SetScopeEffectCommand(s, 0)
		if err := core.ScopeCompleteCommandEffects(s, false, false); err != nil {
			t.Fatal(err)
		}
		if !core.ScopeForeignActionsSlot(s).Transitions[0].Checked {
			t.Fatal("root obligation lost")
		}
	})
	t.Run("failed_acceptance_has_no_event", func(t *testing.T) {
		_, s := plExceptionUnitScope(t)
		accepted, _, err := s.WriteHost().AppendAcceptedInsert(ids.ObjectRef{}, index.WriteCandidate{})
		if accepted || err == nil || len(core.ScopeForeignActionsOrZero(s).Transitions) != 0 {
			t.Fatal("failed stage admission queued an effect", accepted, err)
		}
	})
	for _, sameRelation := range []bool{false, true} {
		name := "pending_outer_different_relation"
		if sameRelation {
			name = "pending_outer_same_relation"
		}
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			sess := sessionStart(t, e, nil)
			sessionSQL(t, sess, "CREATE TABLE node(id INT PRIMARY KEY,parent INT REFERENCES node(id))")
			sessionSQL(t, sess, "CREATE TABLE auxiliary(id INT)")
			scope := foreignActionUnitScope(t, e, sess, "INSERT INTO node VALUES(1,2)")
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "node")
			pending := scope.Stage.Rows[ref.Address][0]
			integer := pending.Values[0].Type()
			three, err := sqltype.Integer(integer, 3)
			if err != nil {
				t.Fatal(err)
			}
			relation := ref.Address
			values := []sqltype.Value{three, sqltype.Null(integer)}
			if !sameRelation {
				relation = enginetest.CatalogRef(t, e, e.CatalogRead(), "auxiliary").Address
				values = []sqltype.Value{three}
			}
			child := storage.RowVersion{Row: 90002, Version: 90002, Values: values}
			scope.Stage.Rows[relation] = append(scope.Stage.Rows[relation], child)
			core.ScopeForeignActions(scope).Transitions = append(core.ScopeForeignActionsOrZero(scope).Transitions, dml.ForeignActionTransition{Relation: relation, Next: child, Command: 2})
			core.SetScopeEffectCommand(scope, 2)
			if err := core.ScopeCompleteCommandEffects(scope, false, true); err != nil {
				t.Fatal("child checked incomplete outer producer", err)
			}
			if core.ScopeForeignActionsSlot(scope).Transitions[0].Checked || len(scope.WriteHost().PrivateRows(ref.Address).Staged) < 1 {
				t.Fatal("child hid/consumed outer visibility or obligation")
			}
			core.SetScopeEffectCommand(scope, 0)
			err = core.ScopeCompleteCommandEffects(scope, false, false)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "23503" {
				t.Fatal("root lost its pending FK obligation", err)
			}
		})
	}
	t.Run("causal_fixed_point_once_and_authority_restore", func(t *testing.T) {
		e := engine.New()
		sess := sessionStart(t, e, nil)
		for _, q := range []string{
			"CREATE TABLE p(id INT PRIMARY KEY)",
			"CREATE TABLE a(id INT PRIMARY KEY REFERENCES p ON UPDATE CASCADE)",
			"CREATE TABLE b(id INT PRIMARY KEY REFERENCES p ON UPDATE CASCADE)",
			"CREATE TABLE g(id INT,aid INT REFERENCES a ON UPDATE CASCADE,bid INT REFERENCES b ON UPDATE CASCADE)",
			"INSERT INTO p VALUES(1)", "INSERT INTO a VALUES(1)", "INSERT INTO b VALUES(1)", "INSERT INTO g VALUES(99,1,1)",
		} {
			sessionSQL(t, sess, q)
		}
		scope := foreignActionUnitScope(t, e, sess, "UPDATE p SET id=2")
		auth, err := scope.WriteHost().ForeignKeyAuthority()
		if err != nil {
			t.Fatal(err)
		}
		previous := scope.WriteHost().SwapForeignActionAuthority(auth)
		defer scope.WriteHost().SwapForeignActionAuthority(previous)
		core.ScopeForeignActionsSlot(scope).Settling = true
		if err := core.ScopeCompleteCommandEffects(scope, false, false); err != nil {
			t.Fatal(err)
		}
		if !core.ScopeForeignActionsSlot(scope).Settling || scope.ForeignActionAuth != core.ForeignAuthorizationOf(auth) {
			t.Fatal("reentrant settlement lost authority/Settling")
		}
		core.ScopeForeignActionsSlot(scope).Settling = false
		ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "g")
		var diamond []dml.ForeignActionTransition
		for _, event := range core.ScopeForeignActionsSlot(scope).Transitions {
			if !event.ActionsDone || !event.Checked {
				t.Fatal("causal queue not exhausted", event)
			}
			if event.Relation == ref.Address {
				diamond = append(diamond, event)
			}
		}
		if len(diamond) != 2 || diamond[0].Next.Version != diamond[1].Old.Version || diamond[0].Old.Row != diamond[1].Old.Row {
			t.Fatal("same-row causal transitions collapsed", diamond)
		}
		count, frontier := len(core.ScopeForeignActionsSlot(scope).Transitions), e.Storage.VersionFrontier()
		if err := core.ScopeCompleteCommandEffects(scope, false, false); err != nil {
			t.Fatal(err)
		}
		if len(core.ScopeForeignActionsSlot(scope).Transitions) != count || e.Storage.VersionFrontier() != frontier {
			t.Fatal("root replayed completed cascade")
		}
		mark, err := scope.BeginPLException()
		if err != nil {
			t.Fatal(err)
		}
		core.ScopeForeignActionsSlot(scope).Transitions[0].Checked = false
		core.ScopeForeignActionsSlot(scope).Transitions[0].ActionsDone = false
		if err := scope.EndPLException(mark, true, errors.New("body")); err != nil {
			t.Fatal(err)
		}
		if !core.ScopeForeignActionsSlot(scope).Transitions[0].Checked || !core.ScopeForeignActionsSlot(scope).Transitions[0].ActionsDone {
			t.Fatal("exception rollback did not restore command progress")
		}
	})
}
