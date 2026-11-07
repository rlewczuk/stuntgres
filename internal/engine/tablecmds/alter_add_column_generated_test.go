package tablecmds_test

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func addGeneratedUnitAction(name, kind string, expression parser.Expr) parser.AlterTableAction {
	return parser.AlterTableAction{Action: parser.AddColumn, Definition: &parser.Column{Name: name, TypeName: parser.TypeName{Name: "int4"}, Constraints: []parser.ColumnConstraint{{Generation: &parser.GenerationDefinition{Mode: parser.IdentityAlways, Kind: kind, Expression: expression}}}}}
}
func addGeneratedUnitFixture(t *testing.T) (*core.Engine, *session.Operation, ids.ObjectRef) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	create := &parser.CreateTable{Name: alterGenerationName("g"), Elements: []parser.TableElement{{Column: &parser.Column{Name: "a", TypeName: parser.TypeName{Name: "int4"}}}}}
	if _, err := e.Execute(op, create, 0, nil); err != nil {
		t.Fatal(err)
	}
	alterGenerationPopulate(t, e, op)
	return e, op, enginetest.CatalogRef(t, e, e.CatalogRead(), "g")
}

func TestAlterAddColumnGeneratedCandidate(t *testing.T) {
	e, scope, auth, old := alterAddUnitScope(t)
	for _, kind := range []string{"stored", "virtual", ""} {
		action := addGeneratedUnitAction("b", kind, alterGenerationOperator("+", alterGenerationColumn("a"), alterGenerationLiteral(1)))
		next, added, err := tablecmds.AddColumnCandidate(e, scope, scope.Op, auth, old, action, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := catalog.GenerationVirtual
		if kind == "stored" {
			want = catalog.GenerationStored
		}
		generation, exists := next.ColumnMeta.Generations[4]
		if !exists || generation.Kind != want || generation.Expression == nil || generation.Planned == nil || generation.OID != 0 || len(generation.Dependencies) == 0 {
			t.Fatal("incomplete retained recipe", generation)
		}
		if added.Attribute != 4 || added.Expression != nil || !added.Value.IsNull() || added.Value.Type() != old.Columns[0].Usage.Type || len(next.ColumnMeta.Defaults) != 0 || next.ColumnMeta.HighWater != 4 {
			t.Fatal("generation entered defaults lane", added, next)
		}
		if len(old.ColumnMeta.Generations) != 0 || len(old.Columns) != 1 || old.ColumnMeta.HighWater != 3 || !reflect.DeepEqual(next.Attributes, []int32{1, 4}) {
			t.Fatal("candidate mutated source")
		}
		next.ColumnMeta.Generations[99] = generation
		if len(old.ColumnMeta.Generations) != 0 {
			t.Fatal("source generations map aliased")
		}
		skip := action
		skip.IfNotExists = true
		skip.Definition = &parser.Column{Name: "a", TypeName: parser.TypeName{Name: "nonexistent_type"}, Constraints: action.Definition.Constraints}
		same, ignored, err := tablecmds.AddColumnCandidate(e, scope, scope.Op, auth, old, skip, nil)
		if err != nil || same != old || ignored.Column != nil || len(old.ColumnMeta.Generations) != 0 {
			t.Fatal("skip cooked generation", same, ignored, err)
		}
	}
}

func TestAlterAddColumnGeneratedVirtualNoRewrite(t *testing.T) {
	for _, kind := range []string{"virtual", ""} {
		t.Run(kind, func(t *testing.T) {
			e, op, ref := addGeneratedUnitFixture(t)
			old := e.Catalog.State.MustRelation(ref.Address)
			store := e.Storage.Relations[ref.Address]
			snapshot := e.Catalog.State
			originalVersions, originalEnds := store.Versions, store.Ends
			originalHeaders, originalRetained, originalDiscarded := store.Headers, store.Retained, store.Discarded
			originalGeneration := store.Generation
			rowID, versionID := e.Storage.RowFrontier(), e.Storage.VersionFrontier()
			sourceStore := *store
			sourceStore.Versions = addGeneratedSnapshotRows(store.Versions)
			sourceStore.Retained = addGeneratedSnapshotHistory(store.Retained)
			sourceStore.Discarded = addGeneratedSnapshotHistory(store.Discarded)
			if store.Ends != nil {
				ends := *store.Ends
				ends.Rows, ends.Versions = maps.Clone(ends.Rows), maps.Clone(ends.Versions)
				sourceStore.Ends = &ends
			}
			if store.Headers != nil {
				headers := *store.Headers
				headers.Additional = addGeneratedSnapshotRows(headers.Additional)
				sourceStore.Headers = &headers
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(errors.New("unfinished virtual witness"))
			sourceWrites, sourceDeletes := scope.ColumnProjectionRoots(ref.Address)
			writesBefore := addGeneratedSnapshotRows(sourceWrites)
			var deletesBefore *visibility.PrivateDeletes
			if sourceDeletes != nil {
				deletesBefore = &visibility.PrivateDeletes{Rows: maps.Clone(sourceDeletes.Rows), Versions: maps.Clone(sourceDeletes.Versions)}
			}
			unchangedSource := func() {
				t.Helper()
				if !storage.SameVersionRoot(store.Versions, originalVersions) || store.Ends != originalEnds || store.Headers != originalHeaders || store.Retained != originalRetained || store.Discarded != originalDiscarded || !reflect.DeepEqual(store, &sourceStore) || !reflect.DeepEqual(sourceWrites, writesBefore) || !reflect.DeepEqual(sourceDeletes, deletesBefore) || snapshot.Get(ref.Address) != old || len(old.Columns) != 1 {
					t.Fatal("virtual projection mutated an original owner")
				}
				if e.Storage.RowFrontier() != rowID || e.Storage.VersionFrontier() != versionID {
					t.Fatal("virtual projection allocated row/version identities")
				}
			}
			action := addGeneratedUnitAction("b", kind, alterGenerationOperator("/", alterGenerationColumn("a"), alterGenerationLiteral(0)))
			if _, err := scope.Execute(alterGenerationStatement(action), 0, nil); err != nil {
				t.Fatal("virtual ADD eagerly evaluated", err)
			}
			next := scope.Catalog.Descriptors[ref.Address].(*catalog.Relation)
			shape := scope.Catalog.Storage[ref.Address]
			if next.ColumnMeta.Generations[2].Kind != catalog.GenerationVirtual || next.ColumnMeta.Generations[2].Expression == nil || next.ColumnMeta.Generations[2].Planned == nil || next.ColumnMeta.Generations[2].OID == 0 || e.Catalog.State != snapshot || e.Storage.Relations[ref.Address] != store {
				t.Fatal("private virtual candidate or definition missing")
			}
			if shape.Original != store || !storage.SameVersionRoot(shape.Root, originalVersions) || shape.Ends != originalEnds || shape.Generation != originalGeneration || shape.Relation != next || shape.Rewritten || shape.Replacement == nil || shape.Replacement == store || shape.Replacement.Generation != originalGeneration+1 {
				t.Fatal("virtual ADD lost ordinary projection fences", shape)
			}
			writes, deletes := scope.ColumnProjectionRoots(ref.Address)
			addGeneratedProjectedRows(t, originalVersions, shape.Replacement.Versions, next.Columns[1].Usage.Type)
			addGeneratedProjectedRows(t, sourceWrites, writes, next.Columns[1].Usage.Type)
			addGeneratedProjectedHistory(t, originalRetained, shape.Replacement.Retained, next.Columns[1].Usage.Type)
			addGeneratedProjectedHistory(t, originalDiscarded, shape.Replacement.Discarded, next.Columns[1].Usage.Type)
			if shape.Replacement.Ends != originalEnds {
				t.Fatal("virtual projection changed committed ends")
			}
			if originalHeaders == nil || len(originalHeaders.Additional) == 0 {
				if shape.Replacement.Headers != originalHeaders {
					t.Fatal("virtual projection replaced untouched header authority")
				}
			} else {
				headers := shape.Replacement.Headers
				if headers == nil || headers.Stamp != originalHeaders.Stamp || !reflect.DeepEqual(headers.Versions, originalHeaders.Versions) {
					t.Fatal("virtual projection changed exact-version headers")
				}
				addGeneratedProjectedRows(t, originalHeaders.Additional, headers.Additional, next.Columns[1].Usage.Type)
			}
			if deletes.Len() != sourceDeletes.Len() {
				t.Fatal("virtual projection lost delete candidates")
			}
			if sourceDeletes != nil {
				for _, source := range sourceDeletes.All() {
					matched := false
					for _, produced := range deletes.All() {
						if produced.Row == source.Row && produced.Version == source.Version {
							source.Storage = shape.Replacement
							if produced != source {
								t.Fatal("virtual projection changed deletion identity")
							}
							matched = true
							break
						}
					}
					if !matched {
						t.Fatal("virtual projection lost exact deletion")
					}
				}
			}
			if len(originalVersions)+len(sourceWrites) != 2 {
				t.Fatal("populated virtual witness lost effective rows")
			}
			unchangedSource()
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			installed := e.Catalog.State.MustRelation(ref.Address)
			if installed != next || installed.ColumnMeta.Generations[2].OID == 0 {
				t.Fatal("virtual recipe was not published")
			}
			published := e.Storage.Relations[ref.Address]
			if scope.Owner.Catalog != nil {
				published = scope.Owner.Catalog.Storage[ref.Address].Replacement
			}
			rows := append(slices.Clone(published.Versions), scope.Owner.Writes[ref.Address]...)
			expected := append(slices.Clone(shape.Replacement.Versions), writes...)
			if len(rows) != len(expected) {
				t.Fatal("virtual publication lost committed/private rows")
			}
			for _, source := range expected {
				matched := false
				for _, row := range rows {
					if row.Row == source.Row && row.Version == source.Version {
						if !reflect.DeepEqual(row.Values, source.Values) || len(row.Values) != 2 || !row.Values[1].IsNull() || row.Values[1].Type() != next.Columns[1].Usage.Type {
							t.Fatal("virtual publication changed projected physical values")
						}
						matched = true
						break
					}
				}
				if !matched {
					t.Fatal("virtual publication allocated/replaced a physical identity")
				}
			}
			unchangedSource()
		})
	}
}

// Snapshots own payload vectors; their source roots must survive publication.
func addGeneratedSnapshotRows(rows []storage.RowVersion) []storage.RowVersion {
	next := slices.Clone(rows)
	for i := range next {
		next[i].Values = slices.Clone(next[i].Values)
		next[i].IndexOutputs = maps.Clone(next[i].IndexOutputs)
		next[i].PrivateHistory = addGeneratedSnapshotHistory(next[i].PrivateHistory)
	}
	return next
}

func addGeneratedSnapshotHistory(root *storage.HeapPredecessor) *storage.HeapPredecessor {
	if root == nil {
		return nil
	}
	next := *root
	next.Version = addGeneratedSnapshotRows([]storage.RowVersion{root.Version})[0]
	next.Next = addGeneratedSnapshotHistory(root.Next)
	return &next
}

func addGeneratedProjectedRows(t *testing.T, source, produced []storage.RowVersion, typ *sqltype.Descriptor) {
	t.Helper()
	if len(produced) != len(source) {
		t.Fatal("projection changed lane cardinality", len(source), len(produced))
	}
	for _, original := range source {
		matched := false
		for _, row := range produced {
			if row.Row != original.Row || row.Version != original.Version {
				continue
			}
			if len(original.Values) != 1 || len(row.Values) != 2 || !reflect.DeepEqual(original.Values[0], row.Values[0]) || !row.Values[1].IsNull() || row.Values[1].Type() != typ {
				t.Fatal("virtual projection lost typed NULL/base payload", row)
			}
			addGeneratedProjectedHistory(t, original.PrivateHistory, row.PrivateHistory, typ)
			original.Values, row.Values = nil, nil
			original.PrivateHistory, row.PrivateHistory = nil, nil
			original.IndexOutputs, row.IndexOutputs = nil, nil
			if !reflect.DeepEqual(original, row) {
				t.Fatal("virtual projection changed physical row identity", original, row)
			}
			matched = true
			break
		}
		if !matched {
			t.Fatal("projection lost exact row/version")
		}
	}
}

func addGeneratedProjectedHistory(t *testing.T, source, produced *storage.HeapPredecessor, typ *sqltype.Descriptor) {
	t.Helper()
	for source != nil && produced != nil {
		if source.End != produced.End || source.EndFullXID != produced.EndFullXID {
			t.Fatal("virtual projection changed predecessor ends")
		}
		addGeneratedProjectedRows(t, []storage.RowVersion{source.Version}, []storage.RowVersion{produced.Version}, typ)
		source, produced = source.Next, produced.Next
	}
	if source != nil || produced != nil {
		t.Fatal("virtual projection changed predecessor cardinality")
	}
}

func TestAlterAddColumnGeneratedStoredRewrite(t *testing.T) {
	for _, expression := range []parser.Expr{alterGenerationLiteral(7), parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}} {
		e, op, ref := addGeneratedUnitFixture(t)
		old := e.Catalog.State.MustRelation(ref.Address)
		store := e.Storage.Relations[ref.Address]
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer scope.Finish(errors.New("unfinished stored witness"))
			action := addGeneratedUnitAction("b", "stored", expression)
			if _, err := scope.Execute(alterGenerationStatement(action), 0, nil); err != nil {
				t.Fatal(err)
			}
			next := scope.Catalog.Descriptors[ref.Address].(*catalog.Relation)
			shape := scope.Catalog.Storage[ref.Address]
			if shape.Replacement == store || shape.Replacement.Generation != store.Generation+1 || len(shape.Replacement.Versions) != 2 || next.ColumnMeta.Generations[2].OID == 0 {
				t.Fatal("constant/NULL stored ADD lacked genuine rewrite", shape)
			}
			for _, row := range shape.Replacement.Versions {
				if len(row.Values) != 2 {
					t.Fatal(row)
				}
				if expression.(parser.LiteralExpr).Value.Kind == parser.NullValue {
					if !row.Values[1].IsNull() {
						t.Fatal(row)
					}
				} else {
					n, err := row.Values[1].Integer()
					if err != nil || n != 7 {
						t.Fatal(n, err)
					}
				}
			}
			stored := table.StoredReplacements(next, []int32{2})
			if reason := table.AlterEventRewriteReason(nil, stored, false); reason != 2 {
				t.Fatal("stored generation event reason", reason)
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			if e.Storage.Relations[ref.Address].Generation != store.Generation+1 || len(old.Columns) != 1 || len(store.Versions[0].Values) != 1 {
				t.Fatal("stored rewrite publication/source ownership")
			}
		}()
	}
}
