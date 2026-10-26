package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

func eventUnitProcedure(t *testing.T, e *core.Engine, name string) *catalog.Procedure {
	t.Helper()
	read := e.CatalogRead()
	address := read.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}, Name: name})
	d, ok := read.Get(address).(*catalog.Procedure)
	if !ok {
		t.Fatal("missing unit procedure", name)
	}
	return d
}

func TestEventTriggerJournalBoundary(t *testing.T) {
	for _, veto := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "finish_veto"}[veto], func(t *testing.T) {
			e := engine.New()
			actor := enginetest.StartSession(t, e, nil)
			for _, q := range []string{
				"CREATE TABLE et_boundary_audit(n integer)",
				"CREATE FUNCTION et_boundary_prepare() RETURNS integer LANGUAGE SQL AS $$CREATE TABLE et_boundary_source(id integer); SELECT 1$$",
				"CREATE FUNCTION et_boundary_record() RETURNS integer LANGUAGE SQL AS $$INSERT INTO et_boundary_audit VALUES(1); CREATE ROLE et_boundary_transient; SELECT 1$$",
				"CREATE FUNCTION et_boundary_start() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT et_boundary_prepare() INTO n; END$$",
				"CREATE FUNCTION et_boundary_end() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT et_boundary_record() INTO n; SELECT count(*) INTO n FROM pg_roles WHERE rolname='et_boundary_transient'; IF n<>1 THEN RAISE EXCEPTION 'callback role is not private-visible'; END IF; END$$",
				"CREATE EVENT TRIGGER et_boundary_start ON ddl_command_start WHEN TAG IN ('CREATE VIEW') EXECUTE FUNCTION et_boundary_start()",
				"CREATE EVENT TRIGGER et_boundary_end ON ddl_command_end WHEN TAG IN ('CREATE VIEW') EXECUTE FUNCTION et_boundary_end()",
				"CREATE EVENT TRIGGER et_boundary_guard ON ddl_command_end WHEN TAG IN ('ALTER TABLE') EXECUTE FUNCTION et_boundary_end()",
			} {
				enginetest.SessionSQL(t, actor, q)
			}
			before := e.Catalog.State
			rolesBefore := e.RoleStore.Published
			audit := enginetest.RoleTableRef(t, e, "et_boundary_audit")
			store := e.Storage.Relations[audit.Address]
			generation, frontier := store.Generation, e.Visibility.CommitSeq()
			op, err := actor.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(errors.New("unfinished event boundary"))
			// The source does not exist until the start callback. Successful VIEW
			// resolution therefore proves the boundary precedes object lookup.
			if _, err := scope.Execute(enginetest.Statement(t, "CREATE VIEW et_boundary_view AS SELECT id FROM et_boundary_source"), 0, nil); err != nil {
				t.Fatal(err)
			}
			if scope.Catalog == nil || scope.Completed || scope.EventCommand != nil || e.Catalog.State != before || store.Generation != generation || e.Visibility.CommitSeq() != frontier || len(store.Versions) != 0 {
				t.Fatal("eligible utility escaped genuine private Finish ownership")
			}
			if e.RoleStore.Published != rolesBefore || scope.Catalog.Roles == nil || scope.Catalog.Roles.Root.Read(actor.Authorization.State.CurrentUser).LookupRef("et_boundary_transient").ID() == 0 {
				t.Fatal("callback shared-role effects escaped or were hidden from private authority")
			}
			private := e.PrivateCatalogLocked(scope.Catalog)
			for _, name := range []string{"et_boundary_source", "et_boundary_view"} {
				key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: before.MustRelation(audit.Address).Namespace(), Name: name}
				if private.NameAddress(key) == (ids.ObjectAddress{}) || before.NameAddress(key) != (ids.ObjectAddress{}) {
					t.Fatal("missing private or prematurely installed definition", name)
				}
			}
			if len(scope.Stage.Rows[audit.Address]) != 1 {
				t.Fatal("callback row did not share root write stage")
			}
			previous, err := scope.BeginEventCommand("ALTER TABLE")
			if err != nil {
				t.Fatal(err)
			}
			mark, err := scope.BeginPLException()
			if err != nil {
				t.Fatal(err)
			}
			scope.RecordEventDrop(audit.Address)
			if err := scope.EndPLException(mark, true, errors.New("caught inner failure")); err != nil {
				t.Fatal(err)
			}
			if scope.EventCommand == nil || scope.EventCommand.Tag != "ALTER TABLE" || scope.EventCommand.Dropped {
				t.Fatal("protected rollback leaked lexical deletion facts")
			}
			caught := errors.New("skip synthetic phase completion")
			if err := scope.FinishEventCommand(previous, caught); err != caught || scope.EventCommand != nil {
				t.Fatal("nested command did not restore exact enclosing event context", err)
			}
			failure := error(nil)
			if veto {
				failure = errors.New("terminal veto")
			}
			if err := scope.Finish(failure); err != failure {
				t.Fatal("Finish lost primary failure", err)
			}
			if core.ActiveScopeOfLease(op.Lease) != nil || core.OperationCatalog(op) != op.Session.Transactions.Catalog {
				t.Fatal("finished lease remained live catalog authority")
			}
			e.Mu.Lock()
			auth, authErr := e.AuthorizationLocked(op)
			e.Mu.Unlock()
			if authErr != nil || auth.Statement != nil || auth.Catalog.State() != e.Catalog.State {
				t.Fatal("post-Finish authority reused the detached statement journal", authErr)
			}
			if veto {
				if e.Catalog.State != before || e.RoleStore.Published != rolesBefore || store.Generation != generation || len(store.Versions) != 0 || e.Visibility.CommitSeq() != frontier {
					t.Fatal("Finish veto published catalog/roles/storage/rows/frontier")
				}
			} else if e.Catalog.State == before || len(store.Versions) != 1 || e.Visibility.CommitSeq() <= frontier || e.RoleStore.Published.Root.Read(actor.Authorization.State.CurrentUser).LookupRef("et_boundary_transient").ID() == 0 {
				t.Fatal("Finish failed to publish one atomic command")
			}
		})
	}
}

func TestEventTriggerProjectionRoots(t *testing.T) {
	t.Run("empty_roots", func(t *testing.T) {
		var scope core.StatementScope
		address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 41002}
		scope.StageProjectedColumnRoots(address, nil, nil)
		if len(scope.Stage.Rows) != 0 || len(scope.Stage.Deletes) != 0 {
			t.Fatal("empty projection manufactured a row publication")
		}
	})
	for _, drop := range []bool{false, true} {
		t.Run(map[bool]string{false: "shape", true: "drop"}[drop], func(t *testing.T) {
			target := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 41000}
			audit := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 41001}
			scope := core.StatementScope{
				OwnWrites:  map[ids.ObjectAddress][]storage.RowVersion{target: {{Row: 1, Version: 1}}},
				OwnDeletes: map[ids.ObjectAddress]*visibility.PrivateDeletes{target: {Rows: map[ids.RowID]visibility.DeleteCandidate{1: {Version: 1}}}},
			}
			scope.Stage.Rows = map[ids.ObjectAddress][]storage.RowVersion{target: {{Row: 2, Version: 2}}, audit: {{Row: 3, Version: 3}}}
			scope.Stage.Deletes = map[ids.ObjectAddress]*visibility.PrivateDeletes{target: {Rows: map[ids.RowID]visibility.DeleteCandidate{2: {Version: 2}}}}
			if drop {
				scope.DiscardRemovedWrites(&core.CatalogStage{})
			}
			writes, deletes := scope.ColumnProjectionRoots(target)
			if len(writes) != 2 || len(deletes.Rows) != 2 {
				t.Fatal("projection omitted the transaction prefix or current callback suffix")
			}
			if !drop {
				scope.StageProjectedColumnRoots(target, writes, deletes)
			}
			if !scope.ProjectedRoots || !scope.Stage.Replacing || len(scope.Stage.Rows[target]) != 2 || len(scope.Stage.Rows[audit]) != 1 || len(scope.Stage.Deletes[target].Rows) != 2 {
				t.Fatal("shape/drop discarded earlier callback effects")
			}
			writes[0].Version = 99
			if len(scope.OwnWrites[target]) != 1 || scope.OwnWrites[target][0].Version != 1 || len(scope.OwnDeletes[target].Rows) != 1 {
				t.Fatal("complete-root conversion mutated its retained predecessor")
			}
		})
	}
}

func TestEventTriggerFrame(t *testing.T) {
	t.Run("descriptor_index", eventTriggerIndex)
	t.Run("refresh_isolation", eventTriggerRefreshIsolation)
	e := engine.New()
	actor := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE FUNCTION et_frame() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN RETURN; END$$",
		"CREATE FUNCTION et_frame_dml() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
		"CREATE FUNCTION et_frame_scalar() RETURNS integer LANGUAGE SQL AS $$SELECT 1$$",
		"CREATE EVENT TRIGGER et_frame ON ddl_command_end WHEN TAG IN ('create table','CREATE TABLE','CREATE PUBLICATION') EXECUTE FUNCTION et_frame()",
	} {
		enginetest.SessionSQL(t, actor, q)
	}
	d := eventUnitProcedure(t, e, "et_frame")
	if d.SQL.Invocation != proc.EventTriggerInvocation || d.SQL.PL.Invocation != proc.EventTriggerInvocation || len(d.SQL.Formals) != 0 || d.SQL.PL.FormalCount != 0 {
		t.Fatal("event invocation reused ordinary/DML/formal contract")
	}
	if err := d.SQL.PL.ValidateDefinition(d.SQL); err != nil {
		t.Fatal("compiler-produced event program with bare RETURN failed checked-layout validation", err)
	}
	frame, err := proc.NewPLEventTriggerFrame(d.SQL, "ddl_command_start", "CREATE TABLE")
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"ddl_command_start", "CREATE TABLE"} {
		got, err := frame.Values[i].Text()
		if err != nil || got != want || frame.Values[i].Type().ID() != sqltype.Text || d.SQL.PL.Variables[i].Record {
			t.Fatal("event TG slot is not its exact owned TEXT datum", i, got, err)
		}
	}
	frame.Values[0] = sqltype.String(frame.Values[0].Type(), "mutated invocation")
	frame.Close()
	second, err := proc.NewPLEventTriggerFrame(d.SQL, "ddl_command_end", "ALTER TABLE")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if got, _ := second.Values[0].Text(); got != "ddl_command_end" {
		t.Fatal("event frame leaked into next invocation")
	}
	if _, err := proc.NewPLTriggerFrame(d.SQL, proc.PLTriggerRelation{}, "bad", "INSERT", nil, nil, nil); err == nil {
		t.Fatal("DML frame accepted event program")
	}
	for _, name := range []string{"et_frame_dml", "et_frame_scalar"} {
		if _, err := proc.NewPLEventTriggerFrame(eventUnitProcedure(t, e, name).SQL, "ddl_command_end", "CREATE TABLE"); err == nil {
			t.Fatal("event frame accepted wrong invocation kind", name)
		}
	}
	broken := *d.SQL.PL
	broken.Invocation = proc.DMLTriggerInvocation
	definition := *d.SQL
	definition.PL = &broken
	if err := broken.ValidateDefinition(&definition); err == nil {
		t.Fatal("checked event program accepted DML invocation mutation")
	}
	read := e.CatalogRead()
	address := read.NameAddress(ids.NameKey{ClassID: ids.EventTriggerClassID, Name: "et_frame"})
	original := read.Get(address).(*catalog.EventTrigger)
	if !reflect.DeepEqual(original.Tags, []string{"CREATE TABLE", "CREATE TABLE", "CREATE PUBLICATION"}) {
		t.Fatal("filter lost input order/duplicates/canonical tags")
	}
	if err := (catalog.EventTriggerProvider{}).Validate(read, original); err != nil {
		t.Fatal(err)
	}
	foreign := engine.New().CatalogRead()
	for name, mutate := range map[string]func(*catalog.EventTrigger){
		"foreign_function": func(d *catalog.EventTrigger) {
			d.Function = foreign.Owner().Domain.Ref(read.Epoch(), d.Function.Address)
		},
		"stale_function": func(d *catalog.EventTrigger) { d.Function.Epoch++ },
		"missing_function": func(d *catalog.EventTrigger) {
			d.Function = read.Owner().Domain.Ref(read.Epoch(), ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 999999})
		},
		"foreign_owner":     func(d *catalog.EventTrigger) { d.Owner = foreign.Owner().Principals.Principal(d.Owner.ID()) },
		"invalid_event":     func(d *catalog.EventTrigger) { d.Event = "login" },
		"ineligible_tag":    func(d *catalog.EventTrigger) { d.Tags = []string{"CREATE ROLE"} },
		"unknown_tag":       func(d *catalog.EventTrigger) { d.Tags = []string{"MAKE TABLE"} },
		"wrong_rewrite_tag": func(d *catalog.EventTrigger) { d.Event = "table_rewrite"; d.Tags = []string{"CREATE TABLE"} },
		"empty_filter":      func(d *catalog.EventTrigger) { d.Tags = []string{} },
	} {
		t.Run(name, func(t *testing.T) {
			copy := *original
			copy.ObjectRevision++
			mutate(&copy)
			candidate := catalog.NewCandidate(read)
			candidate.UpdateDescriptor(&copy)
			if err := (catalog.EventTriggerProvider{}).Validate(read.WithState(candidate.State()), &copy); err == nil {
				t.Fatal("invalid descriptor authority accepted")
			}
		})
	}
	enginetest.SessionSQL(t, actor, "ALTER EVENT TRIGGER et_frame DISABLE")
	current := e.CatalogRead().Get(address).(*catalog.EventTrigger)
	if !original.Enabled || current.Enabled || current == original || !reflect.DeepEqual(current.Tags, original.Tags) {
		t.Fatal("bounded state mutation violated immutable predecessor")
	}
	oldFunction := d
	enginetest.SessionSQL(t, actor, "CREATE OR REPLACE FUNCTION et_frame() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN RAISE NOTICE 'new checked body'; END$$")
	updated := eventUnitProcedure(t, e, "et_frame")
	if updated.Address() != oldFunction.Address() || updated.Owner != oldFunction.Owner || !reflect.DeepEqual(updated.ACL, oldFunction.ACL) || updated.Revision() <= oldFunction.Revision() || updated.SQL == oldFunction.SQL || current.Function.Address != updated.Address() {
		t.Fatal("replacement did not preserve real function identity/authority")
	}
	before := e.Catalog.State
	if _, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, "CREATE OR REPLACE FUNCTION et_frame() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN RETURN 1; END$$"), nil); err == nil || e.Catalog.State != before || eventUnitProcedure(t, e, "et_frame") != updated {
		t.Fatal("failed compile replaced a live checked declaration", err)
	}
	enginetest.SessionSQL(t, actor, "CREATE OR REPLACE FUNCTION et_frame_scalar() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 2; END$$")
	if got := enginetest.Tuples(enginetest.SessionSQL(t, actor, "SELECT et_frame_scalar()")); !reflect.DeepEqual(got, [][]driver.Value{{int64(2)}}) {
		t.Fatal("shared ordinary replacement did not execute new checked body", got)
	}
}

func eventTriggerRefreshIsolation(t *testing.T) {
	e := engine.New()
	actor := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE TABLE et_refresh_base(v integer)",
		"INSERT INTO et_refresh_base VALUES(1)",
		"CREATE MATERIALIZED VIEW et_refresh_subject AS SELECT v FROM et_refresh_base",
		"CREATE UNIQUE INDEX et_refresh_key ON et_refresh_subject(v)",
		"CREATE TABLE et_refresh_audit(n integer)",
		"CREATE FUNCTION et_refresh_record() RETURNS integer LANGUAGE SQL AS $$INSERT INTO et_refresh_audit VALUES(1); SELECT 1$$",
		"CREATE FUNCTION et_refresh_event() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT et_refresh_record() INTO n; END$$",
		"CREATE EVENT TRIGGER et_refresh_start ON ddl_command_start WHEN TAG IN ('REFRESH MATERIALIZED VIEW') EXECUTE FUNCTION et_refresh_event()",
		"CREATE EVENT TRIGGER et_refresh_end ON ddl_command_end WHEN TAG IN ('REFRESH MATERIALIZED VIEW') EXECUTE FUNCTION et_refresh_event()",
	} {
		enginetest.SessionSQL(t, actor, q)
	}
	audit := enginetest.RoleTableRef(t, e, "et_refresh_audit")
	for _, q := range []string{"REFRESH MATERIALIZED VIEW et_refresh_subject", "REFRESH MATERIALIZED VIEW CONCURRENTLY et_refresh_subject"} {
		func() {
			op, err := actor.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(errors.New("unfinished refresh isolation proof"))
			before := e.Catalog.State
			if _, err := scope.Execute(enginetest.Statement(t, q), 0, nil); err != nil {
				t.Fatal(q, err)
			}
			outer := scope.MaterializedSlot
			if outer == nil || !outer.Refresh || len(outer.Sources) == 0 || scope.Catalog == nil || e.Catalog.State != before || len(scope.Stage.Rows[audit.Address]) != 2 {
				t.Fatal("refresh lost its own source scope or private callback effects")
			}
			restore := scope.ActivateSQLRoutine(nil, e.CatalogRead())
			if scope.MaterializedSlot != nil {
				t.Fatal("nested routine inherited refresh command admission")
			}
			if err := restore(nil); err != nil || scope.MaterializedSlot != outer {
				t.Fatal("routine unwind did not restore the exact outer refresh scope", err)
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(q, err)
			}
		}()
	}
	if got := enginetest.Tuples(enginetest.SessionSQL(t, actor, "SELECT count(*) FROM et_refresh_audit")); !reflect.DeepEqual(got, [][]driver.Value{{int64(4)}}) {
		t.Fatal("refresh start/end callbacks did not publish exactly once", got)
	}
}

func TestEventTriggerNoMatchingBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, declaration string
		disabled          bool
	}{
		{"empty", "", false},
		{"disabled", "CREATE TABLE", true},
		{"other_tag", "CREATE VIEW", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			actor := enginetest.StartSession(t, e, nil)
			if tc.declaration != "" {
				enginetest.SessionSQL(t, actor, "CREATE FUNCTION et_unmatched() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'unexpected callback'; END$$")
				enginetest.SessionSQL(t, actor, "CREATE EVENT TRIGGER et_unmatched ON ddl_command_end WHEN TAG IN ('"+tc.declaration+"') EXECUTE FUNCTION et_unmatched()")
				if tc.disabled {
					enginetest.SessionSQL(t, actor, "ALTER EVENT TRIGGER et_unmatched DISABLE")
				}
			}
			op, err := actor.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(errors.New("unfinished no-matching boundary proof"))
			if _, err := scope.Execute(enginetest.Statement(t, "CREATE TABLE et_unmatched_table(v integer)"), 0, nil); err != nil {
				t.Fatal(err)
			}
			if scope.Catalog != nil || scope.EventCommand != nil || !scope.Completed {
				t.Fatal("empty/nonmatching index changed ordinary utility ownership")
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
		})
	}

}
