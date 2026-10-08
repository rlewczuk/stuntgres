package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func referenceEnsureStarted() error {
	referenceStartup.Do(func() {
		defer func() {
			if p := recover(); p != nil {
				referenceStartupErr = fmt.Errorf("startup panic: %v", p)
			}
		}()
		referenceStartupErr = referenceStart()
	})
	return referenceStartupErr
}

func referenceStart() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var err error
	referenceContainer, err = postgres.Run(ctx, referenceImage,
		postgres.WithDatabase("stuntgres_reference"), postgres.WithUsername("stuntgres"),
		postgres.WithPassword("stuntgres"), postgres.BasicWaitStrategies(),
		testcontainers.WithEnv(map[string]string{"POSTGRES_INITDB_ARGS": "--locale=C --encoding=UTF8"}))
	if err != nil {
		return fmt.Errorf("container: %w", err)
	}
	referenceContainerID = referenceContainer.GetContainerID()
	fmt.Fprintf(os.Stderr, "reference container: %s\n", referenceContainerID)
	referenceDSN, err = referenceContainer.ConnectionString(ctx, "sslmode=disable", "application_name=")
	if err != nil {
		return fmt.Errorf("connection string: %w", err)
	}
	referenceAdmin, err = sql.Open("postgres", referenceDSN)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	if err = referenceAdmin.PingContext(ctx); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	var version, encoding, locale string
	if err = referenceAdmin.QueryRowContext(ctx, "SELECT current_setting('server_version_num'), current_setting('server_encoding'), datcollate FROM pg_database WHERE datname = current_database()").Scan(&version, &encoding, &locale); err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	if version != "180000" || encoding != "UTF8" || locale != "C" {
		return fmt.Errorf("configuration mismatch: version=%q encoding=%q locale=%q", version, encoding, locale)
	}
	// Native dumps name their bootstrap owner postgres, independently of initdb.
	if _, err = referenceAdmin.ExecContext(ctx, "CREATE ROLE postgres SUPERUSER NOLOGIN"); err != nil {
		return fmt.Errorf("native bootstrap owner: %w", err)
	}
	return nil
}

type referenceBackend struct {
	name string
	db *sql.DB
	recorder *referenceRecorder
	pg bool
}

func (p *referencePair) backends() []referenceBackend {
	backends := []referenceBackend{{"memory", p.memory, p.memoryRecord, false}}
	if p.postgres != nil {
		backends = append(backends, referenceBackend{"postgres", p.postgres, p.postgresRecord, true})
	}
	return backends
}

func newReferenceScenario(t *testing.T) *referencePair {
	t.Helper()
	return newReferenceFixture(t, *referenceMode, true)
}
