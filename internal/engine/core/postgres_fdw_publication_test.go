package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqltype"
)

type postgresPublicationRejector struct {
	entered chan context.Context
	failure error
}

func (p *postgresPublicationRejector) Open(ctx context.Context, _ catalog.ExternalSQLConnectRequest) (catalog.ExternalSQLConnection, error) {
	p.entered <- ctx
	<-ctx.Done()
	return nil, p.failure
}

func TestPostgresFDWPublication(t *testing.T) {
	for _, boundary := range []string{"commit", "mapping_commit", "mapping_remove", "mapping_private_then_commit", "failed", "unrelated", "unrelated_commit", "new_server", "new_mapping", "new_server_commit", "new_mapping_commit", "private_then_commit", "private_then_rollback", "replacement", "canceled_replacement"} {
		t.Run(boundary, func(t *testing.T) {
			e := engine.New()
			failure := errors.New("negative-only publication transport rejection")
			provider := &postgresPublicationRejector{entered: make(chan context.Context, 1), failure: failure}
			bindings := &catalog.ExternalSQLBindings{PostgresFDW: &catalog.ExternalSQLProviderDefinition{Name: "publication-unit", Version: "1", Provider: provider}}
			if err := e.InstallForeignDefinitions(bindings, nil, nil,
				func(context.Context, driver.Value, sqltype.Usage, sqltype.InputEnvironment) (sqltype.Value, error) {
					return sqltype.Value{}, errors.New("negative-only unit imported a row")
				},
				func(context.Context, sqltype.Value, sqltype.OutputEnvironment) (driver.Value, error) {
					return nil, errors.New("negative-only unit exported a parameter")
				}); err != nil {
				t.Fatal(err)
			}
			// SQL only reaches the real installed gateway and publication owners.
			// Consumer SQL rows/diagnostics remain solely in reference scenarios.
			for _, q := range []string{"CREATE EXTENSION postgres_fdw", "CREATE SERVER publication_server FOREIGN DATA WRAPPER postgres_fdw", "CREATE USER MAPPING FOR CURRENT_USER SERVER publication_server", "CREATE FOREIGN TABLE publication_table(id integer) SERVER publication_server"} {
				enginetest.Run(t, e, q)
			}
			consumer := enginetest.StartSession(t, e, nil)
			peer := enginetest.StartSession(t, e, nil)
			queryCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			op, err := consumer.Acquire(queryCtx)
			if err != nil {
				t.Fatal(err)
			}
			statement := enginetest.Statement(t, "SELECT id FROM publication_table")
			done := make(chan error, 1)
			joined := make(chan struct{})
			go func() {
				defer close(joined)
				defer op.Done()
				result, err := e.Execute(op, statement, 0, nil)
				if result.Rows != nil {
					result.Rows.Close()
					err = errors.New("negative-only gateway published rows")
				}
				done <- err
			}()
			defer func() {
				cancel()
				guard, stop := context.WithTimeout(context.Background(), 10*time.Second)
				defer stop()
				select {
				case <-joined:
				case <-guard.Done():
					t.Error("publication gateway worker did not join")
				}
			}()
			var captured context.Context
			select {
			case captured = <-provider.entered:
			case err := <-done:
				t.Fatal("gateway did not own pending Open", err)
			case <-queryCtx.Done():
				t.Fatal(queryCtx.Err())
			}
			unchanged := func() {
				t.Helper()
				if captured.Err() != nil {
					t.Fatal("unpublished/failed candidate invalidated peer pending ownership", captured.Err())
				}
				select {
				case err := <-done:
					t.Fatal("peer Open ended without a published invalidation", err)
				default:
				}
			}
			switch boundary {
			case "commit":
				enginetest.SessionSQL(t, peer, "ALTER SERVER publication_server OPTIONS(ADD application_name 'committed')")
			case "mapping_commit":
				enginetest.SessionSQL(t, peer, "ALTER USER MAPPING FOR CURRENT_USER SERVER publication_server OPTIONS(ADD user 'changed')")
			case "mapping_remove":
				enginetest.SessionSQL(t, peer, "DROP USER MAPPING FOR CURRENT_USER SERVER publication_server")
			case "unrelated", "new_server", "new_mapping":
				query := "CREATE SCHEMA publication_unrelated"
				if boundary == "new_server" {
					query = "CREATE SERVER publication_new FOREIGN DATA WRAPPER postgres_fdw"
				}
				if boundary == "new_mapping" {
					query = "CREATE USER MAPPING FOR PUBLIC SERVER publication_server"
				}
				enginetest.SessionSQL(t, peer, query)
				unchanged()
				cancel()
			case "unrelated_commit", "new_server_commit", "new_mapping_commit":
				enginetest.SessionSQL(t, peer, "BEGIN")
				query := "CREATE SCHEMA publication_unrelated"
				if boundary == "new_server_commit" {
					query = "CREATE SERVER publication_new FOREIGN DATA WRAPPER postgres_fdw"
				}
				if boundary == "new_mapping_commit" {
					query = "CREATE USER MAPPING FOR PUBLIC SERVER publication_server"
				}
				enginetest.SessionSQL(t, peer, query)
				unchanged()
				enginetest.SessionSQL(t, peer, "COMMIT")
				unchanged()
				cancel()
			case "failed":
				peerOp, err := peer.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				_, rejected := e.Execute(peerOp, enginetest.Statement(t, "ALTER SERVER publication_server OPTIONS(ADD fetch_size '0')"), 0, nil)
				peerOp.Done()
				if rejected == nil {
					t.Fatal("invalid candidate reached publication")
				}
				unchanged()
				cancel()
			case "private_then_commit", "private_then_rollback", "mapping_private_then_commit":
				enginetest.SessionSQL(t, peer, "BEGIN")
				query := "ALTER SERVER publication_server OPTIONS(ADD application_name 'private')"
				if boundary == "mapping_private_then_commit" {
					query = "DROP USER MAPPING FOR CURRENT_USER SERVER publication_server"
				}
				enginetest.SessionSQL(t, peer, query)
				unchanged()
				if boundary != "private_then_rollback" {
					enginetest.SessionSQL(t, peer, "COMMIT")
				} else {
					enginetest.SessionSQL(t, peer, "ROLLBACK")
					unchanged()
					cancel()
				}
			case "replacement":
				if err := engine.Reset(context.Background(), e); err != nil {
					t.Fatal(err)
				}
			case "canceled_replacement":
				canceled, stop := context.WithCancel(context.Background())
				stop()
				if err := engine.Reset(canceled, e); !errors.Is(err, context.Canceled) {
					t.Fatal("canceled replacement admission", err)
				}
				unchanged()
				cancel()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("negative-only transport acquired execution authority")
				}
				if boundary == "commit" || boundary == "mapping_commit" || boundary == "mapping_remove" || boundary == "mapping_private_then_commit" || boundary == "private_then_commit" || boundary == "replacement" {
					if captured.Err() == nil {
						t.Fatal("actual publication did not cancel pending child")
					}
					if !errors.Is(err, failure) && !errors.Is(err, context.Canceled) {
						t.Fatal("publication lost host/cancellation identity", err)
					}
				}
			case <-time.After(10 * time.Second):
				t.Fatal("pending generation did not settle")
			}
			if core.EngineOf(peer) != e || core.EngineOf(consumer) != e {
				t.Fatal("publication transferred physical session ownership")
			}
		})
	}
}
