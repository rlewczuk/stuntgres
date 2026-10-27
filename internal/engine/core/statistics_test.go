package core_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func statisticsUnitEngine(t *testing.T) (*core.Engine, *catalog.Relation) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	syntax := &parser.CreateTable{Name: configName("statistics_input")}
	for _, name := range []string{"a", "b", "unselected", "c"} {
		syntax.Elements = append(syntax.Elements, parser.TableElement{Column: &parser.Column{Name: name, TypeName: parser.TypeName{Name: "integer"}}})
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	err = tablecmds.SchemaTableLocked(e, op, auth, stage, syntax, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, auth.State.CurrentUser, sqltype.InputEnvironment{}, nil)
	e.Mu.Unlock()
	if err != nil {
		e.DiscardCatalogStage(stage)
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	extensionUnitPublish(t, e, op, stage)
	view := e.CatalogRead()
	address := view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "statistics_input"})
	return e, view.MustRelation(address)
}
func statisticsUnitCreate() *parser.CreateStatistics {
	return &parser.CreateStatistics{Name: configName("statistics_definition"), Relation: configName("statistics_input"), Elements: []parser.Expr{parser.ColumnExpr{Name: configName("c")}, parser.ColumnExpr{Name: configName("a")}}, Kinds: []parser.Identifier{{Text: "mcv"}, {Text: "dependencies"}}}
}
func statisticsUnitStage(t *testing.T, e *core.Engine, op *session.Operation, s *parser.CreateStatistics) (*core.CatalogStage, error) {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return nil, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	err = ddl.StageCreateStatistics(core.DDLHost{E: e, Op: op, CatalogStage: stage}, auth, s, sqltype.InputEnvironment{})
	return stage, err
}
func statisticsUnitDescriptor(t *testing.T, stage *core.CatalogStage) *catalog.Statistics {
	t.Helper()
	for _, d := range stage.Descriptors() {
		if d, ok := d.(*catalog.Statistics); ok {
			return d
		}
	}
	t.Fatal("missing staged statistics")
	return nil
}
func TestStatisticsObjectImmutableDefinition(t *testing.T) {
	e, rel := statisticsUnitEngine(t)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	source := statisticsUnitCreate()
	stage, err := statisticsUnitStage(t, e, op, source)
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	d := statisticsUnitDescriptor(t, stage)
	if d.Selection.Len() != 2 || d.Selection.Key(0) != 1 || d.Selection.Key(1) != 4 || d.Selection.Kinds() != "fm" || d.Relation.Address != rel.Address() || d.Owner != op.RoleState().CurrentUser {
		t.Fatal("definition facts", d)
	}
	source.Elements[0] = parser.ColumnExpr{Name: configName("b")}
	source.Kinds[0].Text = "bogus"
	if d.Selection.Key(1) != 4 || d.Selection.Kinds() != "fm" {
		t.Fatal("parser buffers retained")
	}
	keys := []int32{4, 1}
	frozen, err := catalog.NewStatisticsSelection(keys, 0, "mff")
	if err != nil {
		t.Fatal(err)
	}
	keys[0] = 2
	if frozen.Key(1) != 4 || frozen.Kinds() != "fm" {
		t.Fatal("caller storage retained")
	}
	extensionUnitPublish(t, e, op, stage)
	captured := e.CatalogRead()
	provider := catalog.StatisticsProvider{}
	if err := provider.Validate(captured, d); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*catalog.Statistics){
		func(n *catalog.Statistics) { n.Owner = (&authorization.Domain{}).Principal(d.Owner.ID()) },
		func(n *catalog.Statistics) { n.ObjectRevision = 0 },
		func(n *catalog.Statistics) { n.ObjectAddress.ClassID = ids.RelationClassID },
		func(n *catalog.Statistics) { n.ObjectNamespace.ObjectID = 999999 },
		func(n *catalog.Statistics) { n.Relation = (&ids.Domain{}).Ref(d.Relation.Epoch, d.Relation.Address) },
		func(n *catalog.Statistics) { n.Relation.Epoch++ },
		func(n *catalog.Statistics) { n.Selection = catalog.StatisticsSelection{} },
	} {
		n := *d
		change(&n)
		state := catalog.CloneState(captured.State())
		state.SetDescriptor(n.Address(), &n)
		if err := provider.Validate(captured.WithState(state), &n); err == nil {
			t.Fatal("invalid provider authority admitted", n)
		}
	}
	if err := provider.Validate(captured, d); err != nil {
		t.Fatal("original snapshot corrupted", err)
	}
}
func TestStatisticsObjectPublicationCancellation(t *testing.T) {
	e, _ := statisticsUnitEngine(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, done, err := enginetest.Operation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	before := e.CatalogRead()
	stage, err := statisticsUnitStage(t, e, op, statisticsUnitCreate())
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	prepared, err := e.ValidateCatalogStage(ctx, stage)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err = e.PublishCatalogOperation(op, nil, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()}); !errors.Is(err, context.Canceled) || e.CatalogRead().State() != before.State() {
		t.Fatal("canceled statistics published", err)
	}
}
func TestStatisticsObjectPublicationRevisionFence(t *testing.T) {
	e, rel := statisticsUnitEngine(t)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	stage, err := statisticsUnitStage(t, e, op, statisticsUnitCreate())
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	prepared, err := e.ValidateCatalogStage(t.Context(), stage)
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	next := *rel
	next.ObjectRevision, err = e.Catalog.RevisionLocked()
	winner := e.BeginCatalogStage(e.Catalog.Current())
	if err == nil {
		err = winner.Replace(&next)
	}
	e.Mu.Unlock()
	if err != nil || next.Revision() <= rel.Revision() {
		t.Fatal("effective revision did not advance", err)
	}
	defer e.DiscardCatalogStage(winner)
	extensionUnitPublish(t, e, op, winner)
	incumbent := e.CatalogRead()
	if _, err = e.PublishCatalogOperation(op, nil, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()}); err == nil {
		t.Fatal("stale input statistics published")
	}
	if e.CatalogRead().State() != incumbent.State() {
		t.Fatal("failed publication replaced winner")
	}
}

func TestStatisticsObjectPublicationConcurrentSource(t *testing.T) {
	e, rel := statisticsUnitEngine(t)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	stage, err := statisticsUnitStage(t, e, op, statisticsUnitCreate())
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	original := e.Catalog.Providers[ids.StatisticExtClassID]
	var admission sync.Once
	e.Catalog.Providers[ids.StatisticExtClassID] = alterDropProvider{original, func() { admission.Do(func() { close(entered); <-release }) }}
	type statisticsPublicationResult struct {
		prepared *core.PreparedCatalogPublication
		err      error
	}
	finished := make(chan statisticsPublicationResult, 1)
	go func() {
		p, err := e.ValidateCatalogStage(t.Context(), stage)
		finished <- statisticsPublicationResult{p, err}
	}()
	joined := false
	defer func() {
		unblock()
		if !joined {
			<-finished
		}
	}()
	select {
	case <-entered:
	case result := <-finished:
		joined = true
		t.Fatal("statistics provider validation did not reach admission hook", result.err)
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	e.Mu.Lock()
	winner := e.BeginCatalogStage(e.Catalog.Current())
	next := *rel
	next.ObjectRevision, err = e.Catalog.RevisionLocked()
	if err == nil {
		err = winner.Replace(&next)
	}
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(winner)
	if err != nil {
		t.Fatal(err)
	}
	extensionUnitPublish(t, e, op, winner)
	incumbent := e.CatalogRead()
	unblock()
	result := <-finished
	joined = true
	if result.err == nil {
		_, result.err = e.PublishCatalogOperation(op, nil, result.prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
	}
	if result.err == nil || e.CatalogRead().State() != incumbent.State() {
		t.Fatal("concurrent source revision crossed statistics publication fence", result.err)
	}
}
