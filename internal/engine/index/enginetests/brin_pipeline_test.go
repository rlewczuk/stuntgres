package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

func brinInstalledRoot(t *testing.T, e *core.Engine) *storage.IndexRoot {
	t.Helper()
	view := e.CatalogRead()
	d, ok := view.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}, Name: "brin_pipeline_index"})
	if !ok {
		t.Fatal("missing published descriptor")
	}
	e.Mu.Lock()
	root := e.Storage.IndexRoots[d]
	e.Mu.Unlock()
	if root == nil || root.Brin == nil || root.Hash != nil || len(root.Entries) != 0 {
		t.Fatal("missing published BRIN summary payload", root)
	}
	return root
}

// SQL here is composed-fixture setup; assertions inspect actual root publication,
// not SQL acceptance (which belongs to the public reference scenarios).
func brinPublicationPipeline(t *testing.T) {
	e := engine.New()
	for _, q := range []string{
		"CREATE TABLE brin_pipeline(n INT,s INT)",
		"INSERT INTO brin_pipeline VALUES(1,50),(9,-1),(4,20),(NULL,9),(7,0)",
		"CREATE INDEX brin_pipeline_index ON brin_pipeline USING brin(n,s) WITH(pages_per_range=2,autosummarize=off)",
	} {
		enginetest.Run(t, e, q)
	}
	original := brinInstalledRoot(t, e)
	if original.Brin.EffectivePagesPerRange != 2 || len(original.Brin.Ranges) != 3 {
		t.Fatal(original)
	}
	enginetest.Run(t, e, "INSERT INTO brin_pipeline VALUES(99,0)")
	inserted := brinInstalledRoot(t, e)
	a, b := brinBounds(t, inserted.Brin.Ranges[2].Keys[0])
	if a != 7 || b != 99 {
		t.Fatal("real insertion did not extend its actual range", a, b)
	}
	a, b = brinBounds(t, original.Brin.Ranges[2].Keys[0])
	if a != 7 || b != 7 {
		t.Fatal("real insertion mutated retained root", a, b)
	}
	enginetest.Run(t, e, "ALTER INDEX brin_pipeline_index SET(pages_per_range=1,autosummarize=on)")
	if got := brinInstalledRoot(t, e); got != inserted || got.Brin.EffectivePagesPerRange != 2 {
		t.Fatal("metadata ALTER resized published root", got, inserted)
	}
	enginetest.Run(t, e, "REINDEX INDEX brin_pipeline_index")
	rebuilt := brinInstalledRoot(t, e)
	if rebuilt == inserted || rebuilt.Brin.EffectivePagesPerRange != 1 || len(rebuilt.Brin.Ranges) != 6 {
		t.Fatal("real REINDEX ignored configured intent", rebuilt)
	}
	enginetest.Run(t, e, "DELETE FROM brin_pipeline WHERE n=4")
	deleted := brinInstalledRoot(t, e)
	if deleted.Brin.Ranges[2].Keys[0].Min != rebuilt.Brin.Ranges[2].Keys[0].Min {
		t.Fatal("DELETE unnecessarily shrank conservative extrema")
	}
	enginetest.Run(t, e, "VACUUM brin_pipeline")
	vacuumed := brinInstalledRoot(t, e)
	if vacuumed.Brin.Prefix != 5 || len(vacuumed.Brin.Ranges) != 5 || len(rebuilt.Brin.Ranges) != 6 {
		t.Fatal("VACUUM failed destination slot reconstruction", vacuumed, rebuilt)
	}
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "BEGIN")
	enginetest.SessionSQL(t, s, "INSERT INTO brin_pipeline VALUES(-5,0)")
	if got := brinInstalledRoot(t, e); got != vacuumed {
		t.Fatal("private root leaked before commit", got, vacuumed)
	}
	enginetest.SessionSQL(t, s, "SAVEPOINT brin_private")
	enginetest.SessionSQL(t, s, "UPDATE brin_pipeline SET n=-9 WHERE n=9")
	enginetest.SessionSQL(t, s, "ROLLBACK TO brin_private")
	enginetest.SessionSQL(t, s, "ROLLBACK")
	if got := brinInstalledRoot(t, e); got != vacuumed {
		t.Fatal("rollback changed committed summaries", got, vacuumed)
	}
	enginetest.SessionSQL(t, s, "BEGIN")
	enginetest.SessionSQL(t, s, "INSERT INTO brin_pipeline VALUES(-5,0),(-4,0)")
	enginetest.SessionSQL(t, s, "SAVEPOINT brin_committed")
	enginetest.SessionSQL(t, s, "UPDATE brin_pipeline SET n=-9 WHERE n=9")
	enginetest.SessionSQL(t, s, "ROLLBACK TO brin_committed")
	if got := brinInstalledRoot(t, e); got != vacuumed {
		t.Fatal("private commit candidate escaped before commit", got)
	}
	enginetest.SessionSQL(t, s, "COMMIT")
	committed := brinInstalledRoot(t, e)
	if committed == vacuumed || committed.Brin.Prefix != 7 || len(committed.Brin.Ranges) != 7 || committed.Brin.Ranges[6].Summarized {
		t.Fatal("commit did not install private summaries", committed)
	}
	a, b = brinBounds(t, committed.Brin.Ranges[5].Keys[0])
	if a != -5 || b != -5 || !committed.Brin.Ranges[5].Summarized {
		t.Fatal("committed private value missing from installed summary", a, b)
	}
	a, b = brinBounds(t, committed.Brin.Ranges[1].Keys[0])
	if a != 9 || b != 9 || vacuumed.Brin.Prefix != 5 || len(vacuumed.Brin.Ranges) != 5 {
		t.Fatal("savepoint rollback or commit mutated retained summaries", a, b, vacuumed)
	}
}
