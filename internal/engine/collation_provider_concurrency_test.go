package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"sync"
	"testing"
)

// Root runs this genuine publication/validation overlap under the race detector.
// The descriptor and catalog are captured immutable authorities; role commands
// use actual sessions, Execute/StatementScope and ordinary role publication.
func TestCollationProviderConcurrentRolePublication(t *testing.T) {
	e := New()
	owner := sessionStart(t, e, nil)
	sessionSQL(t, owner, `CREATE COLLATION concurrent_provider FROM pg_catalog.pg_unicode_fast`)
	sessionSQL(t, owner, `CREATE ROLE concurrent_provider_role`)
	view := e.CatalogRead()
	address := view.state.names[ids.NameKey{ClassID: ids.CollationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "concurrent_provider"}]
	descriptor := view.state.descriptors[address].(*collationDescriptor)
	e.mu.Lock()
	beforeRoles := e.roles
	e.mu.Unlock()
	commands := []parser.Statement{statement(t, `ALTER ROLE concurrent_provider_role LOGIN`), statement(t, `ALTER ROLE concurrent_provider_role NOLOGIN`)}
	start := make(chan struct{})
	ready := make(chan struct{}, 2)
	errors := make(chan error, 2)
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		ready <- struct{}{}
		<-start
		for i := range 128 {
			op, err := owner.Acquire(context.Background())
			if err != nil {
				errors <- err
				return
			}
			_, err = e.Execute(op, commands[i%len(commands)], 0, nil)
			op.Done()
			if err != nil {
				errors <- fmt.Errorf("actual role publication: %w", err)
				return
			}
		}
	}()
	go func() {
		defer workers.Done()
		ready <- struct{}{}
		<-start
		for range 8192 {
			if err := (collationProvider{}).Validate(view, descriptor); err != nil {
				errors <- fmt.Errorf("captured provider validation: %w", err)
				return
			}
		}
	}()
	<-ready
	<-ready
	close(start)
	workers.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	e.mu.Lock()
	published := e.roles != beforeRoles
	e.mu.Unlock()
	if !published {
		t.Fatal("regression did not publish a genuine successor role catalog")
	}
	if e.CatalogRead().state != view.state || view.state.descriptors[address] != descriptor {
		t.Fatal("role publication unexpectedly changed captured collation snapshot")
	}
}
