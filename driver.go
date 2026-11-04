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
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/hbarules"
	"stuntgres/internal/engine/host"
	"stuntgres/internal/engine/identfile"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
	"sync"
)

// Error carries a PostgreSQL diagnostic, including nonfatal notices and warnings.
type Error = sqlstate.Error

// DriverConfig fixes engine startup settings for every database on a Driver.
// The zero value leaves commit timestamp tracking disabled and starts with
// wal_level=replica; LogicalWAL starts every cluster with wal_level=logical.
type DriverConfig struct {
	TrackCommitTimestamp bool
	LogicalWAL           bool
}

// Driver owns independent clusters keyed by exact, opaque DSN strings.
// Logical databases live for the lifetime of their Driver, not their connections.
type Driver struct {
	mu                  sync.Mutex
	engines             map[string]*core.Engine
	lastSession         ids.SessionID
	settings            []settings.Definition
	settingsFrozen      bool
	config              DriverConfig
	foreignWrappers     []fdw.WrapperDefinition
	foreignValidators   []fdw.ValidatorDefinition
	externalProviders   []ExternalSQLProviderDefinition
	hostLanguages       []*host.Runtime
	textSearchFiles     map[string]string
	textSearchResources *functions.DictionaryFiles
	identFile           *identfile.Source
	sepgsql             *sepgsql.Runtime
	hbaRules            *hbarules.Source
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
	if d.hbaRules == nil {
		d.hbaRules = new(hbarules.Source)
	}
	e := d.engines[name]
	if e == nil {
		wrappers, err := fdw.BindExternalProviders(d.foreignWrappers, d.externalProviders)
		if err != nil {
			return nil, err
		}
		e = engine.NewWithStartup(d.settings, name, core.Startup{TrackCommitTimestamp: d.config.TrackCommitTimestamp, LogicalWAL: d.config.LogicalWAL})
		if err := e.InstallForeignDefinitions(wrappers, d.foreignValidators, importParameter, exportValue); err != nil {
			return nil, err
		}
		if err := e.InstallHostDefinitions(d.hostLanguages); err != nil {
			return nil, err
		}
		if err := e.InstallSepgsqlSimulation(d.sepgsql, true); err != nil {
			return nil, err
		}
		if d.textSearchResources == nil && len(d.textSearchFiles) != 0 {
			d.textSearchResources = functions.NewDictionaryFiles(d.textSearchFiles)
		}
		e.TextSearchFiles = d.textSearchResources
		e.IdentFile = d.identFile
		e.HBARules = d.hbaRules
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
	owner                  *Driver
	engine                 *core.Engine
	noticeHandler          func(Error)
	authenticatedRole      string
	database               string
	explicitDatabase       bool
	sepgsqlContext         string
	explicitSepgsqlContext bool
}

// WithNoticeHandler returns an independent configuration. Nil disables delivery.
// Handlers run synchronously after execution releases all locks and its lease.
// Typed output notices run during Rows.Next, using that result's captured filter,
// without retaining the completed request or borrowing a new session lease.
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
	subject := ""
	if c.owner.sepgsql != nil {
		subject = c.owner.sepgsql.Initial()
		if c.explicitSepgsqlContext {
			subject = c.sepgsqlContext
		}
		if !c.owner.sepgsql.Valid(subject) {
			return nil, errors.New("stuntgres: invalid simulated sepgsql connection context")
		}
	} else if c.explicitSepgsqlContext {
		return nil, errors.New("stuntgres: sepgsql context without registered simulation")
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
	admission, err := c.engine.AdmitDatabase(ctx, principal, core.DatabaseSelector{Name: c.database, Explicit: c.explicitDatabase})
	if err != nil {
		return nil, err
	}
	defer admission.Release()
	target := admission.Target()
	sess, err := target.StartSession(ctx, admission, session.SessionStart{ID: id, NoticeHandler: c.noticeHandler, SepgsqlContext: subject})
	if err != nil {
		return nil, err
	}
	native := &conn{engine: target, session: sess, noticeHandler: c.noticeHandler}
	native.cleanup = runtime.AddCleanup(native, cleanupSession, cleanupCapsule{sess})
	return native, nil
}

var _ driver.DriverContext = (*Driver)(nil)
