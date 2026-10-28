package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func clusterExactProviderFence(t *testing.T) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE cluster_fence(id int)", "CREATE INDEX cluster_fence_a ON cluster_fence(id)", "CREATE INDEX cluster_fence_b ON cluster_fence(id)", "ALTER TABLE cluster_fence CLUSTER ON cluster_fence_a"} {
		enginetest.SessionSQL(t, s, q)
	}
	read := e.CatalogRead()
	parent := primaryKeyUnitRelation(t, e, "cluster_fence")
	a := primaryKeyUnitRelation(t, e, "cluster_fence_a")
	b := primaryKeyUnitRelation(t, e, "cluster_fence_b")
	stage := e.BeginCatalogStage(read)
	defer e.DiscardCatalogStage(stage)
	_, err := table.ClusterCandidate(core.DDLHost{E: e, CatalogStage: stage}, parent, parent, parser.AlterTableAction{Action: parser.ClusterOn, ClusterIndex: parser.Identifier{Text: b.Name()}})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := e.ValidateCatalogStage(t.Context(), stage)
	if err != nil {
		t.Fatal(err)
	}
	peer := e.BeginCatalogStage(read)
	defer e.DiscardCatalogStage(peer)
	changed := *b
	changed.ObjectRevision = b.Revision() + 1
	changed.Comment = "peer"
	if err = peer.Replace(&changed); err != nil {
		t.Fatal(err)
	}
	peerCandidate, err := e.ValidateCatalogStage(t.Context(), peer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, peerCandidate, core.PublicationToken{Epoch: read.Epoch()}); err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, candidate, core.PublicationToken{Epoch: read.Epoch()}); err == nil {
		t.Fatal("stale index provider admitted")
	}
	current := e.CatalogRead()
	if current.Get(parent.Address()) != parent || current.Get(a.Address()) != a || !a.SQLIndex.Clustered || current.MustRelation(b.Address()).SQLIndex.Clustered || current.MustRelation(b.Address()).Comment != "peer" {
		t.Fatal("rejected switch changed parent/index state")
	}
}

func clusterInheritedIdentity(t *testing.T) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE cluster_parent(id int)", "CREATE TABLE cluster_child() INHERITS(cluster_parent)", "CREATE INDEX cluster_pa ON cluster_parent(id)", "CREATE INDEX cluster_ch ON cluster_child(id)", "ALTER TABLE cluster_parent CLUSTER ON cluster_pa", "ALTER TABLE cluster_child CLUSTER ON cluster_ch"} {
		enginetest.SessionSQL(t, s, q)
	}
	parent := primaryKeyUnitRelation(t, e, "cluster_parent")
	child := primaryKeyUnitRelation(t, e, "cluster_child")
	pa := primaryKeyUnitRelation(t, e, "cluster_pa")
	ch := primaryKeyUnitRelation(t, e, "cluster_ch")
	storeParent, storeChild := e.Storage.Relations[parent.Address()], e.Storage.Relations[child.Address()]
	rootParent, rootChild := e.Storage.IndexRoots[pa.Address()], e.Storage.IndexRoots[ch.Address()]
	before := e.CatalogRead()
	enginetest.SessionSQL(t, s, "ALTER TABLE cluster_parent CLUSTER ON cluster_pa, SET WITHOUT OIDS")
	after := e.CatalogRead()
	if after.State() != before.State() || after.Get(parent.Address()) != parent || after.Get(child.Address()) != child || after.Get(pa.Address()) != pa || after.Get(ch.Address()) != ch || e.Storage.Relations[parent.Address()] != storeParent || e.Storage.Relations[child.Address()] != storeChild || e.Storage.IndexRoots[pa.Address()] != rootParent || e.Storage.IndexRoots[ch.Address()] != rootChild {
		t.Fatal("pure inherited no-op churned catalog/backing")
	}
	enginetest.SessionSQL(t, s, "ALTER TABLE cluster_parent SET WITHOUT CLUSTER")
	clear := e.CatalogRead()
	if clear.Get(child.Address()) != child || clear.Get(ch.Address()) != ch || !ch.SQLIndex.Clustered {
		t.Fatal("root clearing changed descendant")
	}
	enginetest.SessionSQL(t, s, "ALTER TABLE cluster_parent SET WITHOUT CLUSTER, SET WITHOUT OIDS")
	if e.CatalogRead().State() != clear.State() {
		t.Fatal("already-clear inherited list churned catalog")
	}
}
