package fixture

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"os"
	"testing"
	"time"
)

// One suite-owned client reads bounded timeout-oracle log suffixes. Individual
// responses retain their own synchronous read/close ownership.
var LogClient *testcontainers.DockerClient

func EnsureStarted() error {
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
		Started = referenceStartupErr == nil
	})
	return referenceStartupErr
}

func referenceStart() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var err error
	LogClient, err = testcontainers.NewDockerClientWithOpts(ctx)
	if err != nil {
		return fmt.Errorf("oracle Docker client: %w", err)
	}
	Container, err = postgres.Run(ctx, referenceImage,
		postgres.WithDatabase("stuntgres_reference"), postgres.WithUsername("stuntgres"),
		postgres.WithPassword("stuntgres"), postgres.BasicWaitStrategies(),
		testcontainers.WithCmdArgs("-c", "max_prepared_transactions=100",
			"-c", "log_line_prefix=timeout-oracle|%p|%c|%e|"),
		testcontainers.WithEnv(map[string]string{"POSTGRES_INITDB_ARGS": "--locale=C --encoding=UTF8"}))
	if err != nil {
		return fmt.Errorf("container: %w", err)
	}
	ContainerID = Container.GetContainerID()
	fmt.Fprintf(os.Stderr, "reference container: %s\n", ContainerID)
	DSN, err = Container.ConnectionString(ctx, "sslmode=disable", "application_name=")
	if err != nil {
		return fmt.Errorf("connection string: %w", err)
	}
	Admin, err = sql.Open("postgres", DSN)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	// Retain one sanitizable idle admin session, without limiting concurrent
	// observers while a replay holds a borrowed connection.
	Admin.SetMaxIdleConns(1)
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
	if err = Admin.PingContext(ctx); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	var version, encoding, locale, preparedCapacity string
	if err = Admin.QueryRowContext(ctx, "SELECT current_setting('server_version_num'), current_setting('server_encoding'), datcollate, current_setting('max_prepared_transactions') FROM pg_database WHERE datname = current_database()").Scan(&version, &encoding, &locale, &preparedCapacity); err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	if version != "180000" || encoding != "UTF8" || locale != "C" || preparedCapacity != "100" {
		return fmt.Errorf("configuration mismatch: version=%q encoding=%q locale=%q max_prepared_transactions=%q", version, encoding, locale, preparedCapacity)
	}
	// Native dumps name their bootstrap owner postgres, independently of initdb.
	if _, err = Admin.ExecContext(ctx, "CREATE ROLE postgres SUPERUSER NOLOGIN"); err != nil {
		return fmt.Errorf("native bootstrap owner: %w", err)
	}
	return nil
}

// Release ownership before cleanup so a failed startup cannot leave resources
// for test callbacks or cause final teardown to close/terminate them twice.
func referenceReleaseResources() error {
	admin, logClient, container := Admin, LogClient, Container
	Admin, LogClient, Container = nil, nil, nil
	DSN, ContainerID = "", ""
	Started = false
	var cleanupErr error
	if admin != nil {
		if err := admin.Close(); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("reference close: %w", err))
		}
	}
	if logClient != nil {
		if err := logClient.Close(); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("reference log client close: %w", err))
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

type Backend struct {
	Name     string
	DB       *sql.DB
	Recorder *Recorder
	PG       bool
}

func (p *Pair) Backends() []Backend {
	backends := []Backend{{"memory", p.Memory, p.MemoryRecord, false}}
	if p.Postgres != nil {
		backends = append(backends, Backend{"postgres", p.Postgres, p.PostgresRecord, true})
	}
	return backends
}

func NewScenario(t *testing.T) *Pair {
	t.Helper()
	return NewFixture(t, *Mode, true)
}
