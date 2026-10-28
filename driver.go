// Package stuntgres implements a process-local database/sql driver for a bounded
// PostgreSQL SQL subset. Importing it registers the driver name "stuntgres".
package stuntgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"math"
	"runtime"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
	"sync"
)

// Error carries a PostgreSQL diagnostic, including nonfatal notices and warnings.
type Error = sqlstate.Error

// DriverConfig fixes engine startup settings for every database on a Driver.
// The zero value leaves commit timestamp tracking disabled.
type DriverConfig struct {
	TrackCommitTimestamp bool
}

// Driver owns independent clusters keyed by exact, opaque DSN strings.
// Logical databases live for the lifetime of their Driver, not their connections.
type Driver struct {
	mu                sync.Mutex
	engines           map[string]*core.Engine
	lastSession       ids.SessionID
	settings          []settings.Definition
	settingsFrozen    bool
	config            DriverConfig
	foreignWrappers   []fdw.WrapperDefinition
	foreignValidators []fdw.ValidatorDefinition
}

func NewDriver() *Driver { return NewDriverWithConfig(DriverConfig{}) }

// NewDriverWithConfig copies startup configuration; it cannot be changed by SQL.
func NewDriverWithConfig(config DriverConfig) *Driver { return &Driver{config: config} }
func init()                                           { sql.Register("stuntgres", NewDriver()) }
func (d *Driver) Open(name string) (driver.Conn, error) {
	c, err := d.OpenConnector(name)
	if err != nil {
		return nil, err
	}
	return c.Connect(context.Background())
}
func (d *Driver) OpenConnector(name string) (driver.Connector, error) {
	return d.OpenConnectorAs(name, "postgres")
}

// OpenConnectorAs asserts a trusted host-authenticated role, not a password.
// The exact stored role name is revalidated on every physical connection.
func (d *Driver) OpenConnectorAs(name, authenticatedRole string) (driver.Connector, error) {
	if authenticatedRole == "" {
		return nil, errors.New("stuntgres: empty authenticated role")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.engines == nil {
		d.engines = make(map[string]*core.Engine)
	}
	e := d.engines[name]
	if e == nil {
		e = engine.NewWithSettings(d.settings, name, d.config.TrackCommitTimestamp)
		if err := e.InstallForeignDefinitions(d.foreignWrappers, d.foreignValidators, importParameter, exportValue); err != nil {
			return nil, err
		}
		d.settingsFrozen = true
		d.engines[name] = e
	}
	return &Connector{owner: d, engine: e, authenticatedRole: authenticatedRole}, nil
}

// OpenDatabaseConnector selects an existing logical database below the exact
// opaque DSN. Selection is revalidated at every physical connection.
func (d *Driver) OpenDatabaseConnector(dsn, database string) (driver.Connector, error) {
	c, err := d.OpenConnector(dsn)
	if err != nil {
		return nil, err
	}
	return c.(*Connector).WithDatabase(database), nil
}

// Connector configures new physical connections without changing existing ones.
type Connector struct {
	owner             *Driver
	engine            *core.Engine
	noticeHandler     func(Error)
	authenticatedRole string
	database          string
	explicitDatabase  bool
}

// WithNoticeHandler returns an independent configuration. Nil disables delivery.
// Handlers run synchronously after execution releases all locks and its lease.
func (c *Connector) WithNoticeHandler(handler func(Error)) *Connector {
	copy := *c
	copy.noticeHandler = handler
	return &copy
}

// WithDatabase returns an independent exact decoded database selector.
func (c *Connector) WithDatabase(database string) *Connector {
	copy := *c
	copy.database, copy.explicitDatabase = database, true
	return &copy
}

func (c *Connector) Driver() driver.Driver { return c.owner }
func (c *Connector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.owner.mu.Lock()
	if c.owner.lastSession >= math.MaxInt32 {
		c.owner.mu.Unlock()
		return nil, errors.New("stuntgres: session identity exhausted")
	}
	c.owner.lastSession++
	id := c.owner.lastSession
	c.owner.mu.Unlock()
	principal, err := c.engine.ResolveLogin(c.authenticatedRole)
	if err != nil {
		return nil, err
	}
	target := c.engine
	if c.explicitDatabase {
		var err error
		target, err = target.SelectDatabase(ctx, c.database)
		if err != nil {
			return nil, err
		}
	}
	sess, err := target.StartSession(ctx, session.SessionStart{ID: id, Authenticated: principal, NoticeHandler: c.noticeHandler})
	if err != nil {
		return nil, err
	}
	native := &conn{engine: target, session: sess, noticeHandler: c.noticeHandler}
	native.cleanup = runtime.AddCleanup(native, cleanupSession, cleanupCapsule{sess})
	return native, nil
}

var _ driver.DriverContext = (*Driver)(nil)
