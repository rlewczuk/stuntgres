package fixture

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"os"
	"strings"
	"stuntgres"
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
	tracking := "off"
	if trackCommitTimestamp {
		tracking = "on"
	}
	args := []string{"-c", "max_prepared_transactions=100",
		"-c", "log_line_prefix=timeout-oracle|%p|%c|%e|",
		"-c", "track_commit_timestamp=" + tracking}
	if logicalWAL {
		args = append(args, "-c", "wal_level=logical")
	}
	if alterSystemDisabled {
		args = append(args, "-c", "allow_alter_system=off")
	}
	var preload []string
	if pgStatStatements {
		preload = append(preload, "pg_stat_statements")
		args = append(args, "-c", "compute_query_id=on", "-c", "pg_stat_statements.max=5000")
	}
	if passwordCheck {
		preload = append(preload, "passwordcheck")
	}
	if len(preload) != 0 {
		args = append(args, "-c", "shared_preload_libraries="+strings.Join(preload, ","))
	}
	options := []testcontainers.ContainerCustomizer{
		postgres.WithDatabase("stuntgres_reference"), postgres.WithUsername("stuntgres"),
		postgres.WithPassword("stuntgres"), postgres.BasicWaitStrategies(),
		testcontainers.WithCmdArgs(args...),
		testcontainers.WithEnv(map[string]string{"POSTGRES_INITDB_ARGS": "--locale=C --encoding=UTF8"}),
	}
	options = append(options, testcontainers.WithFiles(thesaurusOracleFiles()...))
	if legacyCryptoProvider {
		const path = "/tmp/stuntgres-openssl.cnf"
		const config = `openssl_conf = openssl_init
[openssl_init]
providers = provider_sect
[provider_sect]
default = default_sect
legacy = legacy_sect
[default_sect]
activate = 1
[legacy_sect]
activate = 1
`
		options = append(options,
			testcontainers.WithEnv(map[string]string{"OPENSSL_CONF": path}),
			testcontainers.WithFiles(testcontainers.ContainerFile{
				Reader: strings.NewReader(config), ContainerFilePath: path, FileMode: 0o644,
			}))
	}
	Container, err = postgres.Run(ctx, referenceImage, options...)
	if err != nil {
		return fmt.Errorf("container: %w", err)
	}
	ContainerID = Container.GetContainerID()
	fmt.Fprintf(os.Stderr, "reference container: %s\n", ContainerID)
	if hostPython {
		if err := provisionHostPython(ctx); err != nil {
			return err
		}
	}
	if err := referenceInstallTextSearchFiles(ctx); err != nil {
		return err
	}
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
	var alterSystem string
	if err = Admin.QueryRowContext(ctx, "SHOW allow_alter_system").Scan(&alterSystem); err != nil {
		return fmt.Errorf("allow_alter_system: %w", err)
	}
	wantAlterSystem := "on"
	if alterSystemDisabled {
		wantAlterSystem = "off"
	}
	if alterSystem != wantAlterSystem {
		return fmt.Errorf("allow_alter_system=%q, want %q", alterSystem, wantAlterSystem)
	}
	fmt.Fprintf(os.Stderr, "reference allow_alter_system=%s\n", alterSystem)
	var version, number, encoding, collate, ctype, messages, preparedCapacity, tracking string
	if err = Admin.QueryRowContext(ctx, "SELECT current_setting('server_version'), current_setting('server_version_num'), current_setting('server_encoding'), datcollate, datctype, current_setting('lc_messages'), current_setting('max_prepared_transactions'), current_setting('track_commit_timestamp') FROM pg_database WHERE datname = current_database()").Scan(&version, &number, &encoding, &collate, &ctype, &messages, &preparedCapacity, &tracking); err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	wantTracking := "off"
	if trackCommitTimestamp {
		wantTracking = "on"
	}
	if (version != "18.6" && !strings.HasPrefix(version, "18.6 ")) || number != "180006" || encoding != "UTF8" || collate != "C" || ctype != "C" || messages != "C" || preparedCapacity != "100" || tracking != wantTracking {
		return fmt.Errorf("configuration mismatch: version=%q number=%q encoding=%q collate=%q ctype=%q messages=%q max_prepared_transactions=%q track_commit_timestamp=%q (want PostgreSQL 18.6/180006 UTF8/C/C/C max_prepared_transactions=100 track_commit_timestamp=%q)", version, number, encoding, collate, ctype, messages, preparedCapacity, tracking, wantTracking)
	}
	if pgStatStatements {
		var preload, queryID, statementCapacity string
		if err = Admin.QueryRowContext(ctx, "SELECT current_setting('shared_preload_libraries'), current_setting('compute_query_id'), current_setting('pg_stat_statements.max')").Scan(&preload, &queryID, &statementCapacity); err != nil {
			return fmt.Errorf("pg_stat_statements configuration: %w", err)
		}
		if !referencePreloadContains(preload, "pg_stat_statements") || queryID != "on" || statementCapacity != "5000" {
			return fmt.Errorf("pg_stat_statements configuration mismatch: shared_preload_libraries=%q compute_query_id=%q pg_stat_statements.max=%q (want pg_stat_statements token/on/5000)", preload, queryID, statementCapacity)
		}
		fmt.Fprintf(os.Stderr, "reference pg_stat_statements: shared_preload_libraries=%s compute_query_id=%s max=%s\n", preload, queryID, statementCapacity)
	}
	if passwordCheck {
		var preload, shown, setting, unit, context, kind, boot, minimum, maximum, description string
		if err = Admin.QueryRowContext(ctx, "SELECT current_setting('shared_preload_libraries'), current_setting('passwordcheck.min_password_length'), setting, unit, context, vartype, boot_val, min_val, max_val, short_desc FROM pg_settings WHERE name='passwordcheck.min_password_length'").Scan(&preload, &shown, &setting, &unit, &context, &kind, &boot, &minimum, &maximum, &description); err != nil {
			return fmt.Errorf("passwordcheck configuration: %w", err)
		}
		if !referencePreloadContains(preload, "passwordcheck") || shown != "8B" || setting != "8" || unit != "B" || context != "superuser" || kind != "integer" || boot != "8" || minimum != "0" || maximum != "2147483647" || description != "Minimum allowed password length." {
			return fmt.Errorf("passwordcheck configuration mismatch: preload=%q shown=%q setting=%q unit=%q context=%q vartype=%q boot=%q min=%q max=%q description=%q", preload, shown, setting, unit, context, kind, boot, minimum, maximum, description)
		}
		fmt.Fprintf(os.Stderr, "reference passwordcheck: shared_preload_libraries=%s minimum=%s unit=%s context=%s\n", preload, setting, unit, context)
	}
	// Native dumps name their bootstrap owner postgres, independently of initdb.
	if _, err = Admin.ExecContext(ctx, "CREATE ROLE postgres SUPERUSER NOLOGIN"); err != nil {
		return fmt.Errorf("native bootstrap owner: %w", err)
	}
	fmt.Fprintf(os.Stderr, "reference oracle: container=%s image=%s server_version=%q server_version_num=%s server_encoding=%s datcollate=%s datctype=%s lc_messages=%s\n", ContainerID, referenceImage, version, number, encoding, collate, ctype, messages)
	return nil
}

// Both supported names are unquoted identifiers in the fixture-owned GUC list.
// Compare tokens, not the whole string, so startup options genuinely compose.
func referencePreloadContains(value, module string) bool {
	for _, token := range strings.Split(value, ",") {
		if strings.TrimSpace(token) == module {
			return true
		}
	}
	return false
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
		// A hang guard: Docker teardown under parallel suites can take minutes.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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

// NewScenarioWithDriverConfig configures the scenario's private native Driver
// before its first engine is constructed. All ordinary fixture ownership,
// middleware, oracle and reset behavior is identical to NewScenario.
func NewScenarioWithDriverConfig(t *testing.T, configure func(*stuntgres.Driver) error) *Pair {
	t.Helper()
	return newFixture(t, *Mode, true, configure)
}
