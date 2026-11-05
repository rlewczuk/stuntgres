package core_test

import (
	"context"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/logical"
	"stuntgres/internal/engine/logical/testdecoding"
	"stuntgres/internal/parser"
)

type databaseDropPlugin struct {
	logical.Plugin
	startup          bool
	entered, release chan struct{}
	once             *sync.Once
}

func (p *databaseDropPlugin) Startup(s logical.Startup) (logical.Options, error) {
	if p.startup {
		p.once.Do(func() { close(p.entered); <-p.release })
	}
	return p.Plugin.Startup(s)
}
func (p *databaseDropPlugin) Begin(o *logical.Output, tx *logical.Txn) error {
	if !p.startup {
		p.once.Do(func() { close(p.entered); <-p.release })
	}
	return p.Plugin.Begin(o, tx)
}

func TestDatabaseDropSlots(t *testing.T) {
	for _, creating := range []bool{false, true} {
		name := "persistent_decoder"
		if creating {
			name = "unfinished_creation"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), enginetest.Liveness)
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			var callback, unblock sync.Once
			factory := func() logical.Plugin {
				return &databaseDropPlugin{Plugin: testdecoding.New(), startup: creating, entered: entered, release: release, once: &callback}
			}
			e := engine.NewWithStartup(nil, "drop_slot_default", core.Startup{LogicalWAL: true, OutputPlugins: map[string]logical.Factory{"test_decoding": factory}})
			admin := enginetest.StartSession(t, e, nil)
			if _, err := databaseDropOwnerExecute(ctx, e, admin, &parser.CreateDatabaseStmt{Name: "drop_owner_target"}); err != nil {
				t.Fatal(err)
			}
			target, err := e.SelectDatabase(ctx, "drop_owner_target")
			if err != nil {
				t.Fatal(err)
			}
			producer := enginetest.StartSession(t, target, nil)
			enginetest.SessionSQL(t, producer, "CREATE TABLE drop_slot_rows(v integer)")
			if !creating {
				enginetest.SessionSQL(t, producer, "SELECT pg_create_logical_replication_slot('drop_owner_slot','test_decoding')")
				enginetest.SessionSQL(t, producer, "INSERT INTO drop_slot_rows VALUES(11)")
			}
			query := "SELECT data FROM pg_logical_slot_get_changes('drop_owner_slot',NULL,NULL)"
			if creating {
				query = "SELECT slot_name FROM pg_create_logical_replication_slot('drop_owner_slot','test_decoding')"
			}
			ast := enginetest.Statement(t, query)
			done := make(chan error, 1)
			joined := false
			defer func() {
				unblock.Do(func() { close(release) })
				if !joined {
					<-done
				}
			}()
			go func() {
				result, err := databaseDropOwnerExecute(ctx, target, producer, ast)
				if result.Rows != nil {
					result.Rows.Close()
				}
				done <- err
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			target.Mu.Lock()
			slots, active := target.LogicalSlotCountLocked()
			target.Mu.Unlock()
			if slots != 1 || active != 1 {
				t.Fatal("callback did not hold actual creating/decoder slot", slots, active)
			}
			for _, force := range []bool{false, true} {
				_, err := databaseDropOwnerExecute(ctx, e, admin, databaseDropOwnerStatement(force))
				enginetest.State(t, err, "55006")
				select {
				case <-producer.EndRequested():
					t.Fatal("slot refusal terminated genuine owner")
				default:
				}
			}
			unblock.Do(func() { close(release) })
			decodeErr := databaseDropReceive(t, done)
			joined = true
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			peer := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, producer, "SELECT pg_create_logical_replication_slot('drop_retire_slot','test_decoding')")
			// The genuine public lease holds FORCE in drain. PostgreSQL permits peer
			// acquisition of existing slots despite the database AX grant; use a distinct
			// physical issuer, not the busy DROP session, for these operations.
			lease, err := producer.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Done()
			dropDone := make(chan error, 1)
			dropJoined := false
			defer func() {
				lease.Done()
				if !dropJoined {
					<-dropDone
				}
			}()
			go func() {
				_, err := databaseDropOwnerExecute(ctx, e, admin, databaseDropOwnerStatement(true))
				dropDone <- err
			}()
			select {
			case <-producer.EndRequested():
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			for _, sql := range []string{
				"SELECT pg_replication_slot_advance('drop_owner_slot','FFFFFFFF/FFFFFFFF')",
				"SELECT pg_copy_logical_replication_slot('drop_owner_slot','drop_owner_copy')",
				"SELECT pg_drop_replication_slot('drop_owner_slot')",
			} {
				result, err := databaseDropOwnerExecute(ctx, e, peer, enginetest.Statement(t, sql))
				if result.Rows != nil {
					result.Rows.Close()
				}
				if err != nil {
					t.Fatal("peer operation on existing slot failed during drain", sql, err)
				}
			}
			lease.Done()
			dropErr := databaseDropReceive(t, dropDone)
			dropJoined = true
			if dropErr != nil {
				t.Fatal(dropErr)
			}
			target.Mu.Lock()
			slots, active = target.LogicalSlotCountLocked()
			target.Mu.Unlock()
			if slots != 0 || active != 0 {
				t.Fatal("inactive target slot survived retirement")
			}
			// Registered factories, stream allocator and peer namespace remain usable.
			enginetest.SessionSQL(t, admin, "SELECT pg_create_logical_replication_slot('drop_peer_slot','test_decoding')")
			e.Mu.Lock()
			slots, active = e.LogicalSlotCountLocked()
			e.Mu.Unlock()
			if slots != 2 || active != 0 {
				t.Fatal("DROP destroyed copied or peer slot authority", slots, active)
			}
			enginetest.SessionSQL(t, admin, "SELECT pg_drop_replication_slot('drop_peer_slot')")
			enginetest.SessionSQL(t, admin, "SELECT pg_drop_replication_slot('drop_owner_copy')")
		})
	}
}
