// Package stuntgres implements a process-local database/sql driver for a bounded
// PostgreSQL SQL subset. Importing it registers the driver name "stuntgres".
package stuntgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"runtime"
	"stuntgres/internal/engine"
	"stuntgres/internal/sqlstate"
	"sync"
)

// Error carries a PostgreSQL diagnostic, including nonfatal notices and warnings.
type Error = sqlstate.Error

// Driver owns independent databases named by exact, opaque DSN strings.
// Databases live for the lifetime of their Driver, not their connections.
type Driver struct {
	mu             sync.Mutex
	engines        map[string]*engine.Engine
	lastSession    engine.SessionID
	settings       []engine.SettingDefinition
	settingsFrozen bool
}

func NewDriver() *Driver { return &Driver{} }
func init()              { sql.Register("stuntgres", NewDriver()) }
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
		d.engines = make(map[string]*engine.Engine)
	}
	e := d.engines[name]
	if e == nil {
		e = engine.NewWithSettings(d.settings)
		d.settingsFrozen = true
		d.engines[name] = e
	}
	return &Connector{owner: d, engine: e, databaseName: name, authenticatedRole: authenticatedRole}, nil
}

// Connector configures new physical connections without changing existing ones.
type Connector struct {
	owner             *Driver
	engine            *engine.Engine
	noticeHandler     func(Error)
	authenticatedRole string
	databaseName      string
}

// WithNoticeHandler returns an independent configuration. Nil disables delivery.
// Handlers run synchronously after execution releases all locks and its lease.
func (c *Connector) WithNoticeHandler(handler func(Error)) *Connector {
	copy := *c
	copy.noticeHandler = handler
	return &copy
}

func (c *Connector) Driver() driver.Driver { return c.owner }
func (c *Connector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.owner.mu.Lock()
	if c.owner.lastSession == ^engine.SessionID(0) {
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
	session, err := c.engine.StartSession(ctx, engine.SessionStart{ID: id, Authenticated: principal, DatabaseName: c.databaseName})
	if err != nil {
		return nil, err
	}
	native := &conn{session: session, noticeHandler: c.noticeHandler}
	native.cleanup = runtime.AddCleanup(native, cleanupSession, cleanupCapsule{session})
	return native, nil
}

var _ driver.DriverContext = (*Driver)(nil)
