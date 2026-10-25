package enginetests_test

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// Uses only current private engine seams. The real root, its maintained entries
// and a captured hash candidate must exist before asserting the corrected class
// identity. No proposed catalog/metadata API is needed at this checkpoint.
func TestPGOpclassJSONBHashIdentity(t *testing.T) {
	e := engine.New()
	for _, q := range []string{
		"CREATE TABLE opclass_jsonb_private(j JSONB)",
		`INSERT INTO opclass_jsonb_private VALUES('{"a":1}'),('{"a":1.0}'),('{"b":2}'),(NULL)`,
		"CREATE INDEX opclass_jsonb_private_h ON opclass_jsonb_private USING hash(j)",
	} {
		enginetest.Run(t, e, q)
	}
	index, old := hashRoot(t, e, "opclass_jsonb_private_h")
	if got := hashKeys(t, old); !reflect.DeepEqual(got, []string{`{"a": 1.0}`, `{"a": 1}`, `{"b": 2}`}) {
		t.Fatal("actual JSONB hash entries", got)
	}
	query, source, release := hashImplicitQuery(t, e, `SELECT j FROM opclass_jsonb_private WHERE j='{"a":1}'::jsonb`)
	stage := hashFilterStage(t, query, source, nil)
	if stage.HashCandidates == nil || stage.HashCandidates.Root != old || len(stage.HashCandidates.Occurrences) != 2 {
		stage.Close()
		release()
		t.Fatal("no actual JSONB hash probe")
	}
	if err := stage.Drain(context.Background()); err != nil || stage.Count() != 2 {
		stage.Close()
		release()
		t.Fatal("JSONB hash matches", stage.Count(), err)
	}
	stage.Close()
	release()
	enginetest.Run(t, e, `UPDATE opclass_jsonb_private SET j='{"c":3}' WHERE j='{"b":2}'::jsonb`)
	_, updated := hashRoot(t, e, "opclass_jsonb_private_h")
	if updated == old {
		t.Fatal("update did not publish real hash root")
	}
	metadata, _ := indexMaintenanceMetadata(t, e, e.CatalogRead(), "opclass_jsonb_private_h")
	if got := index.SQLIndex.Keys[0].Class.OID(); got != 10089 {
		t.Errorf("JSONB hash class OID got %d want released hash identity 10089 (not btree 10088)", got)
	}
	if !reflect.DeepEqual(metadata.IndClass, []ids.OID{10089}) {
		t.Errorf("actual index metadata class got %v want [10089]", metadata.IndClass)
	}
}

func hashRoot(t *testing.T, e *engine.Engine, name string) (*catalog.Relation, *storage.HashRoot) {
	t.Helper()
	index, carrier := btreeRoot(t, e, name)
	if index.SQLIndex.Method != catalog.HashMethod || carrier.Hash == nil || len(carrier.Entries) != 0 {
		t.Fatal("not a hash root", index, carrier)
	}
	return index, carrier.Hash
}

func hashKeys(t *testing.T, root *storage.HashRoot) []string {
	t.Helper()
	keys := []string{}
	versions := map[ids.VersionID]bool{}
	for _, bucket := range root.Buckets {
		for _, entry := range bucket {
			if entry.Key.IsNull() || entry.Row == 0 || entry.Version == 0 || versions[entry.Version] || entry.Position < 0 || entry.Position >= root.Prefix {
				t.Fatal("invalid/collapsed hash identity", entry)
			}
			versions[entry.Version] = true
			s, err := entry.Key.Type().Output(context.Background(), entry.Key)
			if err != nil {
				t.Fatal(err)
			}
			keys = append(keys, s)
		}
	}
	slices.Sort(keys)
	return keys
}

func hashImplicitQuery(t *testing.T, e *engine.Engine, q string) (*plan.Query, *exec.Source, func()) {
	t.Helper()
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(done)
	query, source, release := hashCaptureQuery(t, e, op, q)
	return query, source, func() {
		release()
		done()
	}
}

func hashFilterStage(t *testing.T, query *plan.Query, source *exec.Source, parameters []sqltype.Value) exec.Stage {
	t.Helper()
	filter := exec.HashQueryFilter(query.Root)
	if filter == nil {
		t.Fatal("fixture has no top-level filter")
	}
	state := &exec.State{Source: source, Parameters: parameters, HashFilter: filter, Frame: query.Root.(*plan.ProjectNode).Frame}
	if scope := engine.ScopeOf(source.LiveHost()); scope != nil {
		state.Input, state.Output = scope.Input, scope.Output
	}
	stage, err := exec.OpenNode(context.Background(), state, filter)
	if err != nil {
		t.Fatal(err)
	}
	return stage
}

func indexMaintenanceMetadata(t *testing.T, e *engine.Engine, catalog catalog.Read, name string) (index.Metadata, index.IndexesMetadata) {
	t.Helper()
	ref := enginetest.RoleTableRef(t, e, name)
	m, n, err := index.ProjectIndexMetadata(context.Background(), catalog, ref)
	if err != nil {
		t.Fatal(err)
	}
	return m, n
}

func btreeRoot(t *testing.T, e *engine.Engine, name string) (*catalog.Relation, *storage.IndexRoot) {
	t.Helper()
	ref := enginetest.RoleTableRef(t, e, name)
	idx := e.Catalog.State.MustRelation(ref.Address)
	root := e.Storage.IndexRoots[ref.Address]
	if idx.RelationKind != catalog.IndexRelation || idx.SQLIndex == nil || root == nil || e.Storage.Relations[ref.Address] != nil {
		t.Fatal("not a substantive index", idx, root)
	}
	if err := index.ValidateIndexDefinition(e.CatalogRead(), idx); err != nil {
		t.Fatal(err)
	}
	return idx, root
}

// Follow the existing queryJoinsFixture capture seam, but on the actual hash
// owner's engine. No public adapter or testing-only production switch is used.
func hashCaptureQuery(t *testing.T, e *engine.Engine, op *session.Operation, q string) (*plan.Query, *exec.Source, func()) {
	t.Helper()
	ast := enginetest.Statement(t, q)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scope.Describe(ast); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var bound analyze.Binding
	if err == nil {
		bound, err = engine.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
	}
	var source *exec.Source
	if err == nil {
		// Logical-stage fixtures supply cancellation contexts directly, but keep
		// the actual admission alive until every retained stage is closed.
		source, err = e.CaptureQuerySourcesForInvokerLocked(scope, bound.QuerySources, false, authorization.PrincipalRef{}, plan.RowMark{}, scope.QueryLockContext)
	}
	e.Mu.Unlock()
	if err == nil {
		err = exec.SelectQuerySources(op.Ctx, source)
	}
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		source.Close()
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(release)
	return bound.Query, source, release
}
