package tablecmds_test

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestAnalyzePublicationCancellation(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE analyze_cancel(a INT)", "INSERT INTO analyze_cancel VALUES(1),(1),(2)", "ANALYZE analyze_cancel", "INSERT INTO analyze_cancel VALUES(3),(3)"} {
		enginetest.SessionSQL(t, s, q)
	}
	ref, store := vacuumStore(t, e, "analyze_cancel")
	metadata := e.Storage.RelationMetadata[ref.Address]
	maintenance, rows, ends, generation := store.Maintenance, store.Versions, store.Ends, store.Generation
	// Reuse the owner's test-owned operation Context convention. At finish,
	// actual visibility traversal and typed grouping have already run detached;
	// this is not a pre-canceled admission or a fabricated publication candidate.
	task := startVacuumBarrier(t, e, s, "ANALYZE analyze_cancel", "storage.(*statisticsCollector).finish")
	task.cancel()
	task.join(t, context.Canceled)
	if e.Storage.RelationMetadata[ref.Address] != metadata || store.Maintenance != maintenance || !storage.SameVersionRoot(rows, store.Versions) || store.Ends != ends || store.Generation != generation {
		t.Fatal("cancellation after detached collection installed statistics or scan facts")
	}
	lockEmpty(t, e)
	enginetest.SessionSQL(t, s, "ANALYZE analyze_cancel")
	if fresh := e.Storage.RelationMetadata[ref.Address]; fresh == metadata || fresh.Statistics == metadata.Statistics || fresh.AnalyzeCount != metadata.AnalyzeCount+1 {
		t.Fatal("canceled capture prevented later successful collection")
	}
	t.Run("final_install_checkpoint", analyzeCancellationBeforeInstall)
}

func TestAnalyzePublicationAuthority(t *testing.T) {
	for _, mutation := range []string{"epoch", "store", "shape", "attribute", "type", "collation", "rights"} {
		t.Run(mutation, func(t *testing.T) {
			e := engine.New()
			owner := enginetest.StartSession(t, e, nil)
			worker := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE ROLE analyze_actor", "CREATE TABLE analyze_authority(a TEXT,b TEXT)", "INSERT INTO analyze_authority VALUES('x','m'),('x','m'),('y','n')", "ANALYZE analyze_authority", "GRANT MAINTAIN ON analyze_authority TO analyze_actor", "INSERT INTO analyze_authority VALUES('z','o')"} {
				enginetest.SessionSQL(t, owner, q)
			}
			enginetest.SessionSQL(t, worker, "SET ROLE analyze_actor")
			ref, old := vacuumStore(t, e, "analyze_authority")
			metadata := e.Storage.RelationMetadata[ref.Address]
			facts, maintenance, rows, ends := metadata.Statistics, old.Maintenance, old.Versions, old.Ends
			if mutation == "epoch" {
				// Implicit statements intentionally retain replacement admission
				// throughout detached work. Explicit transactions instead retain
				// epoch-fenced private authority, so a real Reset can invalidate
				// this capture without bypassing or deadlocking that admission.
				enginetest.SessionSQL(t, worker, "BEGIN")
			}
			task := startVacuumBarrier(t, e, worker, "ANALYZE analyze_authority", "storage.(*statisticsCollector).finish")
			switch mutation {
			case "epoch":
				if err := engine.Reset(context.Background(), e); err != nil {
					t.Fatal(err)
				}
				if e.CatalogRead().Epoch() == ref.Epoch {
					t.Fatal("Reset did not actually advance captured catalog epoch")
				}
			case "rights":
				enginetest.SessionSQL(t, owner, "REVOKE MAINTAIN ON analyze_authority FROM analyze_actor")
			default:
				base := e.CatalogRead()
				stage := e.BeginCatalogStage(base)
				defer e.DiscardCatalogStage(stage)
				if mutation == "store" {
					// A genuine host storage publication remints the same logical
					// relation. No direct write to the committed relation map.
					replacement := *old
					stage.RowShapes = map[ids.ObjectAddress]storage.RowShape{ref.Address: {
						Original: old, Root: old.Versions, Replacement: &replacement,
						ObservedLength: len(old.Versions), ObservedEnds: old.Ends, Generation: old.Generation,
					}}
				} else {
					next := *base.MustRelation(ref.Address)
					next.ObjectRevision++
					next.Columns = append([]catalog.Column(nil), next.Columns...)
					switch mutation {
					case "shape":
						next.Columns[0].Name = "renamed"
						next.Index = map[string]int{"renamed": 0, "b": 1}
					case "attribute":
						// Live stable attributes must remain strictly ordered.
						// Remint the trailing attribute, not the leading slot.
						next.Attributes = []int32{next.Attributes[0], 3}
						next.ColumnMeta.HighWater = 3
						next.Columns[1].OriginAttribute = 3
					case "type":
						next.Columns[0].Usage = enginetest.Usage(e, sqltype.VarChar)
					case "collation":
						next.Columns[0].Usage.Collation = sqltype.CollationRef{ID: 950, Derivation: sqltype.Default}
					}
					if err := stage.Replace(&next); err != nil {
						t.Fatal(err)
					}
				}
				prepared, err := e.ValidateCatalogStage(context.Background(), stage)
				if err != nil {
					t.Fatal("real authority replacement did not validate", err)
				}
				if _, err := publishCatalog(e, prepared, core.PublicationToken{Epoch: base.Epoch()}); err != nil {
					t.Fatal("real authority replacement did not publish", err)
				}
				if mutation == "store" && e.Storage.Relations[ref.Address] == old {
					t.Fatal("host publication did not actually replace captured storage")
				}
			}
			task.barrier.resume()
			select {
			case err := <-task.done:
				task.joined = true
				if err == nil {
					t.Fatal("captured publication survived authority replacement", mutation)
				}
				if mutation == "epoch" && !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("epoch replacement returned unrelated rejection", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("rejected publication failed to join")
			}
			if metadata.Statistics != facts || old.Maintenance != maintenance || !storage.SameVersionRoot(rows, old.Versions) || old.Ends != ends {
				t.Fatal("rejected candidate modified captured canonical roots")
			}
			if current := e.Storage.RelationMetadata[ref.Address]; current != nil {
				observed := *current
				observed.Statistics = metadata.Statistics
				if observed != *metadata {
					t.Fatal("rejected candidate changed canonical ANALYZE scan facts")
				}
				if current.Statistics != nil {
					for key, value := range current.Statistics.Columns {
						if facts.Columns[key] != value {
							t.Fatal("rejected candidate attached to current authority")
						}
					}
				}
			}
			if mutation == "epoch" {
				enginetest.SessionSQL(t, worker, "ROLLBACK")
			}
			lockEmpty(t, e)
		})
	}
}

// Unlike the detached-work barrier, this Context cancels at the direct
// analyzeRelation checkpoint after candidates and final authority checks exist.
// Stack inspection is test-owned, like vacuumBarrierContext; production gains
// no hooks or observation exports, and no source line number is pinned here.
type analyzeInstallContext struct {
	context.Context
	cancel context.CancelFunc
	fired  bool
}

func (c *analyzeInstallContext) Err() error {
	var pcs [1]uintptr
	n := runtime.Callers(2, pcs[:])
	if n != 0 {
		frame, _ := runtime.CallersFrames(pcs[:n]).Next()
		if frame.Function == "stuntgres/internal/engine/tablecmds.analyzeRelation" {
			c.fired = true
			c.cancel()
		}
	}
	return c.Context.Err()
}

func analyzeCancellationBeforeInstall(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE analyze_install(a INT)", "INSERT INTO analyze_install VALUES(1),(1),(2)", "ANALYZE analyze_install", "INSERT INTO analyze_install VALUES(3),(3)"} {
		enginetest.SessionSQL(t, s, q)
	}
	ref, store := vacuumStore(t, e, "analyze_install")
	metadata, maintenance := e.Storage.RelationMetadata[ref.Address], store.Maintenance
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := &analyzeInstallContext{Context: op.Context(), cancel: cancel}
	op.Ctx = checkpoint
	_, err = e.Execute(op, enginetest.Statement(t, "ANALYZE analyze_install"), 0, nil)
	op.Done()
	if !checkpoint.fired || !errors.Is(err, context.Canceled) {
		t.Fatal("actual final publication cancellation checkpoint not exercised", checkpoint.fired, err)
	}
	if e.Storage.RelationMetadata[ref.Address] != metadata || store.Maintenance != maintenance {
		t.Fatal("final checkpoint cancellation installed complete detached candidate")
	}
	lockEmpty(t, e)
}
