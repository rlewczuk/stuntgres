package engine

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"sync"
	"testing"
)

func TestUsingIndexBlockedCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			e, waiter, parent, index := usingIndexUnitFixture(t)
			holder := sessionStart(t, e, nil)
			tx := transactionBegin(t, holder)
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			if err := e.locks.acquire(context.Background(), holder.transactions.ticket, lockResource{ObjectRef: ref}, accessShare); err != nil {
				t.Fatal(err)
			}
			catalog, store, root := e.state, e.storage[parent.address], e.indexRoots[index.address]
			entries := slices.Clone(root.entries)
			for i := range entries {
				entries[i].keys = slices.Clone(entries[i].keys)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			var notices []sqlstate.Error
			go func() {
				op, err := waiter.Acquire(ctx)
				if err == nil {
					alter := usingIndexUnitAlter(true)
					if prepared {
						_, err = e.Describe(op, alter)
					}
					if err == nil {
						_, err = e.Execute(op, alter, 0, nil)
					}
					notices = op.DetachNotices()
					op.Done()
				}
				done <- err
			}()
			// Existing lock-manager seam observes actual blocked admission,
			// not a sleep or an already-canceled request.
			lockPending(t, e, 1)
			select {
			case err := <-done:
				t.Fatal("AX waiter completed before cancellation", err)
			default:
			}
			cancel()
			lockAwait(t, done, context.Canceled)
			lockPending(t, e, 0)
			if len(notices) != 0 {
				t.Fatal("blocked cancellation emitted attachment notices", notices)
			}
			if waiter.TransactionState() != TxIdle || e.state != catalog || e.storage[parent.address] != store || e.indexRoots[index.address] != root || !reflect.DeepEqual(root.entries, entries) || len(parent.uniqueConstraints) != 0 || index.sqlIndex.constraint != nil {
				t.Fatal("blocked cancellation leaked state or aborted connection")
			}
			transactionEnd(t, holder, tx, false)
			lockEmpty(t, e)
			usingIndexUnitExecute(t, e, waiter, usingIndexUnitAlter(true))
			attached := primaryKeyUnitRelation(t, e, "owned")
			if attached.address != index.address || !backingConstraintPresent(primaryKeyUnitRelation(t, e, "t"), attached) || e.storage[parent.address] != store || e.indexRoots[index.address] != root {
				t.Fatal("same-session retry lost original group/root")
			}
			if usingIndexUnitExecute(t, e, waiter, primaryKeyUnitInsert(3)).Count != 1 {
				t.Fatal("same-session producer remained canceled")
			}
			lockEmpty(t, e)
		})
	}
}

// Package-entry fixtures use independently built ASTs, not driver SQL. Catalog,
// providers, committed rows, baseline roots and transaction owners are real.
func usingIndexUnitExecute(t *testing.T, e *Engine, s *Session, statement parser.Statement) Result {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	result, err := e.Execute(op, statement, 0, nil)
	if err != nil || result.Rows != nil {
		t.Fatal(result, err)
	}
	return result
}
func usingIndexUnitTable(name string) *parser.CreateTable {
	table := primaryKeyUnitCreate(name, "unused")
	table.Elements = table.Elements[:3]
	return table
}
func usingIndexUnitSeed(t *testing.T, e *Engine, s *Session) (*relationDescriptor, *relationDescriptor) {
	t.Helper()
	usingIndexUnitExecute(t, e, s, usingIndexUnitTable("t"))
	for _, a := range []int64{1, 2} {
		if r := usingIndexUnitExecute(t, e, s, primaryKeyUnitInsert(a)); r.Count != 1 {
			t.Fatal(r)
		}
	}
	usingIndexUnitExecute(t, e, s, &parser.CreateIndex{Name: parser.Identifier{Text: "prebuilt"}, Table: primaryKeyUnitName("t"), Method: "btree", Unique: true, Keys: []parser.IndexKey{{Column: parser.Identifier{Text: "a"}}, {Column: parser.Identifier{Text: "b"}}}, Include: []parser.IndexKey{{Column: parser.Identifier{Text: "secret"}}}})
	usingIndexUnitExecute(t, e, s, &parser.CreateIndex{Name: parser.Identifier{Text: "sibling"}, Table: primaryKeyUnitName("t"), Method: "btree", Keys: []parser.IndexKey{{Column: parser.Identifier{Text: "b"}}}})
	return primaryKeyUnitRelation(t, e, "t"), primaryKeyUnitRelation(t, e, "prebuilt")
}
func usingIndexUnitFixture(t *testing.T) (*Engine, *Session, *relationDescriptor, *relationDescriptor) {
	t.Helper()
	e := New()
	s := sessionStart(t, e, nil)
	parent, index := usingIndexUnitSeed(t, e, s)
	return e, s, parent, index
}
func usingIndexUnitAlter(primary bool) *parser.AlterTable {
	return &parser.AlterTable{Name: primaryKeyUnitName("t"), Actions: []parser.AlterTableAction{{Action: parser.AddUsingIndex, Constraint: parser.Identifier{Text: "owned"}, UsingIndex: &parser.UsingIndexConstraint{Name: parser.Identifier{Text: "owned"}, Index: parser.Identifier{Text: "prebuilt"}, Primary: primary}}}}
}

func TestUsingIndexEligibility(t *testing.T) {
	e, s, parent, index := usingIndexUnitFixture(t)
	raw := &parser.UsingIndexConstraint{Pos: parser.Position{Character: 17}}
	attributes, err := usingIndexEligibility(parent, index, raw)
	if err != nil || !slices.Equal(attributes, parent.attributes[:2]) {
		t.Fatal(attributes, err)
	}
	// Invalid plus nonunique is a valid ready-invalid native descriptor. Its
	// earlier invalid-state diagnostic must not become a nonunique diagnosis.
	invalid := *index
	definition := *index.sqlIndex
	definition.valid, definition.unique = false, false
	invalid.sqlIndex = &definition
	if err := validateIndexDefinition(e.CatalogRead(), &invalid); err != nil {
		t.Fatal(err)
	}
	_, err = usingIndexEligibility(parent, &invalid, raw)
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "55000", Message: `index "prebuilt" is not valid`, Position: "17"})
	// Real associated input wins over its wrong-parent relationship.
	other := primaryKeyUnitCreate("other", "other_pk")
	usingIndexUnitExecute(t, e, s, other)
	owned := primaryKeyUnitRelation(t, e, "other_pk")
	_, err = usingIndexEligibility(parent, owned, raw)
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "55000", Message: `index "other_pk" is already associated with a constraint`, Position: "17"})
	// Compose independently bound, provider-valid expression and predicate
	// definitions to cover the public concurrent-partial setup boundary privately.
	a := parser.ColumnExpr{Name: primaryKeyUnitName("a")}
	one := parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}
	zero := parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 0}}
	usingIndexUnitExecute(t, e, s, &parser.CreateIndex{Name: parser.Identifier{Text: "expression"}, Table: primaryKeyUnitName("t"), Method: "btree", Unique: true, Concurrent: parser.Position{Character: 1}, Keys: []parser.IndexKey{{Expression: parser.OperatorExpr{Name: primaryKeyUnitName("+"), Args: []parser.Expr{a, one}}}}})
	usingIndexUnitExecute(t, e, s, &parser.CreateIndex{Name: parser.Identifier{Text: "partial"}, Table: primaryKeyUnitName("t"), Method: "btree", Keys: []parser.IndexKey{{Column: parser.Identifier{Text: "a"}}}, Predicate: parser.OperatorExpr{Name: primaryKeyUnitName(">"), Args: []parser.Expr{a, zero}}})
	expression := primaryKeyUnitRelation(t, e, "expression")
	partial := primaryKeyUnitRelation(t, e, "partial")
	combined := *expression
	combinedDefinition := *expression.sqlIndex
	combinedDefinition.predicate = partial.sqlIndex.predicate
	combined.sqlIndex = &combinedDefinition
	if err := validateIndexDefinition(e.CatalogRead(), &combined); err != nil {
		t.Fatal(err)
	}
	_, err = usingIndexEligibility(parent, &combined, raw)
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42809", Message: `index "expression" contains expressions`, Detail: "Cannot create a primary key or unique constraint using such an index.", Position: "17"})
	nonunique := combined
	nonuniqueDefinition := combinedDefinition
	nonuniqueDefinition.unique = false
	nonunique.sqlIndex = &nonuniqueDefinition
	if err := validateIndexDefinition(e.CatalogRead(), &nonunique); err != nil {
		t.Fatal(err)
	}
	_, err = usingIndexEligibility(parent, &nonunique, raw)
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42809", Message: `"expression" is not a unique index`, Detail: "Cannot create a primary key or unique constraint using such an index.", Position: "17"})
	// Actual HASH rejects at nonunique. Do not manufacture a unique HASH provider
	// to exercise PostgreSQL's later, native-public-unreachable method branch.
	usingIndexUnitExecute(t, e, s, &parser.CreateIndex{Name: parser.Identifier{Text: "hashed"}, Table: primaryKeyUnitName("t"), Method: "hash", Keys: []parser.IndexKey{{Column: parser.Identifier{Text: "a"}}}})
	hashed := primaryKeyUnitRelation(t, e, "hashed")
	_, err = usingIndexEligibility(parent, hashed, raw)
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42809", Message: `"hashed" is not a unique index`, Detail: "Cannot create a primary key or unique constraint using such an index.", Position: "17"})
	if index.sqlIndex.constraint != nil || !index.sqlIndex.valid || !index.sqlIndex.unique || len(parent.uniqueConstraints) != 0 {
		t.Fatal("eligibility mutated source")
	}
}

func TestUsingIndexImmutableGroup(t *testing.T) {
	for _, primary := range []bool{false, true} {
		t.Run(map[bool]string{false: "unique", true: "primary"}[primary], func(t *testing.T) {
			e, s, _, index := usingIndexUnitFixture(t)
			// Existing NN and descriptor comment are created through their existing real
			// providers. COMMENT ON INDEX is intentionally not a native SQL capability.
			usingIndexUnitExecute(t, e, s, &parser.AlterTable{Name: primaryKeyUnitName("t"), Actions: []parser.AlterTableAction{{Action: parser.SetNotNull, Column: parser.Identifier{Text: "a"}}}})
			copyIndex := *index
			copyIndex.revision++
			copyIndex.comment = "retained backing comment"
			stage := e.BeginCatalogStage(e.CatalogRead())
			defer e.DiscardCatalogStage(stage)
			if err := stage.Replace(&copyIndex); err != nil {
				t.Fatal(err)
			}
			prepared, err := e.ValidateCatalogStage(context.Background(), stage)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: e.state.epoch}); err != nil {
				t.Fatal(err)
			}
			parent := primaryKeyUnitRelation(t, e, "t")
			index = primaryKeyUnitRelation(t, e, "prebuilt")
			sibling := primaryKeyUnitRelation(t, e, "sibling")
			store, root, siblingRoot := e.storage[parent.address], e.indexRoots[index.address], e.indexRoots[sibling.address]
			sourceColumns, sourceNN := slices.Clone(parent.columns), slices.Clone(parent.notNullConstraints)
			sourceEntries := slices.Clone(root.entries)
			for i := range sourceEntries {
				sourceEntries[i].keys = slices.Clone(sourceEntries[i].keys)
			}
			usingIndexUnitExecute(t, e, s, usingIndexUnitAlter(primary))
			next := primaryKeyUnitRelation(t, e, "t")
			attached := primaryKeyUnitRelation(t, e, "owned")
			if next == parent || attached == index || next.address != parent.address || attached.address != index.address || e.storage[parent.address] != store || e.indexRoots[index.address] != root || e.indexRoots[sibling.address] != siblingRoot || e.state.descriptors[sibling.address] != sibling {
				t.Fatal("metadata attachment copied/reminted storage, roots or sibling")
			}
			if len(next.uniqueConstraints) != 1 || next.uniqueConstraints[0].OID == 0 || next.uniqueConstraints[0].Index != index.address || !backingConstraintPresent(next, attached) || attached.sqlIndex.constraint.Primary != primary || attached.comment != index.comment || attached.owner != index.owner || attached.namespace != index.namespace {
				t.Fatal("nonreciprocal or lossy attachment")
			}
			if &next.attributes[0] != &parent.attributes[0] || &attached.sqlIndex.keys[0] != &index.sqlIndex.keys[0] || &attached.sqlIndex.include[0] != &index.sqlIndex.include[0] || !reflect.DeepEqual(root.entries, sourceEntries) {
				t.Fatal("copied or mutated immutable vectors/root entries")
			}
			if !reflect.DeepEqual(parent.columns, sourceColumns) || !reflect.DeepEqual(parent.notNullConstraints, sourceNN) || len(parent.uniqueConstraints) != 0 || index.name != "prebuilt" || index.sqlIndex.constraint != nil {
				t.Fatal("source descriptor changed")
			}
			if next.notNullConstraints[0].OID != sourceNN[0].OID || next.columns[2].Nullable != true {
				t.Fatal("lost existing NN or included payload became NN")
			}
			if primary && (len(next.notNullConstraints) != 2 || next.columns[0].Nullable || next.columns[1].Nullable) {
				t.Fatal("missing key-only NN")
			}
			if !primary && len(next.notNullConstraints) != 1 {
				t.Fatal("UNIQUE added NN")
			}
			usingIndexUnitExecute(t, e, s, &parser.CreateTable{Name: primaryKeyUnitName("copied"), Elements: []parser.TableElement{{Like: &parser.TableLike{Name: primaryKeyUnitName("t"), Options: []parser.LikeOption{{Name: "all", Including: true}}}}}})
			copied := primaryKeyUnitRelation(t, e, "copied")
			copiedIndex := e.state.descriptors[copied.uniqueConstraints[0].Index].(*relationDescriptor)
			if copiedIndex.address == attached.address || copied.uniqueConstraints[0].OID == next.uniqueConstraints[0].OID || copiedIndex.comment != attached.comment || !backingConstraintPresent(copied, copiedIndex) || e.indexRoots[copiedIndex.address] == root || len(e.indexRoots[copiedIndex.address].entries) != 0 || e.state.descriptors[attached.address] != attached || e.indexRoots[attached.address] != root {
				t.Fatal("LIKE lost backing metadata, reused identity/data or mutated source")
			}
		})
	}
}

func TestUsingIndexCombinedTypeAttachment(t *testing.T) {
	for _, primary := range []bool{false, true} {
		for _, change := range []struct {
			column, target string
			slot           int
			usage          sqltype.ID
		}{{"a", "bigint", 0, sqltype.Int8}, {"secret", "varchar", 2, sqltype.VarChar}} {
			for _, typeFirst := range []bool{false, true} {
				for _, collision := range []bool{false, true} {
					name := map[bool]string{false: "unique", true: "primary"}[primary] + "/" + change.column + "/" + map[bool]string{false: "attach_first", true: "type_first"}[typeFirst] + "/" + map[bool]string{false: "success", true: "collision"}[collision]
					t.Run(name, func(t *testing.T) {
						e, s, parent, index := usingIndexUnitFixture(t)
						if collision {
							usingIndexUnitExecute(t, e, s, usingIndexUnitTable("owned"))
						}
						catalog, store, root := e.state, e.storage[parent.address], e.indexRoots[index.address]
						entries := slices.Clone(root.entries)
						for i := range entries {
							entries[i].keys = slices.Clone(entries[i].keys)
						}
						alter := usingIndexUnitAlter(primary)
						typeAction := parser.AlterTableAction{Action: parser.AlterColumnType, Column: parser.Identifier{Text: change.column}, TypeName: parser.TypeName{Name: change.target}}
						if typeFirst {
							alter.Actions = append([]parser.AlterTableAction{typeAction}, alter.Actions...)
						} else {
							alter.Actions = append(alter.Actions, typeAction)
						}
						op, err := s.Acquire(context.Background())
						if err != nil {
							t.Fatal(err)
						}
						result, err := e.Execute(op, alter, 0, nil)
						op.Done()
						if collision {
							var diagnostic *sqlstate.Error
							if !errors.As(err, &diagnostic) || diagnostic.Code != "42P07" || diagnostic.Message != `relation "owned" already exists` {
								t.Fatal(result, err)
							}
							if e.state != catalog || e.storage[parent.address] != store || e.indexRoots[index.address] != root || !reflect.DeepEqual(root.entries, entries) || primaryKeyUnitRelation(t, e, "prebuilt") != index {
								t.Fatal("failed combined TYPE attachment published a candidate")
							}
							retry := usingIndexUnitAlter(primary)
							retry.Actions[0].Constraint = parser.Identifier{}
							retry.Actions[0].UsingIndex.Name = parser.Identifier{}
							usingIndexUnitExecute(t, e, s, retry)
							if primaryKeyUnitRelation(t, e, "prebuilt").address != index.address {
								t.Fatal("collision retired the original index")
							}
							return
						}
						if err != nil || result.Rows != nil || result.Count != 0 {
							t.Fatal(result, err)
						}
						current, backing := primaryKeyUnitRelation(t, e, "t"), primaryKeyUnitRelation(t, e, "owned")
						if current.address != parent.address || backing.address == index.address || !backingConstraintPresent(current, backing) || len(current.uniqueConstraints) != 1 || current.uniqueConstraints[0].Index != backing.address || current.columns[change.slot].Usage.Type.ID() != change.usage || backing.columns[change.slot].Usage.Type.ID() != change.usage || len(e.indexRoots[backing.address].entries) != 2 {
							t.Fatal("combined TYPE attachment lost reminted reciprocal identity, type or rows")
						}
						if len(parent.uniqueConstraints) != 0 || index.sqlIndex.constraint != nil || index.columns[change.slot].Usage.Type.ID() == change.usage || !reflect.DeepEqual(root.entries, entries) {
							t.Fatal("combined TYPE attachment mutated original descriptors/root")
						}
						if primary && (current.columns[0].Nullable || current.columns[1].Nullable || !current.columns[2].Nullable) {
							t.Fatal("combined TYPE attachment lost key-only NN")
						}
						if usingIndexUnitExecute(t, e, s, primaryKeyUnitInsert(3)).Count != 1 {
							t.Fatal("combined TYPE attachment lost healthy producer")
						}
					})
				}
			}
		}
	}
}

func TestUsingIndexPrivateOverlay(t *testing.T) {
	t.Run("metadata_only_commit", func(t *testing.T) {
		e, s, parent, index := usingIndexUnitFixture(t)
		store, root := e.storage[parent.address], e.indexRoots[index.address]
		entries := slices.Clone(root.entries)
		for i := range entries {
			entries[i].keys = slices.Clone(entries[i].keys)
		}
		tx := transactionBegin(t, s)
		usingIndexUnitExecute(t, e, s, usingIndexUnitAlter(true))
		if _, exists := s.transactions.catalog.storage[parent.address]; exists {
			t.Fatal("metadata attachment registered an index rebuild shape")
		}
		if s.transactions.catalog.uniqueRoots[index.address] != nil || e.indexRoots[index.address] != root || e.storage[parent.address] != store || len(parent.uniqueConstraints) != 0 {
			t.Fatal("metadata attachment copied a private baseline or leaked")
		}
		if !transactionEnd(t, s, tx, true).Committed {
			t.Fatal("metadata commit rolled back")
		}
		current := primaryKeyUnitRelation(t, e, "owned")
		if current.address != index.address || !backingConstraintPresent(primaryKeyUnitRelation(t, e, "t"), current) || e.indexRoots[index.address] != root || e.storage[parent.address] != store || !reflect.DeepEqual(root.entries, entries) {
			t.Fatal("metadata-only commit rebuilt roots/storage or changed entries")
		}
	})
	e, s, parent, index := usingIndexUnitFixture(t)
	committedStore, committedRoot := e.storage[parent.address], e.indexRoots[index.address]
	tx := transactionBegin(t, s)
	usingIndexUnitExecute(t, e, s, &parser.AlterTable{Name: primaryKeyUnitName("t"), Actions: []parser.AlterTableAction{{Action: parser.AlterColumnType, Column: parser.Identifier{Text: "a"}, TypeName: parser.TypeName{Name: "bigint"}}}})
	earlier := s.transactions.catalog
	privateRoot := earlier.uniqueRoots[index.address]
	privateStore := earlier.storage[parent.address].replacement
	if privateRoot == nil || privateRoot == committedRoot || privateStore == nil {
		t.Fatal("real prior TYPE roots absent")
	}
	usingIndexUnitExecute(t, e, s, &parser.SavepointControl{Kind: parser.CreateSavepoint, Name: parser.Identifier{Text: "before"}})
	usingIndexUnitExecute(t, e, s, usingIndexUnitAlter(true))
	attached := s.transactions.catalog
	if attached == earlier || attached.uniqueRoots[index.address] != privateRoot || attached.storage[parent.address].replacement != privateStore || earlier.descriptors[index.address].(*relationDescriptor).sqlIndex.constraint != nil || attached.originals[parent.address] != parent || attached.originals[index.address] != index || e.state.descriptors[parent.address] != parent || e.state.descriptors[index.address] != index {
		t.Fatal("private attachment copied roots, changed originals or leaked")
	}
	usingIndexUnitExecute(t, e, s, &parser.SavepointControl{Kind: parser.CreateSavepoint, Name: parser.Identifier{Text: "attached"}})
	usingIndexUnitExecute(t, e, s, &parser.AlterTableRenameConstraint{Name: primaryKeyUnitName("t"), Constraint: parser.Identifier{Text: "owned"}, NewName: parser.Identifier{Text: "later"}})
	if attached.descriptors[index.address].Name() != "owned" || s.transactions.catalog.descriptors[index.address].Name() != "later" {
		t.Fatal("rename mutated saved overlay")
	}
	usingIndexUnitExecute(t, e, s, &parser.SavepointControl{Kind: parser.RollbackSavepoint, Name: parser.Identifier{Text: "attached"}})
	if s.transactions.catalog != attached {
		t.Fatal("savepoint did not restore exact attachment overlay")
	}
	usingIndexUnitExecute(t, e, s, &parser.SavepointControl{Kind: parser.RollbackSavepoint, Name: parser.Identifier{Text: "before"}})
	if s.transactions.catalog != earlier || earlier.descriptors[index.address].Name() != "prebuilt" {
		t.Fatal("attachment rollback lost prior private roots")
	}
	usingIndexUnitExecute(t, e, s, usingIndexUnitAlter(true))
	if !transactionEnd(t, s, tx, true).Committed {
		t.Fatal("commit rolled back")
	}
	current := primaryKeyUnitRelation(t, e, "owned")
	// Earlier TYPE, not attachment, legitimately reconstructs at final publication.
	committed := e.indexRoots[index.address]
	if current.address != index.address || committed == committedRoot || len(committed.entries) != 2 || committed.entries[0].keys[0].Type().ID() != sqltype.Int8 || e.storage[parent.address] != privateStore || len(parent.uniqueConstraints) != 0 || len(committedRoot.entries) != 2 || len(committedStore.versions) != 2 {
		t.Fatal("TYPE/attachment commit lost its real data or mutated old snapshot")
	}
}

func TestUsingIndexPublicationFences(t *testing.T) {
	for _, phase := range []string{"validation", "finish"} {
		for _, change := range []string{"cancel", "reset", "restore", "collision", "parent_descriptor", "index_descriptor", "authorization"} {
			t.Run(phase+"/"+change, func(t *testing.T) {
				e, s, parent, index := usingIndexUnitFixture(t)
				store, root := e.storage[parent.address], e.indexRoots[index.address]
				var dump bytes.Buffer
				if err := e.Dump(context.Background(), &dump); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				mutate := func() {
					switch change {
					case "cancel":
						cancel()
					case "reset":
						if err := e.Reset(context.Background()); err != nil {
							t.Fatal(err)
						}
						usingIndexUnitSeed(t, e, sessionStart(t, e, nil))
					case "restore":
						if err := e.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
					case "collision":
						usingIndexUnitExecute(t, e, sessionStart(t, e, nil), usingIndexUnitTable("owned"))
					case "parent_descriptor", "index_descriptor":
						original := parent
						if change == "index_descriptor" {
							original = index
						}
						e.mu.Lock()
						copyDescriptor := *original
						copyDescriptor.revision++
						copyDescriptor.comment = "concurrent replacement"
						state := cloneCatalog(e.state)
						state.descriptors[original.address] = &copyDescriptor
						e.state = state
						e.mu.Unlock()
					case "authorization":
						usingIndexUnitExecute(t, e, sessionStart(t, e, nil), &parser.CreateRole{Name: parser.Identifier{Text: "changed_authority"}, DefaultLogin: true})
					}
				}
				provider := e.providers[ids.RelationClassID]
				if phase == "validation" {
					var once sync.Once
					e.providers[ids.RelationClassID] = alterDropProvider{CatalogProvider: provider, callback: func() { once.Do(func() { e.providers[ids.RelationClassID] = provider; mutate() }) }}
				}
				op, err := s.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				_, err = scope.Execute(usingIndexUnitAlter(true), 0, nil)
				if phase == "finish" {
					if err != nil {
						scope.Finish(err)
						t.Fatal(err)
					}
					mutate()
				}
				err = scope.Finish(err)
				if err == nil {
					t.Fatal("publication fence missing")
				}
				if change == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if change == "reset" || change == "restore" {
					if !errors.Is(err, ErrCatalogReplaced) {
						t.Fatal(err)
					}
					current := primaryKeyUnitRelation(t, e, "t")
					standalone := primaryKeyUnitRelation(t, e, "prebuilt")
					if len(current.uniqueConstraints) != 0 || len(current.notNullConstraints) != 0 || standalone.sqlIndex.constraint != nil {
						t.Fatal("old candidate leaked into replacement epoch")
					}
					return
				}
				if change != "index_descriptor" && e.state.descriptors[index.address] != index || change != "parent_descriptor" && e.state.descriptors[parent.address] != parent || e.storage[parent.address] != store || e.indexRoots[index.address] != root || len(parent.uniqueConstraints) != 0 || len(parent.notNullConstraints) != 0 || index.sqlIndex.constraint != nil {
					t.Fatal("failed publication leaked an attachment, NN, storage or root")
				}
				liveParent := e.state.descriptors[parent.address].(*relationDescriptor)
				liveIndex := e.state.descriptors[index.address].(*relationDescriptor)
				if len(liveParent.uniqueConstraints) != 0 || len(liveParent.notNullConstraints) != 0 || liveIndex.sqlIndex.constraint != nil || liveIndex.name != "prebuilt" {
					t.Fatal("replacement descriptor retained failed candidate")
				}
			})
		}
	}
	t.Run("successful_finish_no_retroactive_cancel", func(t *testing.T) {
		e, s, parent, index := usingIndexUnitFixture(t)
		root := e.indexRoots[index.address]
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		op, err := s.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		_, err = scope.Execute(usingIndexUnitAlter(true), 0, nil)
		if err = scope.Finish(err); err != nil {
			t.Fatal(err)
		}
		cancel()
		if primaryKeyUnitRelation(t, e, "owned").address != index.address || !backingConstraintPresent(primaryKeyUnitRelation(t, e, "t"), primaryKeyUnitRelation(t, e, "owned")) || e.indexRoots[index.address] != root || len(parent.uniqueConstraints) != 0 {
			t.Fatal("successful effect undone by later cancellation")
		}
	})
}

func TestUsingIndexIncludeLifecycle(t *testing.T) {
	t.Run("semantic_var_like", usingIndexUnitVarLifecycle)
	e, s, _, index := usingIndexUnitFixture(t)
	usingIndexUnitExecute(t, e, s, usingIndexUnitAlter(true))
	parent := primaryKeyUnitRelation(t, e, "t")
	constraint := parent.uniqueConstraints[0]
	if len(constraint.Attributes) != 2 || len(index.sqlIndex.include) != 1 {
		t.Fatal("INCLUDE entered conkey")
	}
	for _, entry := range e.indexRoots[index.address].entries {
		if len(entry.keys) != 2 {
			t.Fatal("INCLUDE entered equality root")
		}
	}
	edges, err := collectPGDepend(context.Background(), e.CatalogRead())
	if err != nil {
		t.Fatal(err)
	}
	for _, attribute := range parent.attributes {
		target := parent.address
		target.SubID = attribute
		if !slices.Contains(edges, pgDependEdge{dependent: ids.ObjectAddress{ClassID: 2606, ObjectID: constraint.OID}, referenced: target, deptype: 'a'}) {
			t.Fatal("missing key/include auto dependency", target)
		}
	}
	usingIndexUnitExecute(t, e, s, &parser.AlterTableRenameColumn{Name: primaryKeyUnitName("t"), Column: parser.Identifier{Text: "secret"}, NewName: parser.Identifier{Text: "current_secret"}})
	renamed := primaryKeyUnitRelation(t, e, "t")
	oldIndex := primaryKeyUnitRelation(t, e, "owned")
	oldNN := slices.Clone(renamed.notNullConstraints)
	if oldIndex.columns[2].Name != "secret" {
		t.Fatal("rename changed physical backing label")
	}
	if text, err := (pgRenderer{view: e.CatalogRead()}).uniqueConstraintSQL(renamed, &renamed.uniqueConstraints[0]); err != nil || text != "PRIMARY KEY (a, b) INCLUDE (current_secret)" {
		t.Fatal(text, err)
	}
	usingIndexUnitExecute(t, e, s, &parser.AlterTable{Name: primaryKeyUnitName("t"), Actions: []parser.AlterTableAction{{Action: parser.AlterColumnType, Column: parser.Identifier{Text: "current_secret"}, TypeName: parser.TypeName{Name: "varchar"}}}})
	typed := primaryKeyUnitRelation(t, e, "t")
	backing := primaryKeyUnitRelation(t, e, "owned")
	if typed.address != renamed.address || typed.uniqueConstraints[0].OID == constraint.OID || backing.address == oldIndex.address || !reflect.DeepEqual(typed.notNullConstraints, oldNN) || backing.columns[2].Usage.Type.ID() != sqltype.VarChar || backing.columns[2].Name != "current_secret" || backing.columns[2].OriginTable != uint32(backing.address.ObjectID) || oldIndex.columns[2].Usage.Type.ID() != sqltype.Text || len(e.indexRoots[backing.address].entries) != 2 {
		t.Fatal("INCLUDE TYPE did not rebuild reciprocal current metadata immutably")
	}
	usingIndexUnitExecute(t, e, s, &parser.AlterTable{Name: primaryKeyUnitName("t"), Actions: []parser.AlterTableAction{{Action: parser.DropColumn, Column: parser.Identifier{Text: "current_secret"}, Mode: parser.DropRestrict}}})
	dropped := primaryKeyUnitRelation(t, e, "t")
	if len(dropped.uniqueConstraints) != 0 || !reflect.DeepEqual(dropped.notNullConstraints, oldNN) || len(dropped.columns) != 2 || dropped.columns[0].Nullable || dropped.columns[1].Nullable || e.state.descriptors[backing.address] != nil || e.indexRoots[backing.address] != nil || len(typed.uniqueConstraints) != 1 {
		t.Fatal("INCLUDE DROP retained group or removed key NN/mutated source")
	}
}

func usingIndexUnitVarLifecycle(t *testing.T) {
	e, s, _, _ := usingIndexUnitFixture(t)
	usingIndexUnitExecute(t, e, s, &parser.DropIndex{Names: []parser.ObjectName{primaryKeyUnitName("prebuilt")}})
	usingIndexUnitExecute(t, e, s, &parser.CreateIndex{Name: parser.Identifier{Text: "prebuilt"}, Table: primaryKeyUnitName("t"), Method: "btree", Unique: true, Concurrent: parser.Position{Character: 1}, Keys: []parser.IndexKey{{Expression: parser.ColumnExpr{Name: primaryKeyUnitName("a")}}}, Include: []parser.IndexKey{{Column: parser.Identifier{Text: "secret"}}}})
	// CREATE deliberately normalizes a bare Var to a plain attribute. Bind the
	// typed expression through its real provider and publish that valid derived
	// representation explicitly, rather than depending on incidental parser shape.
	parentInput := primaryKeyUnitRelation(t, e, "t")
	primitive := primaryKeyUnitRelation(t, e, "prebuilt")
	bindOp, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	auth, err := bindOp.Authorization()
	if err != nil {
		bindOp.Done()
		t.Fatal(err)
	}
	e.mu.Lock()
	derived, _, err := e.bindIndexExpression(bindOp, sqltype.InputEnvironment{}, auth, parentInput, parser.ColumnExpr{Name: primaryKeyUnitName("a")}, nil)
	e.mu.Unlock()
	bindOp.Done()
	if err != nil {
		t.Fatal(err)
	}
	candidate := *primitive
	candidate.revision++
	definition := *primitive.sqlIndex
	definition.keys = slices.Clone(definition.keys)
	definition.keys[0].attribute, definition.keys[0].derived = 0, derived
	candidate.sqlIndex = &definition
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(&candidate); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: e.state.epoch}); err != nil {
		t.Fatal(err)
	}
	source := primaryKeyUnitRelation(t, e, "prebuilt")
	if source.sqlIndex.keys[0].attribute != 0 || source.sqlIndex.keys[0].derived == nil {
		t.Fatal("not a real bound semantic Var")
	}
	usingIndexUnitExecute(t, e, s, usingIndexUnitAlter(true))
	parent := primaryKeyUnitRelation(t, e, "t")
	keyParent := parent
	usingIndexUnitExecute(t, e, s, &parser.AlterTable{Name: primaryKeyUnitName("t"), Actions: []parser.AlterTableAction{{Action: parser.AlterColumnType, Column: parser.Identifier{Text: "a"}, TypeName: parser.TypeName{Name: "bigint"}}}})
	parent = primaryKeyUnitRelation(t, e, "t")
	keyBacking := primaryKeyUnitRelation(t, e, "owned")
	if keyBacking.address == source.address || parent.uniqueConstraints[0].OID == keyParent.uniqueConstraints[0].OID || parent.notNullConstraints[0].OID == keyParent.notNullConstraints[0].OID || keyBacking.sqlIndex.keys[0].attribute != 0 || keyBacking.sqlIndex.keys[0].derived.execution.ResultUsage().Type.ID() != sqltype.Int8 || keyBacking.columns[0].Usage.Type.ID() != sqltype.Int8 || source.columns[0].Usage.Type.ID() != sqltype.Int4 {
		t.Fatal("semantic Var key TYPE lost fresh identity/usage or mutated source")
	}
	if !slices.Equal(parent.uniqueConstraints[0].Attributes, parent.attributes[:1]) {
		t.Fatal("semantic Var not mapped into conkey")
	}
	usingIndexUnitExecute(t, e, s, &parser.CreateTable{Name: primaryKeyUnitName("copied"), Elements: []parser.TableElement{{Like: &parser.TableLike{Name: primaryKeyUnitName("t"), Options: []parser.LikeOption{{Name: "all", Including: true}}}}}})
	copied := primaryKeyUnitRelation(t, e, "copied")
	copyIndex := e.state.descriptors[copied.uniqueConstraints[0].Index].(*relationDescriptor)
	copyRoot := e.indexRoots[copyIndex.address]
	if copyIndex.address == keyBacking.address || copyIndex.sqlIndex.keys[0].attribute != 0 || copyIndex.sqlIndex.keys[0].derived == nil || copyIndex.sqlIndex.keys[0].derived.execution.ResultUsage().Type.ID() != sqltype.Int8 || !backingConstraintPresent(copied, copyIndex) || len(copyRoot.entries) != 0 {
		t.Fatal("LIKE did not independently remap actual Var/include metadata")
	}
	usingIndexUnitExecute(t, e, s, &parser.AlterTable{Name: primaryKeyUnitName("t"), Actions: []parser.AlterTableAction{{Action: parser.AlterColumnType, Column: parser.Identifier{Text: "secret"}, TypeName: parser.TypeName{Name: "varchar"}}}})
	typed := primaryKeyUnitRelation(t, e, "t")
	current := primaryKeyUnitRelation(t, e, "owned")
	if current.address == keyBacking.address || current.sqlIndex.keys[0].attribute != 0 || !slices.Equal(typed.uniqueConstraints[0].Attributes, typed.attributes[:1]) || !reflect.DeepEqual(typed.notNullConstraints, parent.notNullConstraints) || current.columns[1].Usage.Type.ID() != sqltype.VarChar || copyIndex.columns[1].Usage.Type.ID() != sqltype.Text {
		t.Fatal("semantic Var/include TYPE lost independent key or NN mapping")
	}
	usingIndexUnitExecute(t, e, s, &parser.DropTable{Names: []parser.ObjectName{primaryKeyUnitName("t")}, Mode: parser.DropRestrict})
	insert := primaryKeyUnitInsert(3)
	insert.Name = primaryKeyUnitName("copied")
	if result := usingIndexUnitExecute(t, e, s, insert); result.Count != 1 {
		t.Fatal(result)
	}
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	_, err = e.Execute(op, insert, 0, nil)
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `duplicate key value violates unique constraint "` + copied.uniqueConstraints[0].Name + `"`, Detail: "Key (a)=(3) already exists.", Schema: "public", Table: "copied", Constraint: copied.uniqueConstraints[0].Name})
	if len(copyRoot.entries) != 0 || len(e.indexRoots[copyIndex.address].entries) != 1 || len(source.sqlIndex.include) != 1 || source.sqlIndex.constraint != nil {
		t.Fatal("copy enforcement mutated held source/baseline")
	}
}

func TestUsingIndexRestoreGeneration(t *testing.T) {
	e, s, _, _ := usingIndexUnitFixture(t)
	usingIndexUnitExecute(t, e, s, usingIndexUnitAlter(true))
	var dump bytes.Buffer
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	// Deliberately diverge after Dump so success cannot be a no-op replay.
	usingIndexUnitExecute(t, e, s, primaryKeyUnitInsert(3))
	before := e.CatalogRead()
	parent, index := primaryKeyUnitRelation(t, e, "t"), primaryKeyUnitRelation(t, e, "owned")
	parentRef, indexRef := catalogRef(t, e, before, "t"), catalogRef(t, e, before, "owned")
	constraintRef := parentRef.Domain().Ref(before.Epoch(), ids.ObjectAddress{ClassID: 2606, ObjectID: parent.uniqueConstraints[0].OID})
	store, root := e.storage[parent.address], e.indexRoots[index.address]
	versions := slices.Clone(store.versions)
	columns, constraints := slices.Clone(parent.columns), slices.Clone(parent.uniqueConstraints)
	entries := slices.Clone(root.entries)
	for i := range entries {
		entries[i].keys = slices.Clone(entries[i].keys)
	}
	if len(entries) != 3 {
		t.Fatal("missing changed pre-Restore data", len(entries))
	}
	if err := e.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	after := e.CatalogRead()
	current, backing := primaryKeyUnitRelation(t, e, "t"), primaryKeyUnitRelation(t, e, "owned")
	if len(current.uniqueConstraints) != 1 || current.uniqueConstraints[0].OID == 0 || current.uniqueConstraints[0].OID == current.address.ObjectID || current.uniqueConstraints[0].OID == backing.address.ObjectID {
		t.Fatal("Restore lost valid cross-object constraint identity", current.uniqueConstraints)
	}
	currentParentRef, currentIndexRef := catalogRef(t, e, after, "t"), catalogRef(t, e, after, "owned")
	currentConstraintRef := currentParentRef.Domain().Ref(after.Epoch(), ids.ObjectAddress{ClassID: 2606, ObjectID: current.uniqueConstraints[0].OID})
	if after.Epoch() != before.Epoch()+1 || currentParentRef == parentRef || currentIndexRef == indexRef || currentConstraintRef == constraintRef || current.address != parent.address {
		t.Fatal("Restore did not establish fresh group generation at retained parent", before.Epoch(), after.Epoch(), constraintRef, currentConstraintRef)
	}
	if current == parent || backing == index || e.storage[current.address] == store || e.indexRoots[backing.address] == root || len(e.indexRoots[backing.address].entries) != 2 || !backingConstraintPresent(current, backing) {
		t.Fatal("Restore transplanted old descriptor/storage/root or lost reciprocal data")
	}
	if current.columns[0].Nullable || current.columns[1].Nullable || !current.columns[2].Nullable || len(backing.sqlIndex.keys) != 2 || len(backing.sqlIndex.include) != 1 {
		t.Fatal("Restore lost key-only NN or physical INCLUDE")
	}
	if before.state.descriptors[parent.address] != parent || before.state.descriptors[index.address] != index || !reflect.DeepEqual(parent.columns, columns) || !reflect.DeepEqual(parent.uniqueConstraints, constraints) || !reflect.DeepEqual(store.versions, versions) || !reflect.DeepEqual(root.entries, entries) {
		t.Fatal("Restore mutated captured predecessor generation")
	}
	var redump bytes.Buffer
	if err := e.Dump(context.Background(), &redump); err != nil || redump.String() != dump.String() {
		t.Fatal("Restore failed canonical roundtrip", err, redump.String())
	}
	// The real old session now targets new storage, and the discarded key is reusable.
	usingIndexUnitExecute(t, e, s, primaryKeyUnitInsert(3))
	if len(e.indexRoots[backing.address].entries) != 3 || !reflect.DeepEqual(root.entries, entries) {
		t.Fatal("retained session did not target fresh enforcement root")
	}
}
