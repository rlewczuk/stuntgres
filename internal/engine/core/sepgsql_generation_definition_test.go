package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"maps"
	"reflect"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/util"
	"stuntgres/internal/workadmission"
)

type sepgsqlGenerationAudit struct {
	mu     sync.Mutex
	events []sepgsql.AuditEvent
}

func (a *sepgsqlGenerationAudit) snapshot() []sepgsql.AuditEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]sepgsql.AuditEvent(nil), a.events...)
}

func sepgsqlGenerationEngine(t *testing.T) (*core.Engine, *sepgsqlGenerationAudit) {
	t.Helper()
	e := engine.NewWithSettings(nil, "", false)
	audit := new(sepgsqlGenerationAudit)
	runtime, err := sepgsql.Compile(sepgsql.SimulationConfig{InstallSQL: true, AuditAllowed: true, Audit: func(event sepgsql.AuditEvent) {
		audit.mu.Lock()
		audit.events = append(audit.events, event)
		audit.mu.Unlock()
	}, Policy: sepgsql.Policy{
		ID: "generation-definition", Version: "1", Contexts: []string{"A", "P", "D"}, InitialContext: "A", UnlabeledContext: "P",
		Rules: []sepgsql.Rule{
			{Source: "A", Target: "P", Class: "*", Allow: []string{"*"}},
			{Source: "A", Target: "D", Class: "db_procedure", Allow: []string{"*"}, Deny: []string{"execute"}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	err = e.InstallSepgsqlSimulation(runtime, true)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return e, audit
}

const sepgsqlGenerationExpression = "CASE WHEN payload IS NULL THEN abs(-3) ELSE json_array_length(payload) END"

func TestSepgsqlSimulationGeneratedDefinitionInitialization(t *testing.T) {
	for _, importer := range []bool{false, true} {
		name := "live"
		if importer {
			name = "import"
		}
		t.Run(name, func(t *testing.T) { sepgsqlGenerationConstruction(t, importer) })
	}
	for _, command := range []string{"create", "alter", "insert"} {
		phase := "stored_sql_routine"
		if command == "create" {
			phase = "folded_builtin"
		}
		for _, denial := range []string{"acl", "mac"} {
			t.Run(command+"/"+phase+"/"+denial, func(t *testing.T) { sepgsqlGenerationDenial(t, command, denial) })
		}
	}
	for _, command := range []string{"create", "alter"} {
		for _, change := range []string{"healthy", "catalog", "role", "descriptor", "label", "cancel", "session_end"} {
			t.Run(command+"/pause/"+change, func(t *testing.T) { sepgsqlGenerationPause(t, command, change) })
		}
	}
	for _, invalid := range []string{"missing_scope", "foreign_scope", "finished_scope", "wrong_operation", "wrong_lease", "wrong_read", "foreign_stage"} {
		t.Run("borrow/"+invalid, func(t *testing.T) { sepgsqlGenerationRejectBorrow(t, invalid) })
	}
	for _, fail := range []bool{false, true} {
		name := "borrow/restored"
		if fail {
			name += "/failed_callback"
		}
		t.Run(name, func(t *testing.T) { sepgsqlGenerationRestoredBorrow(t, fail) })
	}
	for _, mode := range []string{"live", "import", "retained_temp"} {
		t.Run("runtime_owner/"+mode, func(t *testing.T) { sepgsqlGenerationRuntimeOwner(t, mode) })
	}
	for _, invalid := range []string{"missing_refs", "other_column_refs", "other_column_source", "foreign_consumer", "prospective_consumer", "other_command", "origin", "staging", "finished", "canceled", "import_seal"} {
		t.Run("runtime_reject/"+invalid, func(t *testing.T) { sepgsqlGenerationRuntimeReject(t, invalid) })
	}
	t.Run("source_header", sepgsqlGenerationSourceHeader)
	t.Run("source_order", sepgsqlGenerationSourceOrder)
}

func sepgsqlGenerationConstruction(t *testing.T, importer bool) {
	t.Helper()
	e, audit := sepgsqlGenerationEngine(t)
	var op *session.Operation
	if importer {
		e, op = sepgsqlTrustedReplayOperation(t, e)
	} else {
		var done func()
		op, done = enginetest.RoleOperation(t, e, "postgres")
		t.Cleanup(done)
	}
	origin := sepgsqlRepairOriginOf(e)
	enginetest.RoleExecute(t, e, op, "CREATE SEQUENCE generation_steps START 17")
	events := len(audit.snapshot())
	var expectedConsumer *catalog.Relation
	inspect := func(scope *core.StatementScope) {
		if importer {
			sepgsqlAssertImportInitialization(t, e, scope)
		} else {
			sepgsqlGenerationLiveReceipts(t, e, scope)
		}
		if sepgsqlRepairOriginOf(e) != origin {
			t.Fatal("generation prewalk mutated its original import origin")
		}
		if expectedConsumer != nil {
			sepgsqlGenerationRuntimeSites(t, scope, expectedConsumer, 1)
		}
	}
	query := "CREATE TABLE generation_rows(id integer,payload json,g integer GENERATED ALWAYS AS(" + sepgsqlGenerationExpression + ") STORED,marker bigint DEFAULT nextval('generation_steps'))"
	sepgsqlInitializationCommand(t, e, op, query, nil, inspect)
	if importer && len(audit.snapshot()) != events {
		t.Fatal("import construction emitted fictional client MAC audit")
	}
	for _, generator := range e.Storage.Sequences {
		if generator.Last != 17 || generator.Called {
			t.Fatal("generation metadata invoked unrelated volatile default")
		}
	}
	enginetest.RoleExecute(t, e, op, "INSERT INTO generation_rows(id,payload,marker) VALUES(1,NULL,99),(2,'[1,2]',99)")
	enginetest.RoleExecute(t, e, op, "CREATE INDEX generation_rows_index ON generation_rows(g)")
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "generation_rows")
	original := e.CatalogRead().MustRelation(ref.Address)
	originalHeap := e.Storage.Relations[ref.Address]
	sepgsqlGenerationDefinition(t, e, original)
	expectedConsumer = original
	query = "ALTER TABLE generation_rows ALTER COLUMN g SET EXPRESSION AS (" + sepgsqlGenerationExpression + ")"
	sepgsqlInitializationCommand(t, e, op, query, nil, inspect)
	current := e.CatalogRead().MustRelation(ref.Address)
	if current == original || current.ColumnMeta.Generations[3].OID == original.ColumnMeta.Generations[3].OID {
		t.Fatal("ALTER did not replace the actual generation definition")
	}
	sepgsqlGenerationDefinition(t, e, current)
	sepgsqlPhaseRows(t, e, op, "SELECT id,g FROM generation_rows ORDER BY id", [][]driver.Value{{int64(1), int64(3)}, {int64(2), int64(2)}})
	idx := enginetest.CatalogRef(t, e, e.CatalogRead(), "generation_rows_index")
	root := e.Storage.IndexRoots[idx.Address]
	heap := e.Storage.Relations[ref.Address]
	if root == nil || len(root.Entries) != 2 || heap == nil || heap == originalHeap {
		t.Fatal("generation rewrite omitted real index membership")
	}
	for i, want := range []int64{2, 3} {
		if len(root.Entries[i].Keys) != 1 {
			t.Fatal("generation rewrite omitted index key")
		}
		member := false
		for _, row := range heap.Versions {
			member = member || row.Row == root.Entries[i].Row && row.Version == root.Entries[i].Version
		}
		if !member {
			t.Fatal("generation index retained foreign physical row membership")
		}
		got, err := root.Entries[i].Keys[0].Integer()
		if err != nil || got != want {
			t.Fatal("generation rewrite index key", got, want, err)
		}
	}
}

func sepgsqlGenerationDefinition(t *testing.T, e *core.Engine, d *catalog.Relation) {
	t.Helper()
	definition := d.ColumnMeta.Generations[3]
	if definition.Expression == nil || definition.Planned == nil || reflect.DeepEqual(definition.Expression, definition.Planned) || len(definition.Dependencies) == 0 {
		t.Fatal("generation lost separate original/planned recipes or dependencies")
	}
	if err := table.ValidateGenerationProviders(e.CatalogRead(), d); err != nil {
		t.Fatal("generation retained foreign routine/source ownership", err)
	}
	originalAbs, plannedAbs := false, false
	for _, selected := range []struct {
		node expr.Expr
		seen *bool
	}{{definition.Expression, &originalAbs}, {definition.Planned, &plannedAbs}} {
		plan.WalkExpression(selected.node, func(node expr.Expr) {
			routine, err := exec.ExpressionRoutineDescriptor(node)
			if err != nil {
				t.Fatal(err)
			}
			if routine != nil && routine.Name() == "abs" {
				*selected.seen = true
			}
		})
	}
	if !originalAbs || plannedAbs {
		t.Fatal("constant prewalk changed original source or failed actual abs fold")
	}
}

func sepgsqlGenerationLiveReceipts(t *testing.T, e *core.Engine, scope *core.StatementScope) {
	t.Helper()
	receipts := reflect.ValueOf(scope).Elem().FieldByName("routineInitializations")
	if receipts.Len() == 0 {
		t.Fatal("generation prewalk omitted genuine completed initialization")
	}
	calls := receipts.MapRange()
	for calls.Next() {
		call, receipt := calls.Key(), calls.Value()
		owner, site := call.FieldByName("Owner"), call.FieldByName("Site")
		if owner.FieldByName("command").Uint() != uint64(scope.CommandID) || owner.FieldByName("transaction").Uint() != uint64(scope.TxRef.ID()) || owner.FieldByName("instance").Uint() == 0 || receipt.FieldByName("completion").Uint() == 0 {
			t.Fatal("generation receipt belongs to another command")
		}
		if !receipt.FieldByName("importOrigin").IsNil() || receipt.FieldByName("policy").Pointer() != reflect.ValueOf(e.Sepgsql).Pointer() || receipt.FieldByName("subject").String() != "A" || receipt.FieldByName("predecessor").IsNil() {
			t.Fatal("live generation did not retain genuine live policy evidence")
		}
		if site.FieldByName("ManifestOrdinal").Uint() == 0 && (site.FieldByName("Occurrence").Uint() == 0 || site.FieldByName("Source").FieldByName("Frame").Uint() == 0) {
			t.Fatal("generation receipt lost original source occurrence")
		}
	}
}

func sepgsqlGenerationDenial(t *testing.T, command, denial string) {
	t.Helper()
	e, audit := sepgsqlGenerationEngine(t)
	admin, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	for _, query := range []string{
		"CREATE ROLE generation_actor LOGIN",
		"GRANT USAGE,CREATE ON SCHEMA public TO generation_actor",
		"CREATE FUNCTION generation_probe(v integer) RETURNS integer LANGUAGE sql IMMUTABLE AS $$SELECT json_array_length(json_strip_nulls('[1,2,3]'::json))$$",
		"CREATE SEQUENCE generation_guard START 23",
	} {
		enginetest.RoleExecute(t, e, admin, query)
	}
	actor, release := enginetest.RoleOperation(t, e, "generation_actor")
	released := false
	endActor := func() {
		if !released {
			released = true
			release()
		}
	}
	t.Cleanup(endActor)
	if command != "create" {
		expression := "json_array_length(payload)"
		if command == "insert" {
			expression = "CASE WHEN payload IS NULL THEN generation_probe(-3) ELSE json_array_length(payload) END"
		}
		enginetest.RoleExecute(t, e, actor, "CREATE TABLE generation_denied(payload json,g integer GENERATED ALWAYS AS("+expression+") STORED)")
		enginetest.RoleExecute(t, e, actor, "INSERT INTO generation_denied(payload) VALUES('[1,2]')")
		enginetest.RoleExecute(t, e, actor, "CREATE INDEX generation_denied_index ON generation_denied(g)")
	}
	// SQLRoutineExpr is FoldNever: its genuine EXECUTE demand is stored-program
	// preparation/invocation, not CREATE's definition-only constant simplifier.
	routine, routineName := "generation_probe(integer)", "generation_probe"
	if command == "create" {
		routine, routineName = "pg_catalog.json_strip_nulls(json,boolean)", "json_strip_nulls"
	}
	if denial == "acl" {
		enginetest.RoleExecute(t, e, admin, "REVOKE EXECUTE ON FUNCTION "+routine+" FROM PUBLIC")
	} else {
		enginetest.RoleExecute(t, e, admin, "SECURITY LABEL FOR selinux ON FUNCTION "+routine+" IS 'D'")
	}
	assertUnchanged := sepgsqlGenerationNoEffects(e)
	calls := 0
	previous := actor.Ctx
	actor.Ctx = workadmission.With(previous, func(_ context.Context, class workadmission.Class) error {
		if class == workadmission.WorkJSONStripNulls {
			calls++
		}
		return nil
	})
	defer func() { actor.Ctx = previous }()
	expression := "CASE WHEN payload IS NULL THEN generation_probe(-3) ELSE json_array_length(payload) END"
	if command == "create" {
		expression = "CASE WHEN payload IS NULL THEN json_array_length(json_strip_nulls('[1,2,3]'::json)) ELSE json_array_length(payload) END"
	}
	query := "CREATE TABLE generation_denied(payload json,g integer GENERATED ALWAYS AS(" + expression + ") STORED)"
	if command == "alter" {
		query = "ALTER TABLE generation_denied ALTER COLUMN g SET EXPRESSION AS (" + expression + ")"
	} else if command == "insert" {
		query = "INSERT INTO generation_denied(payload) VALUES(NULL)"
	}
	message := "permission denied for function " + routineName
	if denial == "mac" {
		message = "SELinux: security policy violation"
	}
	mark := len(audit.snapshot())
	enginetest.RoleError(t, e, actor, query, "42501", message, "")
	assertUnchanged(t)
	if calls != 0 {
		t.Fatal("denied initialization entered the actual strip kernel", calls)
	}
	// Detached host events are delivered by the real operation's Done, not
	// StatementScope.Finish. Complete this owned operation/session exactly once.
	endActor()
	denied := false
	for _, event := range audit.snapshot()[mark:] {
		if len(event.Denied) != 0 {
			if denial != "mac" || event.Subject != "A" || event.Target != "D" || event.Class != "db_procedure" || !reflect.DeepEqual(event.Denied, []string{"execute"}) {
				t.Fatal("initialization fabricated a denial", event)
			}
			denied = true
		}
	}
	if denied != (denial == "mac") {
		t.Fatal("ordinary SQL denial acquired MAC evidence or true MAC denial omitted it")
	}
}

// OID reservation is not transactional; the independently expected DDL atomicity
// here concerns published catalog/labels, rows, physical roots and generators.
func sepgsqlGenerationNoEffects(e *core.Engine) func(*testing.T) {
	state := e.CatalogRead().State()
	rows, backing := cloneGeneratedRepairStorage(e.Storage.Relations), maps.Clone(e.Storage.Relations)
	indexes, sequences := maps.Clone(e.Storage.IndexRoots), maps.Clone(e.Storage.Sequences)
	generators := make(map[ids.ObjectAddress]any, len(sequences))
	for address, generator := range sequences {
		generators[address] = *generator
	}
	return func(t *testing.T) {
		t.Helper()
		if e.CatalogRead().State() != state || !reflect.DeepEqual(rows, e.Storage.Relations) || !maps.Equal(backing, e.Storage.Relations) || !maps.Equal(indexes, e.Storage.IndexRoots) || !maps.Equal(sequences, e.Storage.Sequences) {
			t.Fatal("failed generation definition published catalog/labels/rows/indexes")
		}
		for address, generator := range sequences {
			if !reflect.DeepEqual(generators[address], *generator) {
				t.Fatal("failed generation definition changed generator state")
			}
		}
	}
}

func sepgsqlGenerationPause(t *testing.T, command, change string) {
	t.Helper()
	e, _ := sepgsqlGenerationEngine(t)
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE SEQUENCE generation_pause_guard START 29")
	if command == "alter" {
		enginetest.SessionSQL(t, s, "CREATE TABLE generation_paused(payload json,g integer GENERATED ALWAYS AS(json_array_length(payload)) STORED)")
		enginetest.SessionSQL(t, s, "INSERT INTO generation_paused(payload) VALUES('[1,2]')")
		enginetest.SessionSQL(t, s, "CREATE INDEX generation_paused_index ON generation_paused(g)")
	}
	pause := util.NewExecutionPause("json.array_length")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	calls := 0
	request := workadmission.With(util.WithExecutionPause(ctx, pause), func(_ context.Context, class workadmission.Class) error {
		if class == workadmission.WorkJSONStripNulls {
			calls++
		}
		return nil
	})
	op, err := s.Acquire(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	expression := "CASE WHEN payload IS NULL THEN json_array_length(json_strip_nulls('[1,2,3]'::json)) ELSE json_array_length(payload) END"
	query := "CREATE TABLE generation_paused(payload json,g integer GENERATED ALWAYS AS(" + expression + ") STORED)"
	if command == "alter" {
		query = "ALTER TABLE generation_paused ALTER COLUMN g SET EXPRESSION AS (" + expression + ")"
	}
	raw := enginetest.Statement(t, query)
	result := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		scope, failure := e.StartStatement(op)
		if failure == nil {
			_, failure = scope.Execute(raw, 0, nil)
			failure = scope.Finish(failure)
		}
		result <- failure
		close(finished)
	}()
	t.Cleanup(func() { op.Cancel(context.Canceled); pause.Resume(); <-finished })
	if err := pause.Wait(ctx); err != nil {
		t.Fatal("generation never entered actual constant callback", err)
	}
	completedStripWork := calls
	if completedStripWork == 0 {
		t.Fatal("downstream pause preceded completion of actual strip work")
	}
	func() {
		if !e.ReplacementMu.TryLock() {
			t.Fatal("generation callback retained replacement reservation")
		}
		defer e.ReplacementMu.Unlock()
		if !e.Mu.TryLock() {
			t.Fatal("generation callback retained engine mutex")
		}
		defer e.Mu.Unlock()
		sepgsqlGenerationLiveReceipts(t, e, sepgsqlRepairScope(t, e))
	}()
	mutations := map[string]string{
		"catalog":    "CREATE TABLE generation_peer(v integer)",
		"role":       "CREATE ROLE generation_peer",
		"descriptor": "ALTER FUNCTION json_array_length(json) RENAME TO generation_json_length",
		"label":      "SECURITY LABEL FOR selinux ON FUNCTION json_array_length(json) IS 'D'",
	}
	if query := mutations[change]; query != "" {
		enginetest.Run(t, e, query)
	}
	assertUnchanged := sepgsqlGenerationNoEffects(e)
	cause := errors.New("generation constant child cancellation")
	child := op.Context()
	if change == "cancel" {
		op.Cancel(cause)
	} else if change == "session_end" {
		s.RequestEnd(session.CleanClose)
	} else {
		pause.Resume()
	}
	err = <-result
	<-finished
	op.Done()
	if change != "healthy" && calls != completedStripWork {
		t.Fatal("vetoed generation definition restarted completed strip traversal", completedStripWork, calls)
	}
	if change == "healthy" {
		if err != nil {
			t.Fatal("healthy unlocked generation definition", err)
		}
		enginetest.SessionSQL(t, s, "INSERT INTO generation_paused(payload) VALUES(NULL)")
		sepgsqlGenerationRows(t, s, "SELECT g FROM generation_paused WHERE payload IS NULL", [][]driver.Value{{int64(3)}})
	} else {
		if err == nil {
			t.Fatal("stale or canceled generation definition published", change)
		}
		if change == "cancel" && (err != context.Canceled || context.Cause(child) != cause || ctx.Err() != nil) {
			t.Fatal("generation lost exact child cancellation cause", err, context.Cause(child))
		}
		assertUnchanged(t)
	}
}

func sepgsqlGenerationRows(t *testing.T, s *session.Session, query string, want [][]driver.Value) {
	t.Helper()
	if got := enginetest.Tuples(enginetest.SessionSQL(t, s, query)); !reflect.DeepEqual(got, want) {
		t.Fatal(query, got, want)
	}
}

func sepgsqlGenerationRejectBorrow(t *testing.T, invalid string) {
	t.Helper()
	e := sepgsqlInitializationEngine(t)
	op, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT abs(-3)")
	foreign := sepgsqlInitializationEngine(t)
	other, otherScope, _, _ := sepgsqlInitializationBinding(t, foreign, "SELECT abs(-3)")
	auth := scope.RoutineAuth
	h := core.DDLHost{E: e, Op: op}
	previousLease := op.Lease
	defer func() { op.Lease = previousLease }()
	switch invalid {
	case "missing_scope":
		auth.Statement = nil
	case "foreign_scope":
		auth.Statement = otherScope
	case "finished_scope":
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
	case "wrong_operation":
		h.Op = other
	case "wrong_lease":
		op.Lease = other.Lease
	case "wrong_read":
		evaluation.Catalog = foreign.CatalogRead()
	case "foreign_stage":
		h.CatalogStage = foreign.BeginCatalogStage(foreign.CatalogRead())
		defer foreign.DiscardCatalogStage(h.CatalogStage)
	}
	beforeAuth, beforeDependencies := scope.RoutineAuth, scope.RoutineDependencies
	assertUnchanged := sepgsqlGenerationNoEffects(e)
	e.Mu.Lock()
	_, err := h.SimplifyGenerationDefinitionLocked(auth, evaluation, project.Expressions[0])
	e.Mu.Unlock()
	if err == nil {
		t.Fatal("foreign/missing generation borrower admitted", invalid)
	}
	if !reflect.DeepEqual(scope.RoutineAuth, beforeAuth) || !reflect.DeepEqual(scope.RoutineDependencies, beforeDependencies) {
		t.Fatal("failed admission replaced borrowed statement authority")
	}
	assertUnchanged(t)
}

func sepgsqlGenerationSourceOrder(t *testing.T) {
	t.Helper()
	e, _ := sepgsqlGenerationEngine(t)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	enginetest.RoleExecute(t, e, op, "CREATE SEQUENCE generation_order_steps START 31")
	enginetest.RoleExecute(t, e, op, "CREATE FUNCTION generation_virtual_const() RETURNS integer LANGUAGE sql IMMUTABLE AS $$SELECT 3$$")
	originalContext := op.Ctx
	calls := 0
	op.Ctx = workadmission.With(originalContext, func(_ context.Context, class workadmission.Class) error {
		if class == workadmission.WorkJSONStripNulls {
			calls++
		}
		return nil
	})
	defer func() { op.Ctx = originalContext }()
	enginetest.RoleExecute(t, e, op, "CREATE TABLE generation_rowdependent(payload json,g integer GENERATED ALWAYS AS(CASE WHEN payload IS NULL THEN abs(-3) ELSE json_array_length(json_strip_nulls(payload)) END) STORED)")
	if calls != 0 {
		t.Fatal("generation metadata invoked row-dependent callback")
	}
	enginetest.RoleExecute(t, e, op, "INSERT INTO generation_rowdependent(payload) VALUES(NULL),('[1,2]')")
	if calls == 0 {
		t.Fatal("row-dependent callback fixture never executed its real kernel")
	}
	sepgsqlPhaseRows(t, e, op, "SELECT g FROM generation_rowdependent ORDER BY g", [][]driver.Value{{int64(2)}, {int64(3)}})
	// A dead volatile arm can be elided for STORED, but an unresolved volatile
	// provider remains nonimmutable. Neither metadata path consumes nextval.
	enginetest.RoleExecute(t, e, op, "CREATE TABLE generation_elided(payload json,g bigint GENERATED ALWAYS AS(CASE WHEN false THEN nextval('generation_order_steps') ELSE 3 END) STORED)")
	assertUnchanged := sepgsqlGenerationNoEffects(e)
	enginetest.RoleError(t, e, op, "CREATE TABLE generation_volatile(payload json,g bigint GENERATED ALWAYS AS(CASE WHEN payload IS NULL THEN nextval('generation_order_steps') ELSE 3 END) STORED)", "42P17", "generation expression is not immutable", "")
	assertUnchanged(t)
	for _, generator := range e.Storage.Sequences {
		if generator.Last != 31 || generator.Called {
			t.Fatal("definition planning invoked volatile sequence work")
		}
	}
	// Original reference validation precedes conditional elision.
	_, err := e.Execute(op, enginetest.Statement(t, "CREATE TABLE generation_missing(payload json,g integer GENERATED ALWAYS AS(CASE WHEN false THEN missing ELSE 3 END) STORED)"), 0, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42703" || diagnostic.Message != `column "missing" does not exist` {
		t.Fatal("generation source validation was elided", err)
	}
	assertUnchanged(t)
	_, err = e.Execute(op, enginetest.Statement(t, "CREATE TABLE generation_virtual(payload json,g integer GENERATED ALWAYS AS(CASE WHEN false THEN generation_virtual_const() ELSE 3 END) VIRTUAL)"), 0, nil)
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "generation expression uses user-defined function" || diagnostic.Detail != "Virtual generated columns that make use of user-defined functions are not yet supported." {
		t.Fatal("virtual security was checked after conditional source elision", err)
	}
	assertUnchanged(t)
	_, err = e.Execute(op, enginetest.Statement(t, "CREATE TABLE generation_coercion(g integer GENERATED ALWAYS AS(TRUE) STORED)"), 0, nil)
	enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42804", Message: `column "g" is of type integer but default expression is of type boolean`, Hint: "You will need to rewrite or cast the expression."})
	assertUnchanged(t)
}

func sepgsqlGenerationRestoredBorrow(t *testing.T, fail bool) {
	t.Helper()
	e := sepgsqlInitializationEngine(t)
	op, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT json_array_length(json_strip_nulls('[1,2,3]'::json))")
	beforeAuth, beforeDependencies := scope.RoutineAuth, scope.RoutineDependencies
	previousOperationContext := op.Ctx
	barrier := errors.New("actual generation definition callback failed")
	calls := 0
	op.Ctx = workadmission.With(op.Context(), func(_ context.Context, class workadmission.Class) error {
		if class == workadmission.WorkJSONStripNulls {
			calls++
			if fail {
				return barrier
			}
		}
		return nil
	})
	defer func() { op.Ctx = previousOperationContext }()
	evaluation.Context = op.Context()
	evaluation.RoutineAuthority = &beforeAuth
	evaluation.RoutineRecipe = true
	evaluation.RoutineChecks = beforeDependencies
	previousHost, previousContext := evaluation.Host, evaluation.Context
	var node expr.Expr
	var err error
	func() {
		e.Mu.Lock()
		defer e.Mu.Unlock()
		node, err = (core.DDLHost{E: e, Op: op}).SimplifyGenerationDefinitionLocked(beforeAuth, evaluation, project.Expressions[0])
	}()
	if calls == 0 || (fail && (err != barrier || calls != 1)) || (!fail && err != nil) {
		t.Fatal("generation callback or primary error changed", calls, err)
	}
	if !fail {
		literal, ok := node.(plan.LiteralExpr)
		if !ok {
			t.Fatal("constant generation callback was not simplified")
		}
		value, err := literal.Value.Integer()
		if err != nil || value != 3 {
			t.Fatal("constant generation callback result", value, err)
		}
	}
	if !reflect.DeepEqual(scope.RoutineAuth, beforeAuth) || !reflect.DeepEqual(scope.RoutineDependencies, beforeDependencies) || evaluation.Host != previousHost || evaluation.Context != previousContext {
		t.Fatal("generation helper retained borrowed authority or runtime context")
	}
}
