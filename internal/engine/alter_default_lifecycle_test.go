package engine

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestAlterDefaultGrantTimeDescriptor(t *testing.T) {
	for _, action := range []string{"SET DEFAULT 7", "DROP DEFAULT"} {
		for _, kind := range []string{"revision", "shadow", "reset", "restore", "recreate"} {
			t.Run(action+"/"+kind, func(t *testing.T) {
				e := New()
				a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
				sessionSQL(t, a, "CREATE SCHEMA shadow")
				sessionSQL(t, a, "CREATE TABLE t(a INT DEFAULT 11,b INT DEFAULT 13)")
				sessionSQL(t, a, "INSERT INTO t DEFAULT VALUES")
				if kind == "shadow" {
					sessionSQL(t, b, "SET search_path TO shadow,public")
				}
				ref := roleTableRef(t, e, "t")
				tx := transactionBegin(t, a)
				sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
				done := lockAsync(e, b, context.Background(), "ALTER TABLE t ALTER a "+action, false)
				lockPending(t, e, 1)
				switch kind {
				case "revision":
					old := e.catalog.state.MustRelation(ref.Address)
					next := *old
					next.revision++
					next.comment = "retained"
					stage := e.BeginCatalogStage(e.CatalogRead())
					defer e.DiscardCatalogStage(stage)
					if err := stage.Replace(&next); err != nil {
						t.Fatal(err)
					}
					prepared, err := e.ValidateCatalogStage(context.Background(), stage)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: ref.Epoch}); err != nil {
						t.Fatal(err)
					}
				case "shadow":
					run(t, e, "CREATE TABLE shadow.t(a INT DEFAULT 99)")
				case "reset":
					if err := e.Reset(context.Background()); err != nil {
						t.Fatal(err)
					}
				case "restore":
					if err := e.Restore(context.Background(), strings.NewReader("CREATE TABLE t(a INT DEFAULT 99)")); err != nil {
						t.Fatal(err)
					}
				case "recreate":
					run(t, e, "DROP TABLE t")
					run(t, e, "CREATE TABLE t(a INT DEFAULT 99)")
				}
				if kind == "reset" || kind == "restore" {
					lockAwait(t, done, ids.ErrCatalogReplaced)
					transactionEnd(t, a, tx, false)
				} else {
					transactionEnd(t, a, tx, false)
					if kind == "revision" {
						lockAwait(t, done, nil)
						d := e.catalog.state.MustRelation(ref.Address)
						if d.comment != "retained" {
							t.Fatal("lost grant-time revision")
						}
						run(t, e, "INSERT INTO t DEFAULT VALUES")
						got := tuples(run(t, e, "SELECT * FROM t ORDER BY a"))
						if len(got) != 2 {
							t.Fatal(got)
						}
						if action == "SET DEFAULT 7" {
							if got[0][0] != int64(7) || got[0][1] != int64(13) || got[1][0] != int64(11) {
								t.Fatal(got)
							}
						} else if got[0][0] != int64(11) || got[1][0] != nil || got[1][1] != int64(13) {
							t.Fatal(got)
						}
					} else {
						lockAwait(t, done, ids.ErrCatalogReplaced)
					}
				}
				lockEmpty(t, e)
			})
		}
	}
}

func TestAlterDefaultProviderDropFences(t *testing.T) {
	for _, kind := range []string{"cancel", "descriptor", "insert"} {
		t.Run(kind, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE t(a INT DEFAULT 11)")
			run(t, e, "INSERT INTO t DEFAULT VALUES")
			ref := roleTableRef(t, e, "t")
			old := e.catalog.state.MustRelation(ref.Address)
			storage := e.storage[ref.Address]
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			provider := e.catalog.providers[ids.RelationClassID]
			op, cleanup, err := testOperation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			e.catalog.providers[ids.RelationClassID] = alterDropProvider{provider, func() {
				e.catalog.providers[ids.RelationClassID] = provider
				switch kind {
				case "cancel":
					cancel()
				case "descriptor":
					next := *old
					next.revision++
					next.comment = "changed"
					stage := e.BeginCatalogStage(e.CatalogRead())
					defer e.DiscardCatalogStage(stage)
					if err := stage.Replace(&next); err != nil {
						t.Fatal(err)
					}
					prepared, err := e.ValidateCatalogStage(context.Background(), stage)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: ref.Epoch}); err != nil {
						t.Fatal(err)
					}
				case "insert":
					e.mu.Lock()
					row := storage.versions[0]
					row.row = 99
					row.version = 99
					storage.versions = append(storage.versions, row)
					e.mu.Unlock()
				}
			}}
			_, err = e.Execute(op, statement(t, "ALTER TABLE t ALTER a DROP DEFAULT"), 0, nil)
			if err == nil {
				t.Fatal("stale DROP published")
			}
			current := e.catalog.state.MustRelation(ref.Address)
			if current.defaults[current.attributes[0]].expression != old.defaults[old.attributes[0]].expression || e.storage[ref.Address] != storage {
				t.Fatal("failed DROP removed definition")
			}
			lockEmpty(t, e)
		})
	}
}
