package reference_test

import (
	"context"
	"database/sql"
	"errors"
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
			if referenceStartupErr != nil {
				referenceStartupErr = errors.Join(referenceStartupErr, referenceReleaseResources())
			}
		}()
		referenceStartupErr = referenceStart()
		referenceStarted = referenceStartupErr == nil
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
		testcontainers.WithCmdArgs("-c", "max_prepared_transactions=100",
			"-c", "log_line_prefix=timeout-oracle|%p|%e|"),
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
	return referenceInitialize(ctx)
}

// Initialization owns acquired resources until all server preconditions pass.
// Its failure path also applies when a real initialization step panics.
func referenceInitialize(ctx context.Context) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("initialization panic: %v", p)
		}
		if err != nil {
			err = errors.Join(err, referenceReleaseResources())
		}
	}()
	if err = referenceAdmin.PingContext(ctx); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	var version, encoding, locale, preparedCapacity string
	if err = referenceAdmin.QueryRowContext(ctx, "SELECT current_setting('server_version_num'), current_setting('server_encoding'), datcollate, current_setting('max_prepared_transactions') FROM pg_database WHERE datname = current_database()").Scan(&version, &encoding, &locale, &preparedCapacity); err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	if version != "180000" || encoding != "UTF8" || locale != "C" || preparedCapacity != "100" {
		return fmt.Errorf("configuration mismatch: version=%q encoding=%q locale=%q max_prepared_transactions=%q", version, encoding, locale, preparedCapacity)
	}
	// Native dumps name their bootstrap owner postgres, independently of initdb.
	if _, err = referenceAdmin.ExecContext(ctx, "CREATE ROLE postgres SUPERUSER NOLOGIN"); err != nil {
		return fmt.Errorf("native bootstrap owner: %w", err)
	}
	return nil
}

// Release ownership before cleanup so a failed startup cannot leave resources
// for test callbacks or cause final teardown to close/terminate them twice.
func referenceReleaseResources() error {
	admin, container := referenceAdmin, referenceContainer
	referenceAdmin, referenceContainer = nil, nil
	referenceDSN, referenceContainerID = "", ""
	referenceStarted = false
	var cleanupErr error
	if admin != nil {
		if err := admin.Close(); err != nil {
			cleanupErr = fmt.Errorf("reference close: %w", err)
		}
	}
	if container != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if err := container.Terminate(ctx); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("reference terminate: %w", err))
		}
	}
	return cleanupErr
}

type referenceBackend struct {
	name     string
	db       *sql.DB
	recorder *referenceRecorder
	pg       bool
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
