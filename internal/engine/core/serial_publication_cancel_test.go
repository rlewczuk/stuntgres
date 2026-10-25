package core_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func TestSerialDefaultPublicationReplacementCancellation(t *testing.T) {
	for _, alter := range []bool{false, true} {
		for _, replacement := range []bool{false, true} {
			t.Run(fmt.Sprintf("alter=%t/replacement=%t", alter, replacement), func(t *testing.T) {
				e := engine.New()
				enginetest.Run(t, e, `CREATE SEQUENCE q`)
				if alter {
					enginetest.Run(t, e, `CREATE TABLE consumer(id SERIAL)`)
					enginetest.Run(t, e, `INSERT INTO consumer VALUES(40)`)
				}
				before := e.Catalog.State
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				reset := make(chan error, 1)
				var once sync.Once
				provider := e.Catalog.Providers[ids.RelationClassID]
				e.Catalog.Providers[ids.RelationClassID] = alterDropProvider{provider, func() {
					once.Do(func() {
						e.Catalog.Providers[ids.RelationClassID] = provider
						if replacement {
							go func() { reset <- engine.Reset(context.Background(), e) }()
							deadline := time.Now().Add(5 * time.Second)
							for {
								core.ReplacementAdmissionMuRef(&e.ReplacementMu).Lock()
								waiting := core.ReplacementAdmissionWriters(&e.ReplacementMu) == 1 && core.ReplacementAdmissionReaders(&e.ReplacementMu) == 1 && !core.ReplacementAdmissionWriting(&e.ReplacementMu)
								core.ReplacementAdmissionMuRef(&e.ReplacementMu).Unlock()
								if waiting {
									break
								}
								if time.Now().After(deadline) {
									cancel()
									t.Fatal("replacement did not queue behind unpublished definition provider hold")
								}
								runtime.Gosched()
							}
							if e.Catalog.State != before {
								t.Fatal("queued replacement changed catalog before publication released its hold")
							}
						}
						cancel()
					})
				}}
				_, err := enginetest.Execute(e, ctx, enginetest.Statement(t, serialPublicationQuery(alter)), nil)
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err, context.Canceled)
				}
				if replacement {
					select {
					case err := <-reset:
						if err != nil {
							t.Fatal(err)
						}
					case <-time.After(5 * time.Second):
						t.Fatal("canceled definition did not release replacement admission")
					}
					if e.Catalog.State.Epoch() == before.Epoch() {
						t.Fatal("released replacement did not fence the old definition epoch")
					}
				}
				if !replacement && e.Catalog.State != before {
					t.Fatal("canceled default publication changed catalog")
				}
				enginetest.LockEmpty(t, e)
				checkReviewDependencyValid(t, e)
				deleteReplacementAdmissionEmpty(t, e)
			})
		}
	}
}

func serialPublicationQuery(alter bool) string {
	if alter {
		return `ALTER TABLE consumer ALTER id SET DEFAULT nextval('q')`
	}
	return `CREATE TABLE consumer(id INT DEFAULT nextval('q'))`
}
