package core_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestSnapshotSyncUtilityExpressionCapture(t *testing.T) {
	e:=engine.New()
	enginetest.Run(t,e,"CREATE TABLE sync_expression(id INT)")
	s:=snapshotSyncScope(t,e,false,session.ReadCommitted)
	e.Mu.Lock();historical:=e.Visibility.CommitSeq();e.Mu.Unlock()
	enginetest.Run(t,e,"INSERT INTO sync_expression VALUES(1)")
	binder:=enginetest.ExpressionBinder(t,context.Background(),e,sqltype.InputEnvironment{},nil)
	node,err:=binder.Bind(enginetest.Statement(t,"SELECT pg_export_snapshot()").(*parser.Select).Targets[0],analyze.ExpressionPolicy{Context:analyze.ProjectionContext})
	if err!=nil{t.Fatal(err)}
	e.Mu.Lock();current:=e.Visibility.CommitSeq();s.NoteUtilityExpressionDemandLocked(e.Catalog.Current(),node);e.Mu.Unlock()
	if err:=s.AcquireUtilityExpressionSnapshot();err!=nil{t.Fatal(err)}
	roots,_,value,frontier:=core.ScopeExportCapture(s)
	if roots==nil||value==nil||frontier!=current||frontier<=historical{t.Fatal("expression cut was reconstructed for a historical row frontier",frontier,current,historical)}
	enginetest.Run(t,e,"INSERT INTO sync_expression VALUES(2)")
	token,err:=core.ScopeExportSnapshot(s);if err!=nil{t.Fatal(err)}
	_,saved,cut:=core.EngineExportCapture(e,token)
	if saved!=value||cut!=frontier{t.Fatal("utility expression export took a newer cut at Eval")}
}

func TestSnapshotSyncAlterCaptureDemand(t *testing.T) {
	for _,route:=range []string{"metadata","empty-check","ordinary-check","export-check","inherited-check","domain-preflight","nonexport-preflight"}{t.Run(route,func(t *testing.T){
		e:=engine.New();enginetest.Run(t,e,"CREATE TABLE sync_alter_structural(id INT)")
		if route!="empty-check"{enginetest.Run(t,e,"INSERT INTO sync_alter_structural VALUES(1)")}
		command:="ALTER TABLE sync_alter_structural ADD CHECK(id>0)"
		if route=="metadata"{command="ALTER TABLE sync_alter_structural ALTER COLUMN id SET STATISTICS 10"}
		if route=="empty-check"||route=="export-check"{command="ALTER TABLE sync_alter_structural ADD CHECK(pg_export_snapshot() IS NOT NULL)"}
		if route=="nonexport-preflight"{command="ALTER TABLE sync_alter_structural ADD COLUMN filled TEXT DEFAULT 'filled'"}
		if route=="domain-preflight"{
			enginetest.Run(t,e,"CREATE FUNCTION sync_structural_domain_check() RETURNS BOOL IMMUTABLE LANGUAGE SQL AS $$SELECT pg_export_snapshot() IS NOT NULL$$")
			enginetest.Run(t,e,"CREATE DOMAIN sync_structural_domain AS TEXT CHECK(sync_structural_domain_check())")
			command="ALTER TABLE sync_alter_structural ADD COLUMN filled TEXT DEFAULT (('filled'::sync_structural_domain)::TEXT)"
		}
		if route=="inherited-check"{
			enginetest.Run(t,e,"CREATE TABLE sync_structural_child() INHERITS(sync_alter_structural)")
			enginetest.Run(t,e,"INSERT INTO sync_structural_child VALUES(2),(3)")
			enginetest.Run(t,e,"CREATE FUNCTION sync_structural_child_check(x BIGINT) RETURNS BOOL STABLE LANGUAGE SQL AS $$SELECT true$$")
			enginetest.Run(t,e,"ALTER TABLE ONLY sync_structural_child ADD CHECK(sync_structural_child_check(id::BIGINT))")
			enginetest.Run(t,e,"CREATE OR REPLACE FUNCTION sync_structural_child_check(x BIGINT) RETURNS BOOL STABLE LANGUAGE SQL AS $$SELECT pg_export_snapshot() IS NOT NULL$$")
			command="ALTER TABLE sync_alter_structural ADD COLUMN filled INT DEFAULT 7, ALTER COLUMN id TYPE BIGINT"
		}
		op,done,err:=enginetest.Operation(e,context.Background());if err!=nil{t.Fatal(err)};defer done()
		s,err:=e.StartStatement(op);if err!=nil{t.Fatal(err)};defer s.Finish(errors.New("structural rollback"))
		if _,err:=s.Execute(enginetest.Statement(t,command),0,nil);err!=nil{t.Fatal(err)}
		roots,_,_,_:=core.ScopeExportCapture(s)
		exporting:=route=="export-check"||route=="domain-preflight"||route=="inherited-check"
		if (roots!=nil)!=exporting{t.Fatal("wrong typed ALTER root demand",route)}
		want:=0;if exporting{want=1};if route=="inherited-check"{want=2}
		if core.EngineExportCount(e)!=want{t.Fatal("wrong actual ALTER export execution count",route,core.EngineExportCount(e),want)}
	})}
}

// Observe real published roots, not a second implementation of the late selector.
// DEFAULT preflight can eliminate a rewrite while layout publication still needs
// an immutable Construction wrapper. Physical backing must remain exact.
func TestSnapshotSyncAlterIndexSelection(t *testing.T) {
	for _, partial := range []bool{false, true} {
		for _, indebted := range []bool{false, true} {
			name := "clean"
			if indebted {
				name = "indebted"
			}
			if partial {
				name = "partial/" + name
			}
			t.Run(name, func(t *testing.T) {
				e := engine.New()
				indexDDL := "CREATE INDEX sync_alter_key_index ON sync_alter_indexes(sync_alter_key(id))"
				if partial {
					indexDDL += " WHERE id>1"
				}
				for _, query := range []string{
					"CREATE TABLE sync_alter_indexes(id INT,v INT)",
					"INSERT INTO sync_alter_indexes VALUES(1,10),(2,20)",
					"CREATE FUNCTION sync_alter_key(x INT) RETURNS INT IMMUTABLE LANGUAGE SQL AS $$SELECT x$$",
					indexDDL,
					"CREATE INDEX sync_alter_v_index ON sync_alter_indexes(v)",
					"CREATE FUNCTION sync_alter_domain_check() RETURNS BOOL IMMUTABLE LANGUAGE SQL AS $$SELECT pg_export_snapshot() IS NOT NULL$$",
					"CREATE DOMAIN sync_alter_fill AS TEXT CHECK(sync_alter_domain_check())",
				} {
					enginetest.Run(t, e, query)
				}
				view := e.CatalogRead()
				parent := enginetest.CatalogRef(t, e, view, "sync_alter_indexes").Address
				key := enginetest.CatalogRef(t, e, view, "sync_alter_key_index").Address
				ordinary := enginetest.CatalogRef(t, e, view, "sync_alter_v_index").Address
				capture := func() (map[ids.ObjectAddress]*catalog.Relation, map[ids.ObjectAddress]*storage.IndexRoot) {
					e.Mu.Lock()
					defer e.Mu.Unlock()
					descriptors := make(map[ids.ObjectAddress]*catalog.Relation)
					roots := make(map[ids.ObjectAddress]*storage.IndexRoot)
					for address, root := range e.Storage.IndexRoots {
						descriptor, ok := e.Catalog.State.Relation(address)
						if ok && descriptor.SQLIndex != nil && descriptor.SQLIndex.Parent == parent {
							descriptors[address], roots[address] = descriptor, root
						}
					}
					return descriptors, roots
				}
				original, originalRoots := capture()
				if len(originalRoots) != 2 || originalRoots[key] == nil || originalRoots[ordinary] == nil {
					t.Fatal("expected the original two real index roots")
				}
				// Freeze values separately from backing identities: comparing two
				// aliases cannot detect in-place mutation of retained physical data.
				type retainedRoot struct {
					wrapper storage.IndexRoot
					payload storage.IndexRoot
					keys    [][]sqltype.Value
					history any
				}
				retain := func(roots map[ids.ObjectAddress]*storage.IndexRoot) map[ids.ObjectAddress]retainedRoot {
					saved := make(map[ids.ObjectAddress]retainedRoot)
					for address, root := range roots {
						if root == nil || root.Construction == nil || len(root.Entries) == 0 {
							t.Fatal("fixture lacks populated physical construction", address)
						}
						payload := *root
						payload.Construction = nil
						payload.Entries = slices.Clone(root.Entries)
						keys := make([][]sqltype.Value, len(root.Entries))
						for i := range payload.Entries {
							keys[i] = root.Entries[i].Keys
							payload.Entries[i].Keys = slices.Clone(root.Entries[i].Keys)
						}
						payload.Membership = maps.Clone(root.Membership)
						payload.RowEntries = maps.Clone(root.RowEntries)
						history := reflect.New(reflect.TypeOf(root.Construction).Elem())
						history.Elem().Set(reflect.ValueOf(root.Construction).Elem())
						saved[address] = retainedRoot{wrapper: *root, payload: payload, keys: keys, history: history.Interface()}
					}
					return saved
				}
				assertRetained := func(roots map[ids.ObjectAddress]*storage.IndexRoot, saved map[ids.ObjectAddress]retainedRoot) {
					t.Helper()
					for address, root := range roots {
						old := saved[address]
						payload := *root
						payload.Construction = nil
						if !reflect.DeepEqual(payload, old.payload) ||
							len(root.Entries) != len(old.wrapper.Entries) || cap(root.Entries) != cap(old.wrapper.Entries) ||
							&root.Entries[0] != &old.wrapper.Entries[0] ||
							reflect.ValueOf(root.Membership).Pointer() != reflect.ValueOf(old.wrapper.Membership).Pointer() ||
							reflect.ValueOf(root.RowEntries).Pointer() != reflect.ValueOf(old.wrapper.RowEntries).Pointer() ||
							root.Construction != old.wrapper.Construction || !reflect.DeepEqual(root.Construction, old.history) {
							t.Fatal("publication mutated retained wrapper, physical backing or construction history", address)
						}
						for i, entry := range root.Entries {
							if len(entry.Keys) != len(old.keys[i]) || cap(entry.Keys) != cap(old.keys[i]) ||
								&entry.Keys[0] != &old.keys[i][0] {
								t.Fatal("publication changed retained produced key backing", address)
							}
						}
					}
				}
				savedOriginal := retain(originalRoots)
				for address, root := range originalRoots {
					if !index.ConstructionMatches(root, original[address]) {
						t.Fatal("original complete build lacks clean construction", address)
					}
				}
				wantEntries := 2
				if partial {
					wantEntries = 1
					if len(originalRoots[key].Membership) != 1 {
						t.Fatal("partial fixture lacks actual membership backing")
					}
				}
				if len(originalRoots[key].Entries) != wantEntries {
					t.Fatal("incorrect original key membership")
				}
				if indebted {
					// This exact public no-rewrite poison must never execute.
					// Successful ADD is itself a zero-execution witness: any
					// invocation on either existing row raises division by zero.
					enginetest.Run(t, e, "CREATE OR REPLACE FUNCTION sync_alter_key(x INT) RETURNS INT IMMUTABLE LANGUAGE SQL AS $$SELECT 1/(x-x)$$")
				}
				before, beforeRoots := capture()
				beforeView := e.CatalogRead()
				heapLocator := beforeView.MustRelation(parent).EffectiveStorageLocator(beforeView)
				if heapLocator == 0 {
					t.Fatal("fixture lacks a physical heap incarnation")
				}
				if beforeRoots[key] != originalRoots[key] || beforeRoots[ordinary] != originalRoots[ordinary] ||
					index.ConstructionMatches(beforeRoots[key], before[key]) == indebted {
					t.Fatal("routine publication rebuilt or laundered initial physical construction")
				}
				enginetest.Run(t, e, "ALTER TABLE sync_alter_indexes ADD COLUMN filled TEXT DEFAULT (('filled'::sync_alter_fill)::TEXT)")
				afterFill, fillRoots := capture()
				fillView := e.CatalogRead()
				if fillView.MustRelation(parent).EffectiveStorageLocator(fillView) != heapLocator {
					t.Fatal("metadata-only fill replaced the physical heap incarnation")
				}
				for address, descriptor := range before {
					locator := descriptor.EffectiveStorageLocator(beforeView)
					if current := afterFill[address]; current == nil || locator == 0 || current.EffectiveStorageLocator(fillView) != locator {
						t.Fatal("metadata-only fill replaced an index incarnation", address)
					}
				}
				if len(fillRoots) != len(beforeRoots) || fillRoots[key] == nil || fillRoots[ordinary] == nil {
					t.Fatal("preflight-only fill lost selected index identities")
				}
				source, target := before[key], afterFill[key]
				old, next := beforeRoots[key], fillRoots[key]
				if source.SQLIndex.Keys[0].Derived == target.SQLIndex.Keys[0].Derived ||
					next == old || next.Construction == nil || next.Construction == old.Construction {
					t.Fatal("unrelated ADD-tail failed to remint only immutable provenance metadata")
				}
				// Inspect opaque owner history without copying/minting authority.
				// Behavioral matching alone cannot prove the exact indebted chain.
				mapping := reflect.ValueOf(next.Construction).Elem()
				if !mapping.FieldByName("recipe").IsNil() ||
					mapping.FieldByName("previous").Pointer() != reflect.ValueOf(old.Construction).Pointer() ||
					mapping.FieldByName("source").Pointer() != reflect.ValueOf(source).Pointer() ||
					mapping.FieldByName("target").Pointer() != reflect.ValueOf(target).Pointer() {
					t.Fatal("layout transport did not retain the exact source-to-target construction chain")
				}
				if index.ConstructionMatches(next, target) == indebted ||
					index.ConstructionMatches(old, target) || index.ConstructionMatches(next, source) ||
					index.ConstructionMatches(next, afterFill[ordinary]) {
					t.Fatal("layout transport invented debt, erased debt or accepted reversed/unrelated provenance")
				}
				for _, invalid := range []*storage.IndexRoot{nil, {}, {Construction: target.SQLIndex}, {Construction: old.Construction}} {
					if index.ConstructionMatches(invalid, target) {
						t.Fatal("layout transport accepted missing, forged or unmapped construction")
					}
				}
				oldPayload, newPayload := *old, *next
				oldPayload.Construction, newPayload.Construction = nil, nil
				if !reflect.DeepEqual(oldPayload, newPayload) ||
					len(next.Entries) != len(old.Entries) || cap(next.Entries) != cap(old.Entries) ||
					&next.Entries[0] != &old.Entries[0] ||
					reflect.ValueOf(next.Membership).Pointer() != reflect.ValueOf(old.Membership).Pointer() ||
					reflect.ValueOf(next.RowEntries).Pointer() != reflect.ValueOf(old.RowEntries).Pointer() {
					t.Fatal("preflight-only fill copied or changed physical entries, order or membership")
				}
				for i := range old.Entries {
					if len(next.Entries[i].Keys) != len(old.Entries[i].Keys) ||
						cap(next.Entries[i].Keys) != cap(old.Entries[i].Keys) ||
						&next.Entries[i].Keys[0] != &old.Entries[i].Keys[0] {
						t.Fatal("preflight-only fill copied produced key backing")
					}
				}
				if fillRoots[ordinary] != beforeRoots[ordinary] || !index.ConstructionMatches(fillRoots[ordinary], afterFill[ordinary]) {
					t.Fatal("preflight-only fill changed the exact ordinary sibling root")
				}
				assertRetained(originalRoots, savedOriginal)
				savedFill := retain(fillRoots)
				enginetest.Run(t, e, "CREATE OR REPLACE FUNCTION sync_alter_key(x INT) RETURNS INT IMMUTABLE LANGUAGE SQL AS $$SELECT x+100 WHERE pg_export_snapshot() IS NOT NULL$$")
				preRewrite, preRewriteRoots := capture()
				preRewriteView := e.CatalogRead()
				for address, root := range fillRoots {
					if preRewriteRoots[address] != root {
						t.Fatal("logical routine replacement rebuilt a physical root", address)
					}
				}
				if index.ConstructionMatches(preRewriteRoots[key], preRewrite[key]) {
					t.Fatal("routine replacement laundered physical construction debt")
				}
				enginetest.Run(t, e, "ALTER TABLE sync_alter_indexes ALTER COLUMN v TYPE BIGINT")
				afterRewrite, rewriteRoots := capture()
				rewriteView := e.CatalogRead()
				if locator := rewriteView.MustRelation(parent).EffectiveStorageLocator(rewriteView); locator == 0 || locator == heapLocator {
					t.Fatal("genuine rewrite retained the old physical heap incarnation")
				}
				if len(rewriteRoots) != len(beforeRoots) {
					t.Fatal("rewrite lost selected index identities")
				}
				for address, root := range beforeRoots {
					rebuilt := rewriteRoots[address]
					immediate := preRewriteRoots[address]
					if locator := afterRewrite[address].EffectiveStorageLocator(rewriteView); locator == 0 || locator == preRewrite[address].EffectiveStorageLocator(preRewriteView) {
						t.Fatal("genuine rewrite retained the old index incarnation", address)
					}
					if rebuilt == nil || rebuilt == root || rebuilt == immediate ||
						len(rebuilt.Entries) != len(immediate.Entries) ||
						&rebuilt.Entries[0] == &immediate.Entries[0] ||
						rebuilt.Construction == immediate.Construction ||
						!index.ConstructionMatches(rebuilt, afterRewrite[address]) {
						t.Fatal("physical rewrite omitted real backing/replacement construction", address)
					}
					construction := reflect.ValueOf(rebuilt.Construction).Elem()
					if construction.FieldByName("recipe").Pointer() != reflect.ValueOf(afterRewrite[address].SQLIndex).Pointer() ||
						!construction.FieldByName("previous").IsNil() ||
						!construction.FieldByName("source").IsNil() || !construction.FieldByName("target").IsNil() {
						t.Fatal("genuine rewrite transported old history instead of recording its exact complete-build recipe", address)
					}
					if partial && address == key &&
						(len(rebuilt.Membership) != 1 || reflect.ValueOf(rebuilt.Membership).Pointer() == reflect.ValueOf(immediate.Membership).Pointer()) {
						t.Fatal("physical rewrite retained old partial membership backing")
					}
				}
				for i, entry := range rewriteRoots[key].Entries {
					want := int64(i + 101)
					if partial {
						want = 102
					}
					got, err := entry.Keys[0].Integer()
					if err != nil || got != want {
						t.Fatal("genuine rewrite did not evaluate the current replacement key", got, want, err)
					}
				}
				for i, entry := range rewriteRoots[ordinary].Entries {
					got, err := entry.Keys[0].Integer()
					if want := int64((i + 1) * 10); err != nil || got != want {
						t.Fatal("genuine rewrite changed ordinary sibling key values", got, want, err)
					}
				}
				assertRetained(originalRoots, savedOriginal)
				assertRetained(fillRoots, savedFill)
				if !index.ConstructionMatches(originalRoots[key], original[key]) ||
					index.ConstructionMatches(fillRoots[key], afterFill[key]) == indebted {
					t.Fatal("genuine rewrite mutated historical clean/debt construction")
				}
			})
		}
	}
}
