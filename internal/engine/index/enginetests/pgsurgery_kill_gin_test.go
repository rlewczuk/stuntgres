package enginetests_test

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// SQL creates genuine provider, publication, retained/discarded and split roots.
// Assertions concern their private identity/backing and capture ordinal domain;
// public row and Restore outcomes belong to the shared reference scenario.
func TestPgsurgeryKillGinCapture(t *testing.T) {
	e := engine.New()
	for _, q := range []string{
		"CREATE EXTENSION pg_surgery",
		"CREATE TABLE kill_gin_capture(k integer,a integer[],v tsvector)",
		"INSERT INTO kill_gin_capture VALUES(10,'{2,2}','cat:1 rat:2'),(20,'{3}','doomed:1')",
		"CREATE INDEX kill_gin_capture_i ON kill_gin_capture USING gin(a,v) WITH(fastupdate=off) WHERE k>0",
	} {
		enginetest.Run(t, e, q)
	}
	parent := gistCopyRelation(t, e, "kill_gin_capture")
	store := e.Storage.Relations[parent.Address()]
	predecessor := killGinHead(t, store, 10)
	enginetest.Run(t, e, "UPDATE kill_gin_capture SET a='{8}',v='dog:1' WHERE k=10")
	successor := killGinHead(t, store, 10)
	if predecessor.Row != successor.Row || predecessor.Version == successor.Version {
		t.Fatal("fixture lacks genuine same-RowID distinct-version history")
	}
	killGinSurgery(t, e, "heap_force_freeze", predecessor)
	writer := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, writer, "BEGIN")
	enginetest.SessionSQL(t, writer, "INSERT INTO kill_gin_capture VALUES(30,'{9}','aborted:1')")
	writes := writer.Transactions.Writes[parent.Address()]
	if len(writes) != 1 {
		t.Fatal("fixture lacks actual private insertion", writes)
	}
	aborted := writes[0]
	enginetest.SessionSQL(t, writer, "ROLLBACK")
	killGinSurgery(t, e, "heap_force_freeze", aborted)
	enginetest.Run(t, e, "ALTER INDEX kill_gin_capture_i SET(fastupdate=on)")
	enginetest.Run(t, e, "INSERT INTO kill_gin_capture VALUES(40,'{4}','doomed:1'),(50,'{5}','pending:1')")
	pending := killGinHead(t, store, 50)
	killGinSurgery(t, e, "heap_force_freeze", pending)
	idx, root := ginInstalledRoot(t, e, "kill_gin_capture_i")
	if len(root.Entries) != 4 || root.MembershipLen() != 4 || len(root.Gin.Pending) != 2 || storage.GinPendingIncidences(root.Gin) == 0 || len(store.Headers.Additional) != 3 {
		t.Fatal("fixture lacks genuine main/pending membership and repaired Additional")
	}
	if root.IsMember(predecessor.Row, predecessor.Version) || root.IsMember(aborted.Row, aborted.Version) || !root.IsMember(pending.Row, pending.Version) {
		t.Fatal("fixture failed to distinguish retained, discarded and head membership")
	}
	for i, row := range []storage.RowVersion{predecessor, aborted, pending} {
		additional := store.Headers.Additional[i]
		if additional.Version != row.Version || &additional.Values[0] != &row.Values[0] {
			t.Fatal("repair copied payload backing or changed Additional order", i)
		}
	}
	oldHeaders := store.Headers
	physical := slices.Clone(store.Versions)
	headBacking := &store.Versions[0]
	oldRows := killGinCopyRows(store.Versions)
	additional := killGinCopyRows(oldHeaders.Additional)
	oldHeaderValues := make(map[ids.VersionID]storage.HeapHeader)
	for _, row := range append(slices.Clone(store.Versions), oldHeaders.Additional...) {
		oldHeaderValues[row.Version] = oldHeaders.Header(row.Version)
	}
	entries := gistCopyEntries(root)
	payload := ginCopyPayload(root.Gin)
	members, memberVersions := maps.Clone(root.Membership), maps.Clone(root.MembershipVersions)
	entriesBacking := &root.Entries[0]
	keysBacking := make([]*sqltype.Value, len(root.Entries))
	for i, entry := range root.Entries {
		keysBacking[i] = &entry.Keys[0]
	}
	ginBacking := root.Gin
	columnsBacking, pendingBacking := &root.Gin.Columns[0], &root.Gin.Pending[0]
	var tokens []*storage.GinPostingList
	var postings, nonnull []*storage.GinPosting
	for _, columns := range [][]storage.GinColumn{root.Gin.Columns, root.Gin.Pending} {
		for _, column := range columns {
			tokens = append(tokens, &column.Tokens[0])
			nonnull = append(nonnull, &column.NonNull[0])
			for _, list := range column.Tokens {
				postings = append(postings, &list.Postings[0])
			}
		}
	}
	killGinSurgery(t, e, "heap_force_kill", killGinHead(t, store, 20), killGinHead(t, store, 40))
	headers, generation := store.Headers, store.Generation
	if headers == oldHeaders || &headers.Additional[0] != &oldHeaders.Additional[0] {
		t.Fatal("kill did not publish immutable headers sharing Additional backing")
	}
	for version, header := range oldHeaderValues {
		if oldHeaders.Header(version) != header {
			t.Fatal("kill mutated a retained source header", version)
		}
	}
	capturedStore := *store
	headerValues := make(map[ids.VersionID]storage.HeapHeader, len(oldHeaderValues))
	for version := range oldHeaderValues {
		headerValues[version] = headers.Header(version)
	}
	visible := storage.VisibleHeapVersions(store, e.Visibility.CommitSeq())
	var versions []ids.VersionID
	for _, row := range visible {
		versions = append(versions, row.Version)
	}
	if !slices.Equal(versions, []ids.VersionID{successor.Version, pending.Version, predecessor.Version, aborted.Version}) {
		t.Fatal("export lost exact-version Additional ordering or deduplication", versions)
	}
	capture, err := index.CaptureGinSplit(context.Background(), e.CatalogRead(), idx, root, store, e.Visibility.CommitSeq())
	if err != nil || !slices.Equal(capture.Pending, []int{2}) {
		t.Fatal("GIN ordinals must name only the genuine pending survivor in relational export", capture, err)
	}
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	if e.Storage.Relations[parent.Address()] != store || e.Storage.IndexRoots[idx.Address()] != root || store.Headers != headers || store.Generation != generation || &headers.Additional[0] != &oldHeaders.Additional[0] {
		t.Fatal("capture replaced source heap, header, Additional or index roots")
	}
	if store.Ends != capturedStore.Ends || store.Retained != capturedStore.Retained || store.Discarded != capturedStore.Discarded || &store.Versions[0] != headBacking {
		t.Fatal("capture replaced source end/history/head backing")
	}
	for i, row := range store.Versions {
		if &row.Values[0] != &physical[i].Values[0] {
			t.Fatal("capture copied source physical payload backing", i)
		}
	}
	for version, header := range headerValues {
		if headers.Header(version) != header || oldHeaders.Header(version) != oldHeaderValues[version] {
			t.Fatal("capture mutated current or retained header values", version)
		}
	}
	if !reflect.DeepEqual(store.Versions, oldRows) || !reflect.DeepEqual(headers.Additional, additional) || !reflect.DeepEqual(root.Entries, entries) || !reflect.DeepEqual(root.Gin, payload) || !maps.Equal(root.Membership, members) || !maps.Equal(root.MembershipVersions, memberVersions) {
		t.Fatal("capture or kill mutated retained physical data, membership, keys or postings")
	}
	if &root.Entries[0] != entriesBacking || root.Gin != ginBacking || &root.Gin.Columns[0] != columnsBacking || &root.Gin.Pending[0] != pendingBacking {
		t.Fatal("capture replaced entry or main/pending backing")
	}
	for i, entry := range root.Entries {
		if &entry.Keys[0] != keysBacking[i] || !root.IsMember(entry.Row, entry.Version) {
			t.Fatal("capture changed produced-key backing or genuine membership", i)
		}
	}
	columnAt, postingAt := 0, 0
	for _, columns := range [][]storage.GinColumn{root.Gin.Columns, root.Gin.Pending} {
		for _, column := range columns {
			if &column.Tokens[0] != tokens[columnAt] || &column.NonNull[0] != nonnull[columnAt] {
				t.Fatal("capture copied typed dictionary/non-null backing", columnAt)
			}
			columnAt++
			for _, list := range column.Tokens {
				if &list.Postings[0] != postings[postingAt] {
					t.Fatal("capture copied genuine posting backing", postingAt)
				}
				postingAt++
			}
		}
	}
}

func killGinHead(t *testing.T, store *storage.Relation, key int64) storage.RowVersion {
	t.Helper()
	for _, row := range store.Versions {
		value, err := row.Values[0].Integer()
		if err != nil {
			t.Fatal(err)
		}
		if value == key {
			return row
		}
	}
	t.Fatal("missing genuine head", key)
	return storage.RowVersion{}
}

func killGinSurgery(t *testing.T, e *core.Engine, operation string, rows ...storage.RowVersion) {
	t.Helper()
	var tids []string
	for _, row := range rows {
		block, offset, err := storage.LogicalTID(row.Version)
		if err != nil {
			t.Fatal(err)
		}
		tids = append(tids, fmt.Sprintf("'(%d,%d)'::tid", block, offset))
	}
	enginetest.Run(t, e, "SELECT "+operation+"('kill_gin_capture'::regclass,ARRAY["+strings.Join(tids, ",")+"])")
}

func killGinCopyRows(rows []storage.RowVersion) []storage.RowVersion {
	copy := slices.Clone(rows)
	for i := range copy {
		copy[i].Values = slices.Clone(rows[i].Values)
	}
	return copy
}
