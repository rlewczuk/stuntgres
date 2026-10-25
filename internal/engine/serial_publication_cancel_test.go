package engine

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
)

func TestSerialDefaultPublicationReplacementCancellation(t *testing.T) {
	for _,alter := range []bool{false,true} {
		for _,replacement := range []bool{false,true} {
			t.Run(fmt.Sprintf("alter=%t/replacement=%t",alter,replacement),func(t *testing.T) {
				e := New()
				run(t,e,`CREATE SEQUENCE q`)
				if alter { run(t,e,`CREATE TABLE consumer(id SERIAL)`); run(t,e,`INSERT INTO consumer VALUES(40)`) }
				before := e.Catalog.State
				ctx,cancel := context.WithCancel(context.Background()); defer cancel()
				reset := make(chan error,1)
				var once sync.Once
				provider := e.Catalog.Providers[ids.RelationClassID]
				e.Catalog.Providers[ids.RelationClassID] = alterDropProvider{provider,func(){ once.Do(func(){
					e.Catalog.Providers[ids.RelationClassID] = provider
					if replacement {
						go func(){ reset <- Reset(context.Background(), e) }()
						deadline := time.Now().Add(5*time.Second)
						for {
							e.ReplacementMu.mu.Lock()
							waiting := e.ReplacementMu.writers==1 && e.ReplacementMu.readers==1 && !e.ReplacementMu.writing
							e.ReplacementMu.mu.Unlock()
							if waiting { break }
							if time.Now().After(deadline) {
								cancel()
								t.Fatal("replacement did not queue behind unpublished definition provider hold")
							}
							runtime.Gosched()
						}
						if e.Catalog.State!=before { t.Fatal("queued replacement changed catalog before publication released its hold") }
					}
					cancel()
				}) }}
				_,err := testExecute(e,ctx,statement(t,serialPublicationQuery(alter)),nil)
				if !errors.Is(err,context.Canceled) { t.Fatal(err,context.Canceled) }
				if replacement {
					select {
					case err := <-reset:
						if err!=nil { t.Fatal(err) }
					case <-time.After(5*time.Second):
						t.Fatal("canceled definition did not release replacement admission")
					}
					if e.Catalog.State.Epoch()==before.Epoch() { t.Fatal("released replacement did not fence the old definition epoch") }
				}
				if !replacement && e.Catalog.State != before { t.Fatal("canceled default publication changed catalog") }
				lockEmpty(t,e)
				checkReviewDependencyValid(t,e)
				deleteReplacementAdmissionEmpty(t,e)
			})
		}
	}
}
