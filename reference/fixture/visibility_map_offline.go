package fixture

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// OfflineVisibilityMap owns one oracle heap's stopped-file witness and recovery.
// The package is serialized. All callers must return every PG borrower (including
// Rows, Stmt, Tx, Conn and COPY workers) before Snapshot or InstallStale. Backend
// values must be reacquired afterward: the same container may get a new TCP port.
// Native pools and both recorders retain their original ownership throughout.
type OfflineVisibilityMap struct {
	pair    *Pair
	file    string
	healthy []byte
	restore bool
}

func NewOfflineVisibilityMap(t *testing.T, p *Pair, relation string) *OfflineVisibilityMap {
	t.Helper()
	if Container == nil || p.Postgres == nil || Container.GetContainerID() != ContainerID {
		t.Fatal("offline VM requires the existing oracle")
	}
	var directory, relative string
	if err := Admin.QueryRowContext(p.Ctx, "SELECT current_setting('data_directory'),pg_relation_filepath($1::regclass)", relation).Scan(&directory, &relative); err != nil {
		t.Fatal(err)
	}
	if !path.IsAbs(directory) || path.IsAbs(relative) || relative == "" || path.Clean(relative) != relative || strings.HasPrefix(relative, "../") {
		t.Fatal("unsafe owned VM path", directory, relative)
	}
	o := &OfflineVisibilityMap{pair: p, file: path.Join(directory, relative) + "_vm"}
	// Registered after Pair cleanup: recover first, then the ordinary fixture
	// closes the CURRENT pools and resets the CURRENT admin connection.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), ScenarioBudget)
		defer cancel()
		if o.restore {
			err := o.offline(ctx, func(ctx context.Context) error { return Container.CopyToContainer(ctx, o.healthy, o.file, 0o600) })
			if err != nil {
				t.Errorf("restore healthy offline VM: %v", err)
			} else {
				o.restore = false
			}
		} else if err := o.ensureOnline(ctx); err != nil {
			t.Errorf("recover oracle: %v", err)
		}
	})
	return o
}

// Snapshot reads a bounded archive from the cleanly stopped original container.
func (o *OfflineVisibilityMap) Snapshot(ctx context.Context) (data []byte, err error) {
	err = o.offline(ctx, func(ctx context.Context) error { var readErr error; data, readErr = o.read(ctx); return readErr })
	return
}

// InstallStale saves the healthy post-DML bytes BEFORE any stale write attempt.
// Cleanup never mistakes the pre-DELETE frozen snapshot for healthy recovery.
func (o *OfflineVisibilityMap) InstallStale(ctx context.Context, stale []byte) error {
	if len(stale) == 0 || len(stale) > 1<<20 {
		return fmt.Errorf("invalid stale VM size %d", len(stale))
	}
	return o.offline(ctx, func(ctx context.Context) error {
		healthy, err := o.read(ctx)
		if err != nil {
			return err
		}
		o.healthy = healthy
		o.restore = true
		return Container.CopyToContainer(ctx, stale, o.file, 0o600)
	})
}

func (o *OfflineVisibilityMap) read(ctx context.Context) ([]byte, error) {
	r, err := Container.CopyFileFromContainer(ctx, o.file)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(r, (1<<20)+1))
	closeErr := r.Close()
	if len(data) > 1<<20 {
		readErr = errors.Join(readErr, fmt.Errorf("VM archive exceeds 1 MiB"))
	}
	return data, errors.Join(readErr, closeErr)
}

func (o *OfflineVisibilityMap) offline(ctx context.Context, operation func(context.Context) error) (err error) {
	ctx, cancel := context.WithTimeout(ctx, ScenarioBudget)
	defer cancel()
	if Container.GetContainerID() != ContainerID {
		return fmt.Errorf("oracle container identity changed")
	}
	for _, db := range []*sql.DB{o.pair.Postgres, o.pair.CopyPostgres, Admin} {
		if db != nil && db.Stats().InUse != 0 {
			return fmt.Errorf("offline VM has outstanding oracle borrowers")
		}
	}
	// Reconnection is attempted even after a failed stop/copy, independently of
	// the caller's potentially canceled context. Never bootstrap a second time.
	defer func() {
		recovery, cancel := context.WithTimeout(context.Background(), ScenarioBudget)
		defer cancel()
		err = errors.Join(err, o.ensureOnline(recovery))
	}()
	for _, db := range []*sql.DB{o.pair.Postgres, o.pair.CopyPostgres, Admin} {
		if db != nil {
			err = errors.Join(err, db.Close())
		}
	}
	if err != nil {
		return err
	}
	state, stateErr := Container.State(ctx)
	if stateErr != nil {
		return stateErr
	}
	if state.Running {
		timeout := -time.Second
		if err := Container.Stop(ctx, &timeout); err != nil {
			return err
		}
	}
	state, err = Container.State(ctx)
	if err != nil {
		return err
	}
	if state.Running || state.ExitCode != 0 || state.OOMKilled || state.Error != "" {
		return fmt.Errorf("unclean oracle stop: %+v", state)
	}
	return operation(ctx)
}

func (o *OfflineVisibilityMap) ensureOnline(ctx context.Context) error {
	if Container.GetContainerID() != ContainerID {
		return fmt.Errorf("oracle container identity changed")
	}
	state, err := Container.State(ctx)
	if err != nil {
		return err
	}
	if !state.Running {
		if err := Container.Start(ctx); err != nil {
			return err
		}
	}
	// Start reruns the stock root entrypoint, which restores PGDATA ownership
	// after Docker's stopped archive copy (uid 0, mode 0600), before postgres.
	dsn, err := Container.ConnectionString(ctx, "sslmode=disable", "application_name=")
	if err != nil {
		return err
	}
	for _, db := range []*sql.DB{o.pair.Postgres, o.pair.CopyPostgres, Admin} {
		if db != nil {
			if db.Stats().InUse != 0 {
				return fmt.Errorf("reconnect has outstanding oracle borrowers")
			}
			if err := db.Close(); err != nil {
				return err
			}
		}
	}
	DSN = dsn
	Admin, err = sql.Open("postgres", DSN)
	if err != nil {
		return err
	}
	Admin.SetMaxIdleConns(1)
	o.pair.Postgres, err = sql.Open("stuntgres_reference_postgres", DSN)
	if err != nil {
		return err
	}
	config, err := pgx.ParseConfig(DSN)
	if err != nil {
		return err
	}
	o.pair.CopyPostgres = sql.OpenDB(stdlib.GetConnector(*config))
	for _, db := range []*sql.DB{Admin, o.pair.Postgres, o.pair.CopyPostgres} {
		if err := db.PingContext(ctx); err != nil {
			return err
		}
	}
	var version int
	if err := Admin.QueryRowContext(ctx, "SHOW server_version_num").Scan(&version); err != nil {
		return err
	}
	if version != 180006 {
		return fmt.Errorf("restarted oracle version %d, expected 180006", version)
	}
	return nil
}
