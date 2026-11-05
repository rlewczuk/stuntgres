package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func sepgsqlTrustedReplayOperation(t *testing.T, source *core.Engine) (*core.Engine, *session.Operation) {
	t.Helper()
	stage, stageErr := source.RestoreStage()
	if stageErr != nil {
		t.Fatal(stageErr)
	}
	s, err := stage.StartRestoreSession(t.Context(), stage.RoleStore.Domain.Principal(10))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.RequestEnd(session.CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	})
	op, err := s.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	core.OperationReplay(op).StagingEffects = true
	return stage, op
}

func sepgsqlAssertImportInitialization(t *testing.T, stage *core.Engine, scope *core.StatementScope) {
	t.Helper()
	fields := reflect.ValueOf(scope).Elem()
	receipts := fields.FieldByName("routineInitializations")
	origin := reflect.ValueOf(stage).Elem().FieldByName("restoreInitializationOrigin").Pointer()
	if receipts.Len() == 0 || origin == 0 {
		t.Fatal("genuine importer omitted actual initialization completion")
	}
	iterator := receipts.MapRange()
	for iterator.Next() {
		receipt := iterator.Value()
		if receipt.FieldByName("importOrigin").Pointer() != origin || receipt.FieldByName("completion").Uint() == 0 {
			t.Fatal("import completion lost its actual origin or monotonic age")
		}
		for _, name := range []string{"policy", "subject", "generation", "frame", "depth", "predecessor", "label"} {
			if !receipt.FieldByName(name).IsZero() {
				t.Fatal("import completion fabricated client MAC evidence", name)
			}
		}
	}
	if fields.FieldByName("sepgsqlObservations").Len() != 0 || reflect.ValueOf(scope.Op).Elem().FieldByName("sepgsqlAudit").Len() != 0 {
		t.Fatal("isolated construction fabricated MAC observations or audit")
	}
}

func TestSepgsqlSimulationTrustedReplayInitialization(t *testing.T) {
	t.Run("actual_construction_default_and_constantwork", func(t *testing.T) {
		stage, op := sepgsqlTrustedReplayOperation(t, sepgsqlInitializationEngine(t))
		for _, query := range []string{
			"CREATE SEQUENCE import_sequence START 17",
			"CREATE FUNCTION import_constant(v integer) RETURNS integer LANGUAGE sql IMMUTABLE AS $$SELECT abs(v)$$",
			"CREATE FUNCTION import_default(v integer) RETURNS integer LANGUAGE sql VOLATILE AS $$SELECT abs(v)$$",
			"CREATE TABLE import_defaults(id integer,v integer DEFAULT import_default(-9))",
			"CREATE TABLE import_volatile(v bigint DEFAULT nextval('import_sequence'))",
		} {
			enginetest.RoleExecute(t, stage, op, query)
		}
		var generatorCount int
		for _, generator := range stage.Storage.Sequences {
			generatorCount++
			if generator.Last != 17 || generator.Called {
				t.Fatal("metadata default construction invoked nextval", generator)
			}
		}
		if generatorCount != 1 {
			t.Fatal("genuine sequence fixture", generatorCount)
		}
		subject, generation, frame, depth := op.Session.Sepgsql.PhaseSnapshot()
		lexical := reflect.ValueOf(op.Session.Sepgsql).Elem().FieldByName("lexical").Uint()
		inspect := func(scope *core.StatementScope) {
			sepgsqlAssertImportInitialization(t, stage, scope)
			afterSubject, afterGeneration, afterFrame, afterDepth := op.Session.Sepgsql.PhaseSnapshot()
			if subject != afterSubject || generation != afterGeneration || frame != afterFrame || depth != afterDepth ||
				reflect.ValueOf(op.Session.Sepgsql).Elem().FieldByName("lexical").Uint() != lexical {
				t.Fatal("import initialization adopted a client subject transition")
			}
		}
		sepgsqlInitializationCommand(t, stage, op, "INSERT INTO import_defaults DEFAULT VALUES RETURNING v", [][]driver.Value{{int64(9)}}, inspect)
		// Successful command Finish advances transactional generation, not a MAC transition.
		subject, generation, frame, depth = op.Session.Sepgsql.PhaseSnapshot()
		// This is genuine installed type input and nested immutable SQL work,
		// not ControlLiteral/Snapshot authority or a metadata-default execution.
		sepgsqlInitializationCommand(t, stage, op, "SELECT import_constant(int4('9'::text))", [][]driver.Value{{int64(9)}}, inspect)
		enginetest.RoleExecute(t, stage, op, "BEGIN")
		op.Done()
		s := op.Session
		cycle := s.NewProtocolCycle()
		command := func(fn func(*core.StatementScope) error) {
			enginetest.PreparedFoldProtocolCommand(t, stage, s, cycle, func(scope *core.StatementScope) error {
				core.OperationReplay(scope.Op).StagingEffects = true
				return fn(scope)
			})
		}
		command(func(scope *core.StatementScope) error {
			_, err := scope.ParseProtocol("import_insert", "INSERT INTO import_defaults(id) VALUES($1) RETURNING v", nil)
			return err
		})
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal("import_portal", "import_insert", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 1, 0, 0, 0, 1, '1', 0, 0}})
			return err
		})
		binding := core.PayloadOf(s).PreparedSQL.Binding("import_portal")
		if binding == nil {
			t.Fatal("missing actual accepted-default portal")
		}
		defaults := reflect.ValueOf(binding).Elem().FieldByName("defaults")
		if defaults.IsNil() || !defaults.Elem().FieldByName("sealed").Bool() || defaults.Elem().FieldByName("entries").Len() != 1 {
			t.Fatal("Bind omitted the genuine sealed default")
		}
		artifact := defaults.Elem().FieldByName("entries").Index(0).Elem()
		root := artifact.FieldByName("root").Pointer()
		if root == 0 || artifact.FieldByName("calls").Len() != 1 {
			t.Fatal("accepted default lost its genuine source call")
		}
		rows := 0
		command(func(scope *core.StatementScope) error {
			_, err := scope.ExecuteProtocolPortal("import_portal", 0, func(fields []session.ProtocolField) error {
				if len(fields) != 1 || fields[0].Null || string(fields[0].Bytes) != "9" {
					return errors.New("unexpected actual imported default row")
				}
				rows++
				return nil
			}, nil, nil, core.CopyProtocolHooks{})
			if err == nil {
				sepgsqlAssertImportInitialization(t, stage, scope)
				if len(sepgsqlInitializationOwners(scope, exec.AcceptedDefaultInitialization, ids.ObjectAddress{})) == 0 {
					t.Fatal("accepted default lost its typed import initializer")
				}
			}
			return err
		})
		if rows != 1 || artifact.FieldByName("root").Pointer() != root || defaults.Elem().FieldByName("retired").Bool() {
			t.Fatal("import execution replaced its accepted default source", rows)
		}
		command(func(scope *core.StatementScope) error {
			_, err := scope.Execute(enginetest.Statement(t, "ROLLBACK"), 0, nil)
			return err
		})
	})
	t.Run("sql_execute_is_not_destination_authority", func(t *testing.T) {
		stage, op := sepgsqlTrustedReplayOperation(t, sepgsqlInitializationEngine(t))
		for _, query := range []string{
			"CREATE USER import_actor",
			"CREATE FUNCTION import_denied() RETURNS integer LANGUAGE sql IMMUTABLE AS $$SELECT 9$$",
			"REVOKE EXECUTE ON FUNCTION import_denied() FROM PUBLIC",
			"SET ROLE import_actor",
		} {
			enginetest.RoleExecute(t, stage, op, query)
		}
		_, scope, project, evaluation := sepgsqlInitializationBindOperation(t, stage, op, "SELECT import_denied()")
		before := stage.CatalogRead().State()
		err := exec.AdmitExpression(evaluation, project.Expressions[0], evaluation.Catalog)
		var diagnostic *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for function import_denied"}
		if !errors.As(err, &diagnostic) || *diagnostic != want {
			t.Fatal("ordinary SQL EXECUTE diagnostic", err)
		}
		if reflect.ValueOf(scope).Elem().FieldByName("routineInitializations").Len() != 0 || stage.CatalogRead().State() != before {
			t.Fatal("denied SQL EXECUTE retained completion or effects")
		}
	})
	for _, invalid := range []string{"missing", "foreign", "copy", "epoch", "policy", "staging", "targets", "cancel", "finished", "foreign_read", "foreign_owner"} {
		t.Run("entry_rejects_"+invalid, func(t *testing.T) {
			stage, op := sepgsqlTrustedReplayOperation(t, sepgsqlInitializationEngine(t))
			_, scope, project, evaluation := sepgsqlInitializationBindOperation(t, stage, op, "SELECT abs(9)")
			node := project.Expressions[0]
			if err := exec.AdmitExpression(evaluation, node, evaluation.Catalog); err != nil {
				t.Fatal("authentic import initialization", err)
			}
			sepgsqlAssertImportInitialization(t, stage, scope)
			host := core.HostOf(scope)
			owner, read := evaluation.RoutineInitialization, evaluation.Catalog
			site := sepgsqlInitializationSite(t, evaluation, node)
			actor, err := host.ValidateRoutineInitialization(owner, site)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = host.SealRoutineInitialization(owner, read); err == nil {
				t.Fatal("import completion became a closed worker MAC proof")
			}
			before := scope.MarkSepgsqlObservations()
			var cause error
			switch invalid {
			case "missing", "foreign", "copy", "epoch", "policy":
				foreign, stageErr := stage.RestoreStage()
				if stageErr != nil {
					t.Fatal(stageErr)
				}
				restore := core.SubstituteRestoreInitializationOrigin(stage, foreign, invalid)
				defer restore()
			case "staging":
				core.OperationReplay(op).StagingEffects = false
			case "targets":
				targets := stage.ImportTargets
				stage.ImportTargets = nil
				defer func() { stage.ImportTargets = targets }()
			case "cancel":
				original := op.Ctx
				defer func() { op.Ctx = original }()
				cause = errors.New("canceled actual import initialization")
				ctx, cancel := context.WithCancelCause(original)
				cancel(cause)
				op.Ctx = ctx
			case "finished":
				if err := scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
			case "foreign_read", "foreign_owner":
				other, otherOp := sepgsqlTrustedReplayOperation(t, sepgsqlInitializationEngine(t))
				_, _, _, foreign := sepgsqlInitializationBindOperation(t, other, otherOp, "SELECT abs(9)")
				if invalid == "foreign_read" {
					read = foreign.Catalog
				} else {
					owner = foreign.RoutineInitialization
				}
			}
			_, err = host.ValidateRoutineInitialization(owner, site)
			if invalid != "foreign_read" && err == nil {
				t.Fatal("invalid origin adopted actual import completion", invalid)
			}
			if host.ValidateRoutineInitializationOwner(owner, read) == nil {
				t.Fatal("invalid origin adopted an initialization owner", invalid)
			}
			if invalid != "foreign_read" && host.InitializeRoutineCall(owner, site, actor) == nil {
				t.Fatal("repeated initialization revived an invalid import", invalid)
			}
			if cause != nil && !errors.Is(err, cause) && !errors.Is(err, context.Canceled) {
				t.Fatal("import cancellation lost its actual context error", err)
			}
			if invalid != "finished" && scope.MarkSepgsqlObservations() != before {
				t.Fatal("rejected import changed completion/observation state", invalid)
			}
		})
	}
	t.Run("zero_call_seal_and_second_session", func(t *testing.T) {
		stage, op := sepgsqlTrustedReplayOperation(t, sepgsqlInitializationEngine(t))
		_, scope, _, evaluation := sepgsqlInitializationBindOperation(t, stage, op, "SELECT 9")
		if _, err := core.HostOf(scope).SealRoutineInitialization(evaluation.RoutineInitialization, evaluation.Catalog); err == nil {
			t.Fatal("empty importer owner became a closed worker proof")
		}
		restore := core.SubstituteRestoreInitializationOrigin(stage, nil, "copy")
		if core.HostOf(scope).ValidateRoutineInitializationOwner(evaluation.RoutineInitialization, evaluation.Catalog) == nil {
			t.Fatal("empty owner adopted a same-fields replacement origin")
		}
		restore()
		if second, err := stage.StartRestoreSession(t.Context(), stage.RoleStore.Domain.Principal(10)); err == nil {
			second.RequestEnd(session.CleanClose)
			_ = second.WaitEnd(context.Background())
			t.Fatal("second private session adopted the import origin")
		}
	})
	t.Run("ordinary_session_and_hostless_cannot_adopt", func(t *testing.T) {
		stage, op := sepgsqlTrustedReplayOperation(t, sepgsqlInitializationEngine(t))
		_, _, _, evaluation := sepgsqlInitializationBindOperation(t, stage, op, "SELECT 9")
		hostless := *evaluation
		hostless.Host, hostless.RoutineInitialization = nil, exec.RoutineInitialization{}
		if err := exec.BeginRoutineInitialization(&hostless); err == nil {
			t.Fatal("hostless context adopted importer authority")
		}
		publicOp, done := enginetest.RoleOperation(t, stage, "postgres")
		t.Cleanup(done)
		scope, err := stage.StartStatement(publicOp)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { scope.Finish(nil) })
		if err = scope.Gate(enginetest.Statement(t, "SELECT 9")); err != nil {
			t.Fatal(err)
		}
		core.OperationReplay(publicOp).StagingEffects = true
		if _, err = core.HostOf(scope).NewRoutineInitialization(); err == nil {
			t.Fatal("ordinary physical session borrowed bound importer origin")
		}
	})
	for _, invalid := range []string{"staging", "targets", "runtime"} {
		t.Run("direct_source_rejects_"+invalid, func(t *testing.T) {
			stage, op := sepgsqlTrustedReplayOperation(t, sepgsqlInitializationEngine(t))
			enginetest.RoleExecute(t, stage, op, "CREATE EXTENSION hstore")
			enginetest.RoleExecute(t, stage, op, "CREATE TABLE import_support(v hstore)")
			_, scope, project, evaluation := sepgsqlInitializationBindOperation(t, stage, op, "SELECT v FROM import_support ORDER BY v")
			sort, ok := project.Child.(*plan.SortNode)
			if !ok || len(sort.Keys) != 1 {
				t.Fatal("missing genuine selected sort support")
			}
			var site exec.DirectRoutineInitializationSite
			count := 0
			if err := sort.Keys[0].Comparison.VisitIndexSupports(func(support sqltype.IndexSupport, slot uint16) error {
				if err := exec.InitializeIndexSupport(evaluation, support, slot); err != nil {
					return err
				}
				callback, err := catalog.CaptureIndexRoutineCallback(evaluation.Catalog, support, slot)
				if err != nil {
					return err
				}
				site = exec.DirectRoutineInitializationSite{Routine: callback.Helper, Reference: callback.Reference, Purpose: exec.DirectIndexSupport, Source: callback.Source, Ordinal: callback.Slot}
				count++
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			host := core.HostOf(scope)
			if count != 1 || host.ValidateDirectRoutineInitialization(evaluation.RoutineInitialization, site) != nil {
				t.Fatal("actual direct import source not initialized", count)
			}
			before := scope.MarkSepgsqlObservations()
			switch invalid {
			case "staging":
				core.OperationReplay(op).StagingEffects = false
			case "targets":
				targets := stage.ImportTargets
				stage.ImportTargets = nil
				defer func() { stage.ImportTargets = targets }()
			case "runtime":
				runtime := stage.Sepgsql
				stage.Sepgsql = sepgsqlInitializationEngine(t).Sepgsql
				defer func() { stage.Sepgsql = runtime }()
			}
			if host.InitializeDirectRoutine(evaluation.RoutineInitialization, site) == nil ||
				host.ValidateDirectRoutineInitialization(evaluation.RoutineInitialization, site) == nil {
				t.Fatal("direct completion survived removed importer provenance", invalid)
			}
			if scope.MarkSepgsqlObservations() != before {
				t.Fatal("direct rejection changed completion state")
			}
		})
	}
	t.Run("failed_session_start_does_not_bind_origin", func(t *testing.T) {
		stage, stageErr := sepgsqlInitializationEngine(t).RestoreStage()
		if stageErr != nil {
			t.Fatal(stageErr)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := stage.StartRestoreSession(ctx, stage.RoleStore.Domain.Principal(10)); !errors.Is(err, context.Canceled) {
			t.Fatal("failed restore-session start lost cancellation", err)
		}
		s, err := stage.StartRestoreSession(t.Context(), stage.RoleStore.Domain.Principal(10))
		if err != nil {
			t.Fatal("failed start consumed the one genuine session binding", err)
		}
		s.RequestEnd(session.CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	for _, mode := range []string{"staging", "destination", "private"} {
		t.Run("ordinary_flags_cannot_mint_origin_"+mode, func(t *testing.T) {
			e := sepgsqlInitializationEngine(t)
			var op *session.Operation
			if mode == "private" {
				s, err := e.StartRestoreSession(t.Context(), e.RoleStore.Domain.Principal(10))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					s.RequestEnd(session.CleanClose)
					if err := s.WaitEnd(context.Background()); err != nil {
						t.Error(err)
					}
				})
				op, err = s.Acquire(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(op.Done)
			} else {
				var done func()
				op, done = enginetest.RoleOperation(t, e, "postgres")
				t.Cleanup(done)
			}
			e.ImportTargets = make(map[authorization.PrincipalRef]authorization.PrincipalRef)
			t.Cleanup(func() { e.ImportTargets = nil })
			core.OperationReplay(op).StagingEffects = mode != "destination"
			core.OperationReplay(op).TableDestination = mode == "destination"
			_, scope, project, evaluation := sepgsqlInitializationBindOperation(t, e, op, "SELECT abs(9)")
			err := exec.AdmitExpression(evaluation, project.Expressions[0], evaluation.Catalog)
			if err == nil || err.Error() != "stuntgres: missing actual routine initialization policy" {
				t.Fatal("ordinary flags minted importer initialization", err)
			}
			if reflect.ValueOf(scope).Elem().FieldByName("routineInitializations").Len() != 0 {
				t.Fatal("forged provenance published a completion")
			}
		})
	}
}
