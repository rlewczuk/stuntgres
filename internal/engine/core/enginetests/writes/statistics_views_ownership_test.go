package writes_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

// Capture through the actual statement/source owner, not a substitute value
// registry. EngineOpenRelationSourceLocked consumes the engine lock.
func statisticsViewsCaptured(t *testing.T, e *core.Engine, oid ids.OID) *exec.Source {
	t.Helper()
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	}()
	e.Mu.Lock()
	err = e.CaptureCommentSnapshotLocked(scope)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	read := e.CatalogRead()
	source := read.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid})
	e.Mu.Lock()
	captured, err := core.EngineOpenRelationSourceLocked(e, scope, read.Owner().Domain.Ref(read.Epoch(), source.Address()), source.Owner, plan.RowMark{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(captured.Close)
	return captured
}

func TestStatisticsViewsCapturedOwnership(t *testing.T) {
	e, s, ref := expressionCollectionFixture(t)
	enginetest.SessionSQL(t, s, "CREATE STATISTICS expr_joint ON b,(a+1) FROM expr_owner")
	enginetest.SessionSQL(t, s, "ANALYZE expr_owner")
	key := ndistinctKey(t, e, "expr_joint")
	metadata := e.Storage.RelationMetadata[ref.Address]
	root := metadata.Statistics
	fact := root.Extended[key]
	data := fact.MCVData
	items := &data.Items[0]
	dictionary := &data.Dictionaries[0][0]
	expression := fact.Expression(0)
	captured := statisticsViewsCaptured(t, e, pgcatalog.StatsExtOID)
	row := statisticsViewsFind(t, captured.Anonymous, "expr_joint")
	rowBacking := &row[0]
	values, frequencies := row[11], row[13]
	if captured.Ref.Domain() != key.Definition.Domain() || captured.Ref.Epoch != key.Definition.Epoch || values.Type() != e.CatalogRead().MustRelation(captured.Ref.Address).Columns[11].Usage.Type {
		t.Fatal("capture lost actual owner/generation")
	}
	// DML changes the real row root, but a statistics-only capture neither
	// collects nor aliases it. Refresh publishes a new combined fact.
	enginetest.SessionSQL(t, s, "INSERT INTO expr_owner VALUES(9,90,90)")
	rowRoot := e.Storage.Relations[ref.Address]
	enginetest.SessionSQL(t, s, "ANALYZE expr_owner")
	fresh := analyzeFacts(e, ref.Address).Extended[key]
	if fresh == fact || fresh.MCVData == data || fresh.Expression(0) == expression || fresh.MCVData.Rows != 5 || e.Storage.Relations[ref.Address] != rowRoot {
		t.Fatal("refresh did not replace only genuine statistics backing")
	}
	if metadata.Statistics != root || root.Extended[key] != fact || fact.MCVData != data || &data.Items[0] != items || &data.Dictionaries[0][0] != dictionary || fact.Expression(0) != expression || data.Rows != 4 {
		t.Fatal("accepted source backing mutated")
	}
	if &statisticsViewsFind(t, captured.Anonymous, "expr_joint")[0] != rowBacking || row[11] != values || row[13] != frequencies {
		t.Fatal("captured artifact replaced its immutable backing")
	}
	current := statisticsViewsCaptured(t, e, pgcatalog.StatsExtOID)
	newRow := statisticsViewsFind(t, current.Anonymous, "expr_joint")
	if &newRow[0] == rowBacking || newRow[11] == values {
		t.Fatal("new capture reused stale projected backing")
	}

	// A canceled real ANALYZE must leave both accepted backing and retained
	// capture untouched, not publish a partially built replacement.
	beforeReject := e.Storage.RelationMetadata[ref.Address]
	enginetest.SessionSQL(t, s, "INSERT INTO expr_owner VALUES(10,100,100)")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, done, err := enginetest.Operation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	boundary := &ndistinctBoundaryContext{Context: op.Ctx, action: cancel}
	op.Ctx = boundary
	_, err = e.Execute(op, enginetest.Statement(t, "ANALYZE expr_owner"), 0, nil)
	done()
	if !boundary.entered || !errors.Is(err, context.Canceled) {
		t.Fatal("real rejected publication boundary not exercised", boundary.entered, err)
	}
	if e.Storage.RelationMetadata[ref.Address] != beforeReject || analyzeFacts(e, ref.Address).Extended[key] != fresh || row[11] != values || &row[0] != rowBacking {
		t.Fatal("rejected publication changed accepted or captured backing")
	}

	// Definition replacement cannot adopt the old fact just because it has
	// the same display name or expression spelling.
	enginetest.SessionSQL(t, s, "DROP STATISTICS expr_joint")
	enginetest.SessionSQL(t, s, "CREATE STATISTICS expr_joint ON b,(a+1) FROM expr_owner")
	replacement := ndistinctKey(t, e, "expr_joint")
	if replacement.Definition == key.Definition || replacement.Definition.Address == key.Definition.Address {
		t.Fatal("fixture did not replace stable definition identity")
	}
	for _, candidate := range statisticsViewsCaptured(t, e, pgcatalog.StatsExtOID).Anonymous {
		name, _ := candidate[3].Text()
		if name == "expr_joint" {
			t.Fatal("new definition adopted retired data")
		}
	}
	unbuilt := statisticsViewsFind(t, statisticsViewsCaptured(t, e, pgcatalog.StatsExtExprsOID).Anonymous, "expr_joint")
	for _, value := range unbuilt[6:] {
		if !value.IsNull() {
			t.Fatal("unbuilt expression inherited retired summary")
		}
	}
	if row[11] != values || data.Rows != 4 || root.Extended[key] != fact {
		t.Fatal("retirement mutated retained source/capture")
	}
	enginetest.LockEmpty(t, e)
}

// Resolve pending statistics through the same runtime input as public reads,
// and inspect the actual journal/committed pointers rather than SQL equality.
func TestStatisticsViewsPrivateOwnership(t *testing.T) {
	e, s, ref := expressionCollectionFixture(t)
	key := ndistinctKey(t, e, "expr_a")
	before := analyzeFacts(e, ref.Address)
	old := before.Extended[key]
	enginetest.SessionSQL(t, s, "INSERT INTO expr_owner VALUES(9,90,90)")
	enginetest.SessionSQL(t, s, "BEGIN")
	enginetest.SessionSQL(t, s, "ANALYZE expr_owner")
	journal := s.Transactions.Catalog
	private := journal.Statistics[ref.Address].Extended[key]
	if private == nil || private == old || private.Expression(0) == old.Expression(0) || analyzeFacts(e, ref.Address) != before {
		t.Fatal("private fact did not have an isolated owner")
	}
	capture := func(s *session.Session) []sqltype.Value {
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
		address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatsExtExprsOID}
		in := pgcatalog.Input{Context: op.Context(), Owner: auth.Owner, Catalog: auth.Catalog, Roles: auth.Roles, Ref: auth.Ref(address), Source: auth.Catalog.MustRelation(address), Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth})}
		return statisticsViewsFind(t, statisticsViewsProduce(t, in), "expr_a")
	}
	own := capture(s)
	peer := enginetest.StartSession(t, e, nil)
	committed := capture(peer)
	if own[10].ArrayLen() != len(private.Expression(0).MCV) || committed[10].ArrayLen() != len(old.Expression(0).MCV) || own[10].Type() != committed[10].Type() {
		t.Fatal("pending/committed capture source mismatch")
	}
	ownFrequency, _ := own[11].ArrayElement(0).Float()
	peerFrequency, _ := committed[11].ArrayElement(0).Float()
	if ownFrequency == peerFrequency || ownFrequency != float64(float32(float64(private.Expression(0).MCV[0].Count)/float64(private.Expression(0).Rows))) || peerFrequency != float64(float32(float64(old.Expression(0).MCV[0].Count)/float64(old.Expression(0).Rows))) {
		t.Fatal("capture did not distinguish private and committed real populations")
	}
	enginetest.SessionSQL(t, s, "ROLLBACK")
	if analyzeFacts(e, ref.Address) != before || before.Extended[key] != old || private.Expression(0) == old.Expression(0) || own[10].ArrayLen() != len(private.Expression(0).MCV) {
		t.Fatal("rollback mutated committed/private detached artifact")
	}
	// A real different descriptor generation cannot be substituted by OID.
	foreign := engineGeneration(t)
	if own[10].ValidResult(foreign) || own[10].Type() == foreign.Type {
		t.Fatal("captured ANYARRAY admitted another engine generation")
	}
}

func engineGeneration(t *testing.T) sqltype.Usage {
	t.Helper()
	// Builtins supplies an independently owned generation without publishing
	// anything into either real engine's catalog.
	for _, descriptor := range sqltype.Builtins() {
		if descriptor.ID() == 2277 {
			return descriptor.Usage()
		}
	}
	t.Fatal("missing ANYARRAY generation")
	return sqltype.Usage{}
}
