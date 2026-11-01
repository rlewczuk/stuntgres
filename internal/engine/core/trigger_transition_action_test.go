package core_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

type transitionActionHost struct {
	dml.FinishHost
	t             *testing.T
	scope         *core.StatementScope
	integer       *sqltype.Descriptor
	steps         []string
	caller, owner authorization.PrincipalRef
}

func (h *transitionActionHost) depth(want int64) {
	h.t.Helper()
	value, err := h.FinishHost.TriggerDepthValue(h.integer)
	got, numberErr := value.Integer()
	if err != nil || numberErr != nil || got != want {
		h.t.Fatal("unpaired RI trigger depth", got, want, err, numberErr)
	}
}

// Identity is lexical command presentation scratch. Actual invocation authority
// is selected afresh from the operation and the currently borrowed RI actor.
func (h *transitionActionHost) actor(want authorization.PrincipalRef) {
	h.t.Helper()
	e := h.scope.Engine()
	e.Mu.Lock()
	auth, err := h.scope.RoutineAuthorizationLocked()
	e.Mu.Unlock()
	if err != nil || auth.State.CurrentUser != want || h.scope.Op.EffectiveUser != nil || h.scope.Op.Restricted != nil {
		h.t.Fatal("live invocation authority was not restored", auth.State.CurrentUser, want, err)
	}
}

func (h *transitionActionHost) CompleteAfterStatement(ref ids.ObjectRef, event stored.TriggerEvent) error {
	h.depth(1)
	h.actor(h.owner)
	if h.scope.ForeignActionAuth == nil {
		h.t.Fatal("qualification lost borrowed child authority")
	}
	h.steps = append(h.steps, "qualify")
	if err := h.FinishHost.CompleteAfterStatement(ref, event); err != nil {
		return err
	}
	for _, frame := range core.ScopeAfterTestFrames(h.scope) {
		if frame.Qualified != 1 || frame.RowCursor != 0 || frame.Segments[0].Frozen {
			h.t.Fatal("RI completion exposed bodies before paired unwind", frame)
		}
	}
	return nil
}

func (h *transitionActionHost) SwapForeignActionAuthority(auth dml.Authority) dml.Authority {
	previous := h.FinishHost.SwapForeignActionAuthority(auth)
	if auth == nil {
		h.depth(1)
		h.actor(h.caller)
		h.steps = append(h.steps, "restore-actor")
	} else {
		h.steps = append(h.steps, "borrow-actor")
	}
	return previous
}

func (h *transitionActionHost) LeaveForeignKeyTrigger() {
	h.FinishHost.LeaveForeignKeyTrigger()
	h.depth(0)
	h.actor(h.caller)
	h.steps = append(h.steps, "leave-depth")
}

func (h *transitionActionHost) ExposeAfterStatement(ref ids.ObjectRef, event stored.TriggerEvent) error {
	h.depth(0)
	h.actor(h.caller)
	if h.scope.ForeignActionAuth != nil {
		h.t.Fatal("exposure borrowed internal RI actor")
	}
	h.steps = append(h.steps, "expose")
	if err := h.FinishHost.ExposeAfterStatement(ref, event); err != nil {
		return err
	}
	h.depth(0)
	h.actor(h.caller)
	for _, frame := range core.ScopeAfterTestFrames(h.scope) {
		if !frame.Segments[0].Frozen || frame.Skipped != 1 {
			h.t.Fatal("actual action failed to expose and consume transition bodies", frame)
		}
	}
	return nil
}

func TestTriggerTransitionRIExposureBoundary(t *testing.T) {
	e := engine.New()
	session := enginetest.StartSession(t, e, nil)
	for _, query := range []string{
		"CREATE ROLE tt_owner", "CREATE ROLE tt_actor", "GRANT USAGE ON SCHEMA public TO tt_owner,tt_actor", "GRANT CREATE ON SCHEMA public TO tt_owner",
		"CREATE TABLE p(id integer PRIMARY KEY)", "GRANT REFERENCES ON p TO tt_owner", "SET ROLE tt_owner",
		"CREATE TABLE c(id integer,pid integer REFERENCES p ON DELETE CASCADE)", "RESET ROLE",
		"CREATE FUNCTION tt_qualify() RETURNS boolean LANGUAGE plpgsql AS $$BEGIN IF pg_trigger_depth()<>1 OR current_user::text<>'tt_owner' THEN PERFORM 1/0; END IF; RETURN true; END$$",
		"CREATE FUNCTION tt_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF pg_trigger_depth()<>1 OR current_user::text<>'tt_owner' THEN PERFORM 1/0; END IF; PERFORM count(*) FROM x; RETURN NULL; END$$",
		"CREATE TRIGGER r AFTER DELETE ON c REFERENCING OLD TABLE x FOR EACH ROW EXECUTE FUNCTION tt_body()",
		"CREATE TRIGGER s AFTER DELETE ON c REFERENCING OLD TABLE x FOR EACH STATEMENT WHEN(tt_qualify()) EXECUTE FUNCTION tt_body()",
		"INSERT INTO p VALUES(1)", "INSERT INTO c VALUES(11,1),(12,1)", "GRANT DELETE ON p TO tt_actor", "SET ROLE tt_actor",
	} {
		enginetest.SessionSQL(t, session, query)
	}
	scope := foreignActionUnitScope(t, e, session, "DELETE FROM p")
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	host := &transitionActionHost{
		FinishHost: scope.WriteHost(), t: t, scope: scope, integer: integer,
		caller: e.Roles().LookupRef("tt_actor"), owner: e.Roles().LookupRef("tt_owner"),
	}
	host.actor(host.caller)
	if err := dml.SettleForeignActions(scope.ActivateRuntime(scope.Op.Context()), host); err != nil {
		t.Fatal(err)
	}
	want := []string{"borrow-actor", "qualify", "restore-actor", "leave-depth", "expose"}
	if !reflect.DeepEqual(host.steps, want) {
		t.Fatal("actual RI action did not qualify/unwind/expose in order", host.steps)
	}
	host.actor(host.caller)
	host.depth(0)
	if scope.ForeignActionAuth != nil {
		t.Fatal("exposure retained borrowed RI authority")
	}
	before := core.ScopeAfterTestFrames(scope)
	if err := core.ScopeDrainAfterTest(scope); err != nil {
		t.Fatal(err)
	}
	for id, frame := range core.ScopeAfterTestFrames(scope) {
		if frame.Skipped != before[id].Skipped || frame.RowCursor != frame.Rows || frame.StatementCursor != frame.Statements {
			t.Fatal("final drain lost or replayed an exposed event", frame)
		}
	}
	host.actor(host.caller)
	host.depth(0)
}
