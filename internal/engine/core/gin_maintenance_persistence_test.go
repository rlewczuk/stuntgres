package core_test

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
	"testing"
	"time"
)

func ginCorePlacementIDs(t *testing.T, e *core.Engine, target *catalog.Relation, pending bool) []int64 {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	root := e.Storage.IndexRoots[target.Address()]
	store := e.Storage.Relations[target.SQLIndex.Parent]
	if root == nil || root.Gin == nil || store == nil {
		t.Fatal("missing split storage")
	}
	columns := root.Gin.Columns
	if pending {
		columns = root.Gin.Pending
	}
	seen := make(map[ids.VersionID]bool)
	var ids []int64
	if len(columns) != 0 {
		for _, token := range columns[0].Tokens {
			for _, posting := range token.Postings {
				if posting.Position < 0 || posting.Position >= len(store.Versions) {
					t.Fatal("invalid destination slot")
				}
				row := store.Versions[posting.Position]
				if row.Row != posting.Row || row.Version != posting.Version {
					t.Fatal("source identity retained")
				}
				if !seen[row.Version] {
					seen[row.Version] = true
					id, err := row.Values[0].Integer()
					if err != nil {
						t.Fatal(err)
					}
					ids = append(ids, id)
				}
			}
		}
	}
	slices.Sort(ids)
	return ids
}
func ginCoreCopyPayload(root *storage.GinRoot) *storage.GinRoot {
	next := *root
	next.Columns = slices.Clone(root.Columns)
	next.Pending = slices.Clone(root.Pending)
	for _, columns := range [2][]storage.GinColumn{next.Columns, next.Pending} {
		for i := range columns {
			columns[i].NonNull = slices.Clone(columns[i].NonNull)
			columns[i].Tokens = slices.Clone(columns[i].Tokens)
			for j := range columns[i].Tokens {
				columns[i].Tokens[j].Postings = slices.Clone(columns[i].Tokens[j].Postings)
			}
		}
	}
	return &next
}
func TestGINMaintenancePersistence(t *testing.T) {
	t.Run("materialized-actual-identity-split", ginCoreMaterializedPersistence)
	t.Run("retained-temp-authority", ginMaintenanceRetainedRejection)
	source := engine.New()
	actor := enginetest.StartSession(t, source, nil)
	for _, q := range []string{
		"CREATE SCHEMA gin_padding", "CREATE SCHEMA gin_toast_padding",
		"CREATE TABLE gin_persist(id integer,a integer[],v tsvector)",
		"INSERT INTO gin_persist VALUES(1,'{1,2,2}','cat:1 rat:2'),(2,'{}','')",
		"CREATE INDEX gin_persist_m ON gin_persist USING gin(a,v) WHERE id>0",
		"CREATE INDEX gin_persist_e ON gin_persist USING gin((a||ARRAY[9])) WHERE id>0",
		"CREATE INDEX gin_persist_empty ON gin_persist USING gin(a) WHERE id<0",
		"CREATE INDEX gin_persist_off ON gin_persist USING gin(v) WITH(fastupdate=off)",
		"INSERT INTO gin_persist VALUES(3,NULL,NULL),(4,'{1,2,2}','cat:1 rat:2')",
		"ALTER INDEX gin_persist_m SET(fastupdate=off)",
	} {
		enginetest.SessionSQL(t, actor, q)
	}
	originalIndex := ginCoreRelation(t, source, "gin_persist_m")
	originalParent := ginCoreRelation(t, source, "gin_persist")
	original := ginCorePending(t, source, originalIndex, 6, 4)
	before := ginCoreCopyPayload(original.Gin)
	if !slices.Equal(ginCorePlacementIDs(t, source, originalIndex, false), []int64{1, 2}) || !slices.Equal(ginCorePlacementIDs(t, source, originalIndex, true), []int64{3, 4}) {
		t.Fatal("source is not a genuine split")
	}
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), source, &dump); err != nil {
		t.Fatal(err)
	}
	target := engine.New()
	temporary := enginetest.StartSession(t, target, nil)
	enginetest.SessionSQL(t, temporary, "CREATE TEMP TABLE gin_retained(id integer,a integer[])")
	retained, ok := target.CatalogRead().LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: temporary.TemporaryNamespace, Name: "gin_retained"})
	if !ok || retained != originalParent.Address() {
		t.Fatal("fixture lacks real OID collision", retained, originalParent.Address())
	}
	enginetest.SessionSQL(t, temporary, "CREATE INDEX gin_retained_i ON gin_retained USING gin(a)")
	enginetest.SessionSQL(t, temporary, "INSERT INTO gin_retained VALUES(77,'{7}')")
	var temporaryIndex *catalog.Relation
	for _, descriptor := range target.CatalogRead().Descriptors() {
		if relation, ok := descriptor.(*catalog.Relation); ok && relation.SQLIndex != nil && relation.SQLIndex.Parent == retained {
			temporaryIndex = relation
		}
	}
	if temporaryIndex == nil {
		t.Fatal("missing retained TEMP root")
	}
	retainedRoot := target.Storage.IndexRoots[temporaryIndex.Address()]
	check := func(e *core.Engine) {
		t.Helper()
		idx := ginCoreRelation(t, e, "gin_persist_m")
		parent := ginCoreRelation(t, e, "gin_persist")
		root := ginCorePending(t, e, idx, 6, 4)
		if !slices.Equal(ginCorePlacementIDs(t, e, idx, false), []int64{1, 2}) || !slices.Equal(ginCorePlacementIDs(t, e, idx, true), []int64{3, 4}) {
			t.Fatal("restore collapsed main/pending")
		}
		if e != source && (root == original || root.Gin == original.Gin || parent.Columns[1].Usage.Type == originalParent.Columns[1].Usage.Type) {
			t.Fatal("source binding retained")
		}
		for _, columns := range [2][]storage.GinColumn{root.Gin.Columns, root.Gin.Pending} {
			for ordinal, column := range columns {
				bound, err := routine.BindClassSupport(e.CatalogRead(), idx.SQLIndex.Keys[ordinal].Class)
				if err != nil || column.Binding.Class != bound || column.Binding.Usage.Type != parent.Columns[ordinal+1].Usage.Type {
					t.Fatal("destination binding/support stale", err)
				}
			}
		}
		expression := ginCoreRelation(t, e, "gin_persist_e")
		if !slices.Equal(ginCorePlacementIDs(t, e, expression, true), []int64{3, 4}) {
			t.Fatal("expression split lost")
		}
		ginCorePending(t, e, ginCoreRelation(t, e, "gin_persist_empty"), 0, 0)
		ginCorePending(t, e, ginCoreRelation(t, e, "gin_persist_off"), 0, 4)
	}
	if err := engine.Restore(context.Background(), target, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check(target)
	if ginCoreRelation(t, target, "gin_persist").Address() == retained || target.Storage.IndexRoots[temporaryIndex.Address()] != retainedRoot {
		t.Fatal("remint damaged TEMP/source identity")
	}
	independent := engine.New()
	if err := engine.Restore(context.Background(), independent, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check(independent)
	if err := engine.Restore(context.Background(), source, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check(source)
	if !reflect.DeepEqual(original.Gin, before) {
		t.Fatal("same-destination restore mutated old split")
	}
	// Compatible layout and class presentation transports preserve both portions.
	enginetest.SessionSQL(t, temporary, "ALTER TABLE public.gin_persist ADD COLUMN extra text")
	check(target)
	enginetest.SessionSQL(t, temporary, "ALTER OPERATOR CLASS pg_catalog.array_ops USING gin RENAME TO gin_persist_array")
	check(target)
	enginetest.SessionSQL(t, temporary, "REINDEX INDEX public.gin_persist_m")
	rebuilt := ginCorePending(t, target, ginCoreRelation(t, target, "gin_persist_m"), 0, 4)
	if len(rebuilt.Gin.Columns[0].Tokens) == 0 {
		t.Fatal("REINDEX did not bulk build main")
	}
	enginetest.SessionSQL(t, temporary, "INSERT INTO public.gin_persist VALUES(5,'{2}','dog:1',NULL)")
	ginCorePending(t, target, ginCoreRelation(t, target, "gin_persist_m"), 0, 5)
	expression := ginCoreRelation(t, target, "gin_persist_e")
	if !slices.Equal(ginCorePlacementIDs(t, target, expression, true), []int64{3, 4, 5}) {
		t.Fatal("later DML did not retain independent pending placement")
	}
	// Real late-stage failures/cancellation retain every destination storage/root,
	// catalog identity and allocator frontier, not merely SQL match answers.
	state := target.CatalogRead().State()
	roots := maps.Clone(target.Storage.IndexRoots)
	stores := maps.Clone(target.Storage.Relations)
	rowFrontier, versionFrontier := target.Storage.RowFrontier(), target.Storage.VersionFrontier()
	marker := ""
	for _, line := range strings.Split(dump.String(), "\n") {
		if strings.HasPrefix(line, "-- stuntgres-gin-index-v1 ") {
			marker = line
			break
		}
	}
	if marker == "" {
		t.Fatal("missing native metadata")
	}
	invalidScripts := []string{dump.String() + "\n" + marker + "\n", strings.Replace(dump.String(), marker+"\n", "", 1), dump.String() + "\nALTER INDEX public.gin_persist_m SET(gin_pending_list_limit=63);"}
	record, found, decodeErr := index.DecodeGinIndex(strings.TrimPrefix(marker, "-- "))
	if decodeErr != nil || !found {
		t.Fatal(decodeErr)
	}
	for _, kind := range []string{"missing-target", "wrong-parent", "out-of-range", "duplicate-ordinal"} {
		bad := record
		switch kind {
		case "missing-target":
			bad.Name = "missing_gin_index"
		case "wrong-parent":
			bad.Parent = "missing_gin_parent"
		case "out-of-range":
			bad.Pending = []int{1000}
		case "duplicate-ordinal":
			bad.Pending = []int{3, 3}
		}
		replacement, err := index.EncodeGinIndex(bad)
		if err != nil {
			t.Fatal(err)
		}
		invalidScripts = append(invalidScripts, strings.Replace(dump.String(), marker+"\n", replacement, 1))
	}
	for _, line := range strings.Split(dump.String(), "\n") {
		if !strings.HasPrefix(line, "-- stuntgres-gin-index-v1 ") {
			continue
		}
		bad, found, err := index.DecodeGinIndex(strings.TrimPrefix(line, "-- "))
		if err != nil || !found {
			t.Fatal(err)
		}
		if bad.Name == "gin_persist_empty" {
			bad.Pending = []int{1}
			replacement, err := index.EncodeGinIndex(bad)
			if err != nil {
				t.Fatal(err)
			}
			invalidScripts = append(invalidScripts, strings.Replace(dump.String(), line+"\n", replacement, 1))
		}
	}
	for _, invalid := range invalidScripts {
		if err := engine.Restore(context.Background(), target, strings.NewReader(invalid)); err == nil {
			t.Fatal("late invalid split restore accepted")
		}
		if target.CatalogRead().State() != state || !maps.Equal(roots, target.Storage.IndexRoots) || !maps.Equal(stores, target.Storage.Relations) {
			t.Fatal("failed stage changed destination")
		}
	}
	pause := util.NewExecutionPause("gin.restore.prepared")
	defer pause.Resume()
	ctx, cancel := context.WithTimeout(util.WithExecutionPause(context.Background(), pause), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- engine.Restore(ctx, target, bytes.NewReader(dump.Bytes())) }()
	joined := false
	t.Cleanup(func() {
		cancel()
		pause.Resume()
		if !joined {
			<-done
		}
	})
	ginCoreAwaitPause(t, pause)
	cancel()
	pause.Resume()
	err := <-done
	joined = true
	if !errors.Is(err, context.Canceled) {
		t.Fatal("late stage cancellation", err)
	}
	if target.CatalogRead().State() != state || !maps.Equal(roots, target.Storage.IndexRoots) || !maps.Equal(stores, target.Storage.Relations) || target.Storage.RowFrontier() != rowFrontier || target.Storage.VersionFrontier() != versionFrontier {
		t.Fatal("canceled stage changed destination")
	}
	if !reflect.DeepEqual(original.Gin, before) || storage.GinPendingIncidences(retainedRoot.Gin) != 1 {
		t.Fatal("old/source/TEMP split mutated")
	}
	beforeReset := target.Storage.IndexRoots[temporaryIndex.Address()]
	if beforeReset == nil || beforeReset.Gin == nil {
		t.Fatal("presentation ALTER lost retained TEMP root before Reset")
	}
	beforeResetPayload := ginCoreCopyPayload(beforeReset.Gin)
	if err := engine.Reset(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	// Presentation ALTER above legitimately replaced the retained TEMP binding
	// wrapper. Reset must preserve its actual split and remint current authority,
	// not return the wrapper captured before that ALTER.
	view := target.CatalogRead()
	currentTemporary, ok := view.Relation(temporaryIndex.Address())
	if !ok {
		t.Fatal("reset lost retained TEMP descriptor")
	}
	currentParent, ok := view.Relation(currentTemporary.SQLIndex.Parent)
	if !ok {
		t.Fatal("reset lost retained TEMP parent")
	}
	resetRoot := ginCorePending(t, target, currentTemporary, 1, 1)
	if len(ginCorePlacementIDs(t, target, currentTemporary, false)) != 0 || !slices.Equal(ginCorePlacementIDs(t, target, currentTemporary, true), []int64{77}) {
		t.Fatal("reset lost independent TEMP pending placement")
	}
	bindings, err := index.BindGinIndex(view, currentParent, currentTemporary.SQLIndex)
	if err != nil {
		t.Fatal(err)
	}
	for _, columns := range [2][]storage.GinColumn{resetRoot.Gin.Columns, resetRoot.Gin.Pending} {
		for ordinal, column := range columns {
			if !bindings[ordinal].Same(column.Binding) {
				t.Fatal("reset retained stale TEMP binding authority")
			}
		}
	}
	if !reflect.DeepEqual(beforeReset.Gin, beforeResetPayload) {
		t.Fatal("reset mutated retained TEMP predecessor")
	}
}

func TestGINMaintenanceMetadataValidation(t *testing.T) {
	e, a, _, target := ginCoreFixture(t)
	enginetest.SessionSQL(t, a, "INSERT INTO gin_cohort VALUES(1,'{2}')")
	root := e.Storage.IndexRoots[target.Address()]
	store := e.Storage.Relations[target.SQLIndex.Parent]
	record, err := index.CaptureGinSplit(context.Background(), e.CatalogRead(), target, root, store, e.Visibility.CommitSeq())
	if err != nil {
		t.Fatal(err)
	}
	if record.Schema != "public" || record.Name != "gin_cohort_i" || record.ParentSchema != "public" || record.Parent != "gin_cohort" || !slices.Equal(record.Pending, []int{1}) {
		t.Fatal("independent split metadata", record)
	}
	line, err := index.EncodeGinIndex(record)
	if err != nil {
		t.Fatal(err)
	}
	decoded, found, err := index.DecodeGinIndex(strings.TrimPrefix(strings.TrimSpace(line), "-- "))
	if err != nil || !found || !reflect.DeepEqual(decoded, record) {
		t.Fatal("codec", decoded, err)
	}
	for _, line := range []string{"stuntgres-gin-index-v99 xyz", "stuntgres-gin-index-v1 !"} {
		if _, found, err := index.DecodeGinIndex(line); !found || err == nil {
			t.Fatal("recognized malformed metadata accepted", line, err)
		}
	}
	if _, found, err := index.DecodeGinRow("stuntgres-gin-row-v99 xyz"); !found || err == nil {
		t.Fatal("unsupported row version accepted", err)
	}
	invalid := *root
	invalid.Gin = ginCoreCopyPayload(root.Gin)
	invalid.Gin.Pending[0].Tokens[0].Postings[0].Version++
	if _, err := index.CaptureGinSplit(context.Background(), e.CatalogRead(), target, &invalid, store, e.Visibility.CommitSeq()); err == nil {
		t.Fatal("inconsistent source split accepted")
	}
	if _, err := index.RestoreGinSplit(context.Background(), e.CatalogRead(), target, root, map[index.RowIdentity]bool{{Row: 9, Version: 9}: true}, sqltype.InputEnvironment{}); err == nil {
		t.Fatal("nonmember ordinal accepted")
	}
}
