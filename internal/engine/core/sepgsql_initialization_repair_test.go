package core_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"maps"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
)

// Private repair has no PostgreSQL-format counterpart. These fixtures compose
// genuine TEMP allocation, isolated replay, generated rows and index repair;
// SQL is setup/observation, not an alternative public compatibility scenario.
type sepgsqlRepairFixture struct {
	target, source *core.Engine
	owner, replay  *session.Session
	kept           *catalog.Relation
	imported       ids.ObjectAddress
	sourceHeap     *storage.Relation
	sourceRows     []storage.RowVersion
	route          string
	audits         atomic.Int64
}

func newSepgsqlRepairFixture(t *testing.T, route string) *sepgsqlRepairFixture {
	t.Helper()
	f := &sepgsqlRepairFixture{target: engine.NewWithSettings(nil, "", false), route: route}
	// Advance the real destination lifetime before allocating the collision.
	// The authentic private stage still starts at its own construction epoch.
	if _, err := f.target.ReplaceCatalog(t.Context(), f.target, f.target.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	runtime, err := sepgsql.Compile(sepgsql.SimulationConfig{AuditAllowed: true, Audit: func(sepgsql.AuditEvent) { f.audits.Add(1) }, Policy: sepgsql.Policy{
		ID: "remapped-repair", Version: "1", Contexts: []string{"A", "P"}, InitialContext: "A", UnlabeledContext: "P",
		Rules: []sepgsql.Rule{{Source: "A", Target: "*", Class: "*", Allow: []string{"*"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.target.InstallSepgsqlSimulation(runtime, false); err != nil {
		t.Fatal(err)
	}
	f.owner = enginetest.StartSession(t, f.target, nil)
	enginetest.SessionSQL(t, f.owner, "CREATE TEMP TABLE repair_kept(v integer)")
	enginetest.SessionSQL(t, f.owner, "INSERT INTO repair_kept VALUES(7)")
	for _, d := range f.target.CatalogRead().Descriptors() {
		if r, ok := d.(*catalog.Relation); ok && r.Name() == "repair_kept" && f.target.CatalogRead().MustNamespace(r.Namespace()).Temporary == f.owner.SessionToken {
			f.kept = r
		}
	}
	if f.kept == nil {
		t.Fatal("missing genuine retained TEMP heap")
	}
	enginetest.SessionSQL(t, f.owner, "CREATE TABLE repair_incumbent(v integer)")
	enginetest.SessionSQL(t, f.owner, "INSERT INTO repair_incumbent VALUES(11)")
	enginetest.SessionSQL(t, f.owner, "CREATE SEQUENCE repair_generator START 19")
	f.source = f.target.RestoreStage()
	f.replay, err = f.source.StartRestoreSession(t.Context(), f.source.RoleStore.Domain.Principal(10))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		f.replay.RequestEnd(session.CleanClose)
		if err := f.replay.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	})
	op := f.operation(t, t.Context())
	defer func() { core.OperationReplay(op).StagingEffects = false; op.Done() }()
	definition := "CREATE TABLE repair_import(payload json,g bigint GENERATED ALWAYS AS(CASE WHEN tableoid<>0::oid THEN tableoid::bigint+json_array_length(payload) ELSE 0 END) STORED)"
	if route == "index" {
		definition = "CREATE TABLE repair_import(payload json)"
	}
	for _, query := range []string{"CREATE SCHEMA repair_padding", "CREATE SCHEMA repair_toast_padding", definition} {
		enginetest.RoleExecute(t, f.source, op, query)
	}
	sepgsqlInitializationCommand(t, f.source, op, "INSERT INTO repair_import(payload) VALUES('[1,2]')", nil, func(scope *core.StatementScope) {
		if route == "generation" {
			sepgsqlAssertImportInitialization(t, f.source, scope)
			ref := enginetest.CatalogRef(t, f.source, f.source.CatalogRead(), "repair_import")
			sepgsqlGenerationRuntimeSites(t, scope, f.source.CatalogRead().MustRelation(ref.Address), 1)
		}
	})
	indexSQL := "CREATE INDEX repair_import_index ON repair_import(g)"
	if route == "index" {
		indexSQL = "CREATE INDEX repair_import_index ON repair_import USING brin((json_array_length(payload)))"
	}
	enginetest.RoleExecute(t, f.source, op, indexSQL)
	f.imported = enginetest.CatalogRef(t, f.source, f.source.CatalogRead(), "repair_import").Address
	if f.imported != f.kept.Address() {
		t.Fatal("fixture lacks genuine imported/retained TEMP collision", f.imported, f.kept.Address())
	}
	f.sourceHeap = f.source.Storage.Relations[f.imported]
	f.sourceRows = append([]storage.RowVersion(nil), f.sourceHeap.Versions...)
	for i := range f.sourceRows {
		f.sourceRows[i].Values = append([]sqltype.Value(nil), f.sourceRows[i].Values...)
	}
	return f
}

func (f *sepgsqlRepairFixture) operation(t *testing.T, ctx context.Context) *session.Operation {
	t.Helper()
	op, err := f.replay.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	core.OperationReplay(op).StagingEffects = true
	t.Cleanup(func() { core.OperationReplay(op).StagingEffects = false; op.Done() })
	return op
}

type sepgsqlRepairOrigin struct {
	pointer, engine, domain, policy, session uintptr
	epoch                                    uint64
}

func sepgsqlRepairOriginOf(e *core.Engine) sepgsqlRepairOrigin {
	origin := reflect.ValueOf(e).Elem().FieldByName("restoreInitializationOrigin")
	if origin.IsNil() {
		return sepgsqlRepairOrigin{}
	}
	fields := origin.Elem()
	return sepgsqlRepairOrigin{origin.Pointer(), fields.FieldByName("engine").Pointer(), fields.FieldByName("domain").Pointer(), fields.FieldByName("policy").Pointer(), fields.FieldByName("session").Pointer(), fields.FieldByName("epoch").Uint()}
}

func sepgsqlRepairScope(t *testing.T, e *core.Engine) *core.StatementScope {
	t.Helper()
	core.EngineSessionMuRef(e).Lock()
	sessions := make([]*session.Session, 0, len(core.EngineSessions(e)))
	for _, s := range core.EngineSessions(e) {
		sessions = append(sessions, s)
	}
	core.EngineSessionMuRef(e).Unlock()
	for _, s := range sessions {
		s.Mu.Lock()
		for op := range s.Operations {
			if scope := core.ActiveScopeOfLease(op.Lease); scope != nil {
				s.Mu.Unlock()
				return scope
			}
		}
		s.Mu.Unlock()
	}
	t.Fatal("real repair has no admitted active statement")
	return nil
}

func TestSepgsqlSimulationRemappedRepairInitialization(t *testing.T) {
	t.Run("inactive_multi_session", func(t *testing.T) {
		e := engine.New()
		enginetest.Run(t, e, "CREATE ROLE child_owner LOGIN")
		stage := e.RestoreStage()
		if sepgsqlRepairOriginOf(stage) != (sepgsqlRepairOrigin{}) {
			t.Fatal("inactive maintenance minted a simulation origin")
		}
		var first *session.Session
		for _, role := range []string{"postgres", "child_owner"} {
			op, done := enginetest.RestoreOperation(t, stage, role)
			t.Cleanup(done)
			if first == nil {
				first = op.Session
			} else if first == op.Session || first.SessionToken == op.Session.SessionToken {
				t.Fatal("private maintenance reused the first authenticated session")
			}
			if sepgsqlRepairOriginOf(stage) != (sepgsqlRepairOrigin{}) {
				t.Fatal("inactive session acquired simulation provenance")
			}
		}
	})
	for _, route := range []string{"generation", "index"} {
		for _, supplied := range []bool{false, true} {
			for _, cancelRepair := range []bool{false, true} {
				name := route + "/owned"
				if supplied {
					name = route + "/supplied"
				}
				if cancelRepair {
					name += "/cancel"
				}
				t.Run(name, func(t *testing.T) { sepgsqlRepairRun(t, route, supplied, cancelRepair) })
			}
		}
	}
	for _, invalid := range []string{"missing", "foreign", "epoch", "stage_epoch", "policy", "domain", "engine", "session", "staging", "foreign_operation"} {
		t.Run("reject/"+invalid, func(t *testing.T) { sepgsqlRepairReject(t, invalid) })
	}
}

func sepgsqlRepairRun(t *testing.T, route string, supplied, cancelRepair bool) {
	t.Helper()
	f := newSepgsqlRepairFixture(t, route)
	before := sepgsqlRepairSnapshot(t, f.target)
	pause := util.NewExecutionPause("json.array_length")
	parent, stop := context.WithTimeout(t.Context(), 10*time.Second)
	defer stop()
	ctx, cancel := context.WithCancelCause(util.WithExecutionPause(parent, pause))
	defer cancel(nil)
	var op *session.Operation
	stage := f.source
	original := sepgsqlRepairOriginOf(stage)
	type construction struct {
		stage *core.Engine
		epoch ids.CatalogEpoch
	}
	created := make(chan construction, 1)
	if supplied {
		op = f.operation(t, ctx)
	} else {
		factory := f.target.Factory
		f.target.Factory = func(definitions []settings.Definition, database string, track bool) (*core.Engine, error) {
			e, err := factory(definitions, database, track)
			if err == nil {
				created <- construction{e, e.Catalog.State.Epoch()}
			}
			return e, err
		}
		t.Cleanup(func() { f.target.Factory = factory })
	}
	audits := f.audits.Load()
	result := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		_, err := f.target.ReplaceCatalogInOperation(ctx, f.source, f.source.CatalogRead(), op)
		result <- err
		close(finished)
	}()
	t.Cleanup(func() {
		cancel(context.Canceled)
		pause.Resume()
		<-finished
	})
	if err := pause.Wait(ctx); err != nil {
		t.Fatal("repair failed before actual JSON work", err)
	}
	if !supplied {
		birth := <-created
		stage = birth.stage
		original = sepgsqlRepairOriginOf(stage)
		if original.epoch != uint64(birth.epoch) || original.pointer == 0 || original.policy != reflect.ValueOf(stage.Sepgsql).Pointer() {
			t.Fatal("owned repair rewrote construction lifetime", original, birth.epoch)
		}
	}
	var scope *core.StatementScope
	func() {
		stage.Mu.Lock()
		defer stage.Mu.Unlock()
		scope = sepgsqlRepairScope(t, stage)
		sepgsqlAssertImportInitialization(t, stage, scope)
		if original != sepgsqlRepairOriginOf(stage) || original.session != reflect.ValueOf(scope.Op.Session).Pointer() || original.epoch != uint64(stage.Catalog.State.Epoch()) || original.epoch == uint64(before.state.Epoch()+1) {
			t.Fatal("repair changed original origin or collapsed distinct stage/destination epochs")
		}
		calls := reflect.ValueOf(scope).Elem().FieldByName("routineInitializations").MapRange()
		for calls.Next() {
			ref := calls.Key().FieldByName("Site").FieldByName("Reference")
			if ref.FieldByName("Epoch").Uint() != original.epoch || ref.FieldByName("domain").Pointer() != original.domain {
				t.Fatal("repair initializer retained destination/foreign reference lifetime")
			}
		}
	}()
	if f.audits.Load() != audits {
		t.Fatal("private repair emitted client MAC audit")
	}
	if cancelRepair {
		cause := errors.New("authentic repair child canceled")
		child := scope.Op.Context()
		scope.Op.Cancel(cause)
		if err := <-result; err != context.Canceled || child.Err() != context.Canceled || context.Cause(child) != cause || ctx.Err() != nil {
			t.Fatal("repair lost exact child cause or canceled its source", err, child.Err(), context.Cause(child), ctx.Err())
		}
		before.assertUnchanged(t, f.target)
	} else {
		pause.Resume()
		if err := <-result; err != nil {
			t.Fatal("genuine active remapped repair", err)
		}
		f.assertInstalled(t, before.state.Epoch()+1)
	}
	<-finished
	if sepgsqlRepairOriginOf(stage) != original {
		t.Fatal("repair mutated or rebound original origin after completion")
	}
	if supplied && !core.OperationReplay(op).StagingEffects {
		t.Fatal("repair cleared caller-owned staging provenance")
	}
	if !supplied && core.OperationReplay(scope.Op).StagingEffects {
		t.Fatal("owned repair leaked staging provenance")
	}
	if reflect.ValueOf(scope).Elem().FieldByName("routineInitializations").Len() != 0 {
		t.Fatal("Finish retained command-local import receipts")
	}
	f.assertSource(t)
}

func sepgsqlRepairReject(t *testing.T, invalid string) {
	t.Helper()
	f := newSepgsqlRepairFixture(t, "generation")
	op := f.operation(t, t.Context())
	foreign, foreignOp := sepgsqlTrustedReplayOperation(t, f.target)
	if invalid == "staging" {
		core.OperationReplay(op).StagingEffects = false
	} else if invalid == "foreign_operation" {
		op = foreignOp
	} else {
		restore := core.SubstituteRestoreInitializationOrigin(f.source, foreign, invalid)
		defer restore()
	}
	before := sepgsqlRepairSnapshot(t, f.target)
	stageState := f.source.CatalogRead().State()
	_, err := f.target.ReplaceCatalogInOperation(t.Context(), f.source, f.source.CatalogRead(), op)
	if err == nil {
		t.Fatal("invalid repair provenance admitted", invalid)
	}
	if invalid != "foreign_operation" {
		message := "stuntgres: invalid trusted restore initialization session"
		if invalid == "stage_epoch" {
			// Changing the entire stage epoch first invalidates the genuine
			// retained generation plan, before repair's origin admission.
			message = "stuntgres: invalid generation plan owner"
		}
		if err.Error() != message {
			t.Fatal("repair did not retain exact source/origin rejection precedence", err)
		}
	}
	if f.source.CatalogRead().State() != stageState {
		t.Fatal("invalid original provenance published an evaluation candidate")
	}
	before.assertUnchanged(t, f.target)
	f.assertSource(t)
}

func (f *sepgsqlRepairFixture) assertSource(t *testing.T) {
	t.Helper()
	rows := f.sourceHeap.Versions
	if len(rows) != 1 || !reflect.DeepEqual(rows, f.sourceRows) {
		t.Fatal("repair changed original source heap values or membership")
	}
	if f.route == "generation" {
		g, err := rows[0].Values[1].Integer()
		if err != nil || g != int64(f.imported.ObjectID)+2 {
			t.Fatal("repair mutated original source tableoid+2", g, err)
		}
	}
}

func (f *sepgsqlRepairFixture) assertInstalled(t *testing.T, epoch ids.CatalogEpoch) {
	t.Helper()
	view := f.target.CatalogRead()
	imported := enginetest.CatalogRef(t, f.target, view, "repair_import")
	if view.Epoch() != epoch || imported.Epoch != epoch || imported.Domain() != f.target.Catalog.Domain || imported.Address == f.kept.Address() || view.Get(f.kept.Address()) != f.kept {
		t.Fatal("destination lost its own lifetime, collision remap or retained TEMP identity")
	}
	for query, expected := range map[string]int64{"SELECT v FROM repair_kept": 7, "SELECT json_array_length(payload) FROM repair_import": 2} {
		if rows := enginetest.Tuples(enginetest.SessionSQL(t, f.owner, query)); !reflect.DeepEqual(rows, [][]driver.Value{{expected}}) {
			t.Fatal("installed repair value", query, rows)
		}
	}
	if f.route == "generation" {
		if rows := enginetest.Tuples(enginetest.SessionSQL(t, f.owner, "SELECT g-tableoid::bigint FROM repair_import")); !reflect.DeepEqual(rows, [][]driver.Value{{int64(2)}}) {
			t.Fatal("destination generation retained source lifetime/value", rows)
		}
	}
	idx := enginetest.CatalogRef(t, f.target, view, "repair_import_index")
	root, heap := f.target.Storage.IndexRoots[idx.Address], f.target.Storage.Relations[imported.Address]
	if idx.Epoch != epoch || idx.Domain() != f.target.Catalog.Domain || root == nil || heap == nil || len(heap.Versions) != 1 {
		t.Fatal("destination omitted genuine heap/index publication")
	}
	if f.route == "generation" {
		if len(root.Entries) != 1 || len(root.Entries[0].Keys) != 1 || root.Entries[0].Row != heap.Versions[0].Row || root.Entries[0].Version != heap.Versions[0].Version {
			t.Fatal("B-tree repair lost exact heap membership")
		}
		key, err := root.Entries[0].Keys[0].Integer()
		if err != nil || key != int64(imported.Address.ObjectID)+2 {
			t.Fatal("B-tree repair retained original generated key", key, err)
		}
	} else {
		if root.Brin == nil || len(root.Brin.Ranges) != 1 || !root.Brin.Ranges[0].Summarized || len(root.Brin.Ranges[0].Keys) != 1 {
			t.Fatal("BRIN repair omitted genuine expression summary")
		}
		summary := root.Brin.Ranges[0].Keys[0]
		minimum, minErr := summary.Min.Integer()
		maximum, maxErr := summary.Max.Integer()
		if !summary.HasNonNull || summary.HasNull || minErr != nil || maxErr != nil || minimum != 2 || maximum != 2 {
			t.Fatal("BRIN expression extrema", summary, minErr, maxErr)
		}
	}
}

// Dump plus exact catalog/storage/frontier comparisons covers labels, rows,
// generators and allocator no-effects independently of the rejection error.
type sepgsqlRepairState struct {
	state  *catalog.State
	dump   string
	assert func(*testing.T, *core.Engine)
}

func sepgsqlRepairSnapshot(t *testing.T, e *core.Engine) sepgsqlRepairState {
	t.Helper()
	var dump bytes.Buffer
	if err := engine.Dump(t.Context(), e, &dump); err != nil {
		t.Fatal(err)
	}
	state, roles := e.CatalogRead().State(), e.RoleStore.Published
	rows, backing := cloneGeneratedRepairStorage(e.Storage.Relations), maps.Clone(e.Storage.Relations)
	roots, sequences := maps.Clone(e.Storage.IndexRoots), maps.Clone(e.Storage.Sequences)
	generators := make(map[ids.ObjectAddress]any, len(sequences))
	for address, generator := range sequences {
		generators[address] = *generator
	}
	oid, revision := e.Catalog.NextOID(), e.Catalog.NextRevision()
	row, version, frontier := e.Storage.RowFrontier(), e.Storage.VersionFrontier(), e.Visibility.CommitSeq()
	roleRevision, principal, membership := e.RoleStore.NextRevision(), e.RoleStore.NextPrincipal(), e.RoleStore.NextMembershipOID()
	return sepgsqlRepairState{state: state, dump: dump.String(), assert: func(t *testing.T, e *core.Engine) {
		if e.CatalogRead().State() != state || e.RoleStore.Published != roles || e.Catalog.NextOID() != oid || e.Catalog.NextRevision() != revision || e.Storage.RowFrontier() != row || e.Storage.VersionFrontier() != version || e.Visibility.CommitSeq() != frontier || !reflect.DeepEqual(rows, e.Storage.Relations) || !maps.Equal(backing, e.Storage.Relations) || !maps.Equal(roots, e.Storage.IndexRoots) || !maps.Equal(sequences, e.Storage.Sequences) {
			t.Fatal("rejected repair changed destination catalog/labels/storage/allocators")
		}
		if e.RoleStore.NextRevision() != roleRevision || e.RoleStore.NextPrincipal() != principal || e.RoleStore.NextMembershipOID() != membership {
			t.Fatal("rejected repair changed principal allocation frontiers")
		}
		for address, generator := range e.Storage.Sequences {
			if !reflect.DeepEqual(generators[address], *generator) {
				t.Fatal("rejected repair advanced destination generator")
			}
		}
	}}
}

func (before sepgsqlRepairState) assertUnchanged(t *testing.T, e *core.Engine) {
	t.Helper()
	before.assert(t, e)
	var after bytes.Buffer
	if err := engine.Dump(t.Context(), e, &after); err != nil || after.String() != before.dump {
		t.Fatal("rejected repair changed destination native image", err)
	}
}
