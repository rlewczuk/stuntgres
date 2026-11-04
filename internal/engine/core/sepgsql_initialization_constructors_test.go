package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/workadmission"
)

func sepgsqlInitializationCommand(t *testing.T, e *core.Engine, op *session.Operation, query string, want [][]driver.Value, inspect func(*core.StatementScope)) {
	t.Helper()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	finished := false
	defer func() {
		if !finished {
			scope.Finish(nil)
		}
	}()
	result, err := scope.Execute(enginetest.Statement(t, query), 0, nil)
	if err != nil {
		t.Fatal(query, err)
	}
	if result.Rows != nil {
		got := enginetest.Tuples(result)
		if err = result.Rows.Close(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: got %#v want %#v", query, got, want)
		}
	}
	if inspect != nil {
		inspect(scope)
	}
	if err = scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	finished = true
}

// Read genuine private receipts without exporting test-only authority or
// modifying their maps. This is structural lifetime evidence, not a substitute
// for the public independently expected constructor outcomes.
func sepgsqlInitializationOwners(scope *core.StatementScope, phase exec.RoutineInitializationPhase, routine ids.ObjectAddress) map[uint64]bool {
	owners := map[uint64]bool{}
	receipts := reflect.ValueOf(scope).Elem().FieldByName("routineInitializations")
	iterator := receipts.MapRange()
	for iterator.Next() {
		call := iterator.Key()
		site := call.FieldByName("Site")
		if exec.RoutineInitializationPhase(site.FieldByName("Phase").Uint()) != phase {
			continue
		}
		if routine != (ids.ObjectAddress{}) {
			address := site.FieldByName("Reference").FieldByName("Address")
			if address.FieldByName("ClassID").Uint() != uint64(routine.ClassID) || address.FieldByName("ObjectID").Uint() != uint64(routine.ObjectID) {
				continue
			}
		}
		owners[call.FieldByName("Owner").FieldByName("instance").Uint()] = true
	}
	return owners
}

func TestSepgsqlSimulationInitializationConstructors(t *testing.T) {
	e := sepgsqlInitializationEngine(t)
	for _, query := range []string{
		"CREATE FUNCTION io_default() RETURNS text LANGUAGE sql VOLATILE AS $$SELECT 'ok'::text$$",
		"CREATE FUNCTION io_domain(v text) RETURNS boolean LANGUAGE sql VOLATILE AS $$SELECT v = 'ok'$$",
		"CREATE DOMAIN io_guarded AS text CHECK(io_domain(VALUE))",
		"CREATE TABLE io_attached(id integer,v io_guarded DEFAULT io_default())",
		"CREATE FUNCTION io_key(v text) RETURNS text LANGUAGE sql IMMUTABLE STRICT AS $$SELECT v || ''$$",
		"CREATE FUNCTION io_pred(v text) RETURNS boolean LANGUAGE sql IMMUTABLE STRICT AS $$SELECT v = 'ok'$$",
		"CREATE INDEX io_index ON io_attached(io_key(v::text)) WHERE io_pred(v::text)",
		// Constant definition prewalk executes the real SQL body even before
		// the empty table has a build tuple. It must suspend the DDL mutex.
		"CREATE INDEX io_constant_index ON io_attached(io_key('ok')) WHERE io_pred('ok')",
		"CREATE FUNCTION io_when(v text) RETURNS boolean LANGUAGE sql VOLATILE AS $$SELECT v = 'ok'$$",
		"CREATE FUNCTION io_trigger() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
		"CREATE TRIGGER io_before BEFORE INSERT ON io_attached FOR EACH ROW WHEN(io_when(NEW.v::text)) EXECUTE FUNCTION io_trigger()",
		"CREATE TABLE io_commands(v text)",
		"CREATE FUNCTION io_sql(v text) RETURNS text LANGUAGE sql VOLATILE AS $$SELECT io_key(v)$$",
		"CREATE FUNCTION io_pl(v text) RETURNS text LANGUAGE plpgsql AS $$BEGIN RETURN io_key(v); END$$",
	} {
		enginetest.Run(t, e, query)
	}
	var defaultRoutine ids.ObjectAddress
	for _, descriptor := range e.CatalogRead().Descriptors() {
		if routine, ok := descriptor.(*catalog.Procedure); ok && routine.Name() == "io_default" {
			defaultRoutine = routine.Address()
			break
		}
	}
	if defaultRoutine == (ids.ObjectAddress{}) {
		t.Fatal("missing genuine column default procedure")
	}
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	previous := map[uint64]bool{}
	constructorPhases := []exec.RoutineInitializationPhase{exec.AcceptedDefaultInitialization, exec.DomainCheckInitialization, exec.IndexRecipeInitialization, exec.TriggerWhenInitialization}
	for _, id := range []string{"1", "2"} {
		sepgsqlInitializationCommand(t, e, op, "INSERT INTO io_attached(id) VALUES("+id+") RETURNING v::text", [][]driver.Value{{"ok"}}, func(scope *core.StatementScope) {
			// Direct execution has an ordinary stored default, not a sealed
			// protocol AcceptedDefault artifact. Prove its exact routine source.
			ordinary := sepgsqlInitializationOwners(scope, exec.RetainedExpressionInitialization, defaultRoutine)
			if len(ordinary) == 0 {
				t.Fatal("ordinary default lost its initialized routine source")
			}
			for owner := range ordinary {
				if owner == 0 || previous[owner] {
					t.Fatal("ordinary default reused an earlier command owner", owner)
				}
				previous[owner] = true
			}
			for _, phase := range constructorPhases[1:] {
				owners := sepgsqlInitializationOwners(scope, phase, ids.ObjectAddress{})
				if len(owners) == 0 {
					t.Fatal("real constructor lost its typed initialization owner", phase)
				}
				for owner := range owners {
					if owner == 0 || previous[owner] {
						t.Fatal("fresh constructor adopted an earlier command owner", phase, owner)
					}
					previous[owner] = true
				}
			}
		})
	}
	// The accepted-default phase belongs to the genuine artifact from Bind,
	// not a direct scope.Execute command. Keep all four typed assertions
	// on this real Parse/Bind/Execute lane, across two fresh portal executions.
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) {
		enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn)
	}
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("io_insert", "INSERT INTO io_attached(id) VALUES($1) RETURNING v::text", nil)
		return err
	})
	for _, id := range []byte{'3', '4'} {
		name := "io_portal_" + string(id)
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "io_insert", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 1, 0, 0, 0, 1, id, 0, 0}})
			return err
		})
		binding := core.PayloadOf(s).PreparedSQL.Binding(name)
		if binding == nil {
			t.Fatal("missing genuine bound constructor portal")
		}
		defaults := reflect.ValueOf(binding).Elem().FieldByName("defaults")
		if defaults.IsNil() {
			t.Fatal("Bind omitted the actual accepted default owner")
		}
		set := defaults.Elem()
		if !set.FieldByName("sealed").Bool() || set.FieldByName("retired").Bool() || set.FieldByName("entries").Len() != 1 {
			t.Fatal("Bind did not seal the complete accepted default")
		}
		artifact := set.FieldByName("entries").Index(0).Elem()
		if artifact.FieldByName("root").IsNil() || artifact.FieldByName("calls").Len() != 1 {
			t.Fatal("accepted default omitted its actual routine source")
		}
		root := artifact.FieldByName("root").Pointer()
		owners := make([]map[uint64]bool, len(constructorPhases))
		rows := 0
		command(func(scope *core.StatementScope) error {
			_, err := scope.ExecuteProtocolPortal(name, 0, func(fields []session.ProtocolField) error {
				if len(fields) != 1 || fields[0].Null || string(fields[0].Bytes) != "ok" {
					return errors.New("accepted constructor returned an unexpected actual row")
				}
				rows++
				return nil
			}, nil, nil, core.CopyProtocolHooks{})
			if err == nil {
				for i, phase := range constructorPhases {
					owners[i] = sepgsqlInitializationOwners(scope, phase, ids.ObjectAddress{})
				}
			}
			return err
		})
		if rows != 1 || artifact.FieldByName("root").Pointer() != root || set.FieldByName("retired").Bool() {
			t.Fatal("Execute lost the accepted source or actual constructor row", rows)
		}
		for i, phase := range constructorPhases {
			if len(owners[i]) == 0 {
				t.Fatal("real accepted constructor lost its typed initialization owner", phase)
			}
			for owner := range owners[i] {
				if owner == 0 || previous[owner] {
					t.Fatal("fresh accepted constructor adopted an earlier owner", phase, owner)
				}
				previous[owner] = true
			}
		}
	}
	enginetest.SessionSQL(t, s, "COMMIT")
	sepgsqlInitializationCommand(t, e, op, "SELECT id,v::text FROM io_attached ORDER BY id", [][]driver.Value{{int64(1), "ok"}, {int64(2), "ok"}, {int64(3), "ok"}, {int64(4), "ok"}}, nil)
	sepgsqlInitializationCommand(t, e, op, "CREATE INDEX io_populated_index ON io_attached(io_key(v::text),io_key('ok')) WHERE io_pred(v::text)", nil, func(scope *core.StatementScope) {
		definition := sepgsqlInitializationOwners(scope, exec.RetainedExpressionInitialization, ids.ObjectAddress{})
		build := sepgsqlInitializationOwners(scope, exec.IndexRecipeInitialization, ids.ObjectAddress{})
		if len(definition) == 0 || len(build) == 0 {
			t.Fatal("definition prewalk or genuine populated build lost its owner", definition, build)
		}
		for owner := range build {
			if definition[owner] || previous[owner] {
				t.Fatal("populated build adopted another initializer's owner", owner)
			}
		}
	})
	sepgsqlInitializationCommand(t, e, op, "WITH ins AS (INSERT INTO io_commands VALUES('ok') RETURNING io_key(v) AS v) INSERT INTO io_commands SELECT io_key(v) FROM ins RETURNING v", [][]driver.Value{{"ok"}}, func(scope *core.StatementScope) {
		var selected ids.ObjectAddress
		for _, dependency := range scope.RoutineDependencies {
			if p, ok := scope.ResolutionCatalog().Procedure(dependency.Ref.Address); ok && p.Name() == "io_key" {
				selected = p.Address()
				break
			}
		}
		if selected == (ids.ObjectAddress{}) {
			t.Fatal("missing real CTE-selected function")
		}
		owners := sepgsqlInitializationOwners(scope, exec.RetainedExpressionInitialization, selected)
		if len(owners) < 2 {
			t.Fatal("CTE and main command merged initialized source owners", owners)
		}
	})
	sepgsqlInitializationCommand(t, e, op, "SELECT io_sql('ok'),io_pl('ok')", [][]driver.Value{{"ok", "ok"}}, func(scope *core.StatementScope) {
		// Each actual SQL/PL command has a fresh source even though the two body
		// commands select the same immutable io_key declaration.
		owners := sepgsqlInitializationOwners(scope, exec.RetainedExpressionInitialization, ids.ObjectAddress{})
		if len(owners) < 3 {
			t.Fatal("SQL/PL body commands reused the outer source owner", owners)
		}
	})
}

func TestSepgsqlSimulationInitializationIndexPrewalk(t *testing.T) {
	for _, change := range []string{"catalog", "label", "role", "cancel"} {
		t.Run(change, func(t *testing.T) {
			e := sepgsqlInitializationEngine(t)
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			original := op.Ctx
			ctx, cancel := context.WithCancel(original)
			defer cancel()
			calls := 0
			mutations := map[string]string{
				"catalog": "CREATE TABLE io_peer(v integer)",
				"label":   "SECURITY LABEL FOR selinux ON FUNCTION json_strip_nulls(json,boolean) IS 'D'",
				"role":    "CREATE ROLE io_peer",
				"cancel":  "SELECT 1",
			}
			mutation := enginetest.Statement(t, mutations[change])
			var peerErr error
			barrierErr := errors.New("actual index definition work interrupted")
			op.Ctx = workadmission.With(ctx, func(_ context.Context, class workadmission.Class) error {
				if class != workadmission.WorkJSONStripNulls {
					return nil
				}
				calls++
				if calls != 1 {
					return errors.New("index definition replayed actual constant work")
				}
				if change == "cancel" {
					cancel()
				} else {
					_, peerErr = enginetest.Execute(e, context.Background(), mutation, nil)
					if peerErr != nil {
						return peerErr
					}
					return barrierErr
				}
				return nil
			})
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			_, err = scope.Execute(enginetest.Statement(t, "CREATE INDEX io_fenced ON init_owner_rows(((json_strip_nulls('{\"a\":null}'::json))::text))"), 0, nil)
			finishErr := scope.Finish(err)
			op.Ctx = original
			if peerErr != nil {
				t.Fatal("genuine peer mutation failed", peerErr)
			}
			if calls != 1 || err == nil {
				t.Fatal("actual unlocked prewalk missed its publication veto", calls, err)
			}
			if errors.Is(err, barrierErr) {
				t.Fatal("prewalk failed to readmit its changed predecessor", err)
			}
			if change == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("prewalk lost cancellation", err)
			}
			if finishErr != nil && !errors.Is(finishErr, err) {
				t.Fatal("prewalk cleanup replaced its primary failure", finishErr, err)
			}
			probe, release := enginetest.RoleOperation(t, e, "postgres")
			defer release()
			sepgsqlPhaseRows(t, e, probe, "SELECT count(*) FROM pg_class WHERE relname='io_fenced'", [][]driver.Value{{int64(0)}})
		})
	}
}

func TestSepgsqlSimulationInitializationCursorDetach(t *testing.T) {
	e := sepgsqlInitializationEngine(t)
	enginetest.Run(t, e, "INSERT INTO init_owner_rows VALUES(-1),(-2),(-3)")
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "BEGIN")
	enginetest.RoleExecute(t, e, op, "DECLARE io_cursor NO SCROLL CURSOR FOR SELECT abs(v) FROM init_owner_rows ORDER BY v DESC")
	cursor := core.PayloadOf(op.Session).Cursors.Entries["io_cursor"]
	if cursor == nil {
		t.Fatal("missing actual cursor owner")
	}
	detached := func() {
		t.Helper()
		value := reflect.ValueOf(cursor).Elem()
		authority := value.FieldByName("authority")
		if !authority.IsNil() && !authority.Elem().FieldByName("RoutineInitialization").IsZero() {
			t.Fatal("cursor authority retained detached command initialization")
		}
		if !authority.IsNil() && !authority.Elem().FieldByName("RoutineCatalog").IsZero() {
			t.Fatal("cursor authority retained detached invocation catalog")
		}
		if !value.FieldByName("state").FieldByName("RoutineInitialization").IsZero() {
			t.Fatal("cursor state retained detached command initialization")
		}
	}
	detached()
	previous := map[uint64]bool{}
	for _, want := range []int64{1, 2} {
		sepgsqlInitializationCommand(t, e, op, "FETCH FORWARD 1 FROM io_cursor", [][]driver.Value{{want}}, func(scope *core.StatementScope) {
			owners := sepgsqlInitializationOwners(scope, exec.RetainedExpressionInitialization, ids.ObjectAddress{})
			if len(owners) == 0 {
				t.Fatal("FETCH attachment never initialized its retained expression")
			}
			for owner := range owners {
				if previous[owner] {
					t.Fatal("new FETCH attachment reused a detached source owner")
				}
				previous[owner] = true
			}
		})
		detached()
	}
	enginetest.RoleExecute(t, e, op, "CLOSE io_cursor")
	enginetest.RoleExecute(t, e, op, "COMMIT")
	// WITH HOLD detaches into genuinely owned rows; it must carry neither a
	// borrowed source owner nor a live producer into the next transaction.
	enginetest.RoleExecute(t, e, op, "BEGIN")
	enginetest.RoleExecute(t, e, op, "DECLARE io_held NO SCROLL CURSOR WITH HOLD FOR SELECT abs(v) FROM init_owner_rows ORDER BY v DESC")
	enginetest.RoleExecute(t, e, op, "FETCH FORWARD 1 FROM io_held")
	enginetest.RoleExecute(t, e, op, "COMMIT")
	cursor = core.PayloadOf(op.Session).Cursors.Entries["io_held"]
	if cursor == nil {
		t.Fatal("missing promoted held cursor")
	}
	detached()
	value := reflect.ValueOf(cursor).Elem()
	if !value.FieldByName("Source").IsNil() || !value.FieldByName("stage").IsNil() {
		t.Fatal("hold promotion retained a live producer")
	}
	sepgsqlPhaseRows(t, e, op, "FETCH ALL FROM io_held", [][]driver.Value{{int64(2)}, {int64(3)}})
	enginetest.RoleExecute(t, e, op, "CLOSE io_held")
}
