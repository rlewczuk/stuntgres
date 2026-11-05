package remote

import (
	"context"
	"errors"
	"sync"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

// PostgresKey is local database-incarnation and authentic mapping authority,
// never a role, DSN, handler name or credential cache key.
type PostgresKey struct {
	Database *catalog.Owner
	Epoch    ids.CatalogEpoch
	Provider *catalog.ExternalSQLProviderDefinition
	Mapping  ids.OID
}
type PostgresIdentity struct {
	Key                             PostgresKey
	Server                          ids.OID
	ServerRevision, MappingRevision ids.DefinitionRevision
	Keep                            bool
}
type postgresEntry struct {
	identity       PostgresIdentity
	connection     *connection
	inUse, invalid bool
}
type PostgresLease struct {
	owner *Owner
	entry *postgresEntry
	bytea *sqltype.Descriptor
	once  sync.Once
	err   error
}

func (l *PostgresLease) Connection() catalog.ExternalSQLConnection { return l.entry.connection }
func (l *PostgresLease) ByteaDescriptor() *sqltype.Descriptor      { return l.bytea }
func (l *PostgresLease) Release(ctx context.Context, healthy bool) error {
	l.once.Do(func() {
		o := l.owner
		o.mu.Lock()
		l.entry.inUse = false
		close := o.closed || l.entry.invalid || !l.entry.identity.Keep || !healthy
		if close && o.postgres[l.entry.identity.Key] == l.entry {
			delete(o.postgres, l.entry.identity.Key)
		}
		o.mu.Unlock()
		if close {
			l.err = l.entry.connection.Close(ctx)
		}
	})
	return l.err
}

// BeginPostgres runs under Engine.Mu so invalidation cannot miss an admitted
// pending Open. Publish is likewise pure and runs under Engine.Mu, then
// Session.Mu -> Owner.mu. Open/initialization/Finish/cleanup run unlocked.
func (o *Owner) BeginPostgres(ctx context.Context, op *session.Operation, identity PostgresIdentity, request catalog.ExternalSQLConnectRequest, bytea *sqltype.Descriptor) (*PostgresOpen, *PostgresLease, error) {
	if err := session.ValidateOperation(o.session.Token().Domain(), op); err != nil {
		return nil, nil, err
	}
	if op.Session != o.session || identity.Key.Database == nil || identity.Key.Provider == nil || identity.Key.Mapping == 0 || bytea == nil {
		return nil, nil, errors.New("stuntgres: invalid postgres_fdw connection authority")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil, nil, errors.New("stuntgres: external SQL owner has ended")
	}
	if entry := o.postgres[identity.Key]; entry != nil {
		if entry.identity.Server != identity.Server || entry.identity.ServerRevision != identity.ServerRevision || entry.identity.MappingRevision != identity.MappingRevision || entry.invalid {
			return nil, nil, ids.ErrCatalogReplaced
		}
		if entry.inUse {
			return nil, nil, errors.New("stuntgres: postgres_fdw mapping already enlisted")
		}
		entry.inUse = true
		return nil, &PostgresLease{owner: o, entry: entry, bytea: bytea}, nil
	}
	child, cancel := context.WithCancel(ctx)
	p := &PostgresOpen{owner: o, op: op, identity: identity, request: request, bytea: bytea, ctx: child, cancel: cancel, done: make(chan struct{})}
	if o.postgresPending == nil {
		o.postgresPending = make(map[*PostgresOpen]struct{})
	}
	o.postgresPending[p] = struct{}{}
	return p, nil, nil
}
func (o *Owner) PostgresInfo(key PostgresKey) (catalog.ExternalSQLConnectionInfo, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	entry := o.postgres[key]
	if entry == nil {
		// Invalidated active transactions retain their old physical handle but
		// must still authenticate every effective PUBLIC-mapping caller.
		for c := range o.connections {
			if c.postgres != nil && c.postgres.inUse && c.postgres.identity.Key == key {
				return c.info, true
			}
		}
		return catalog.ExternalSQLConnectionInfo{}, false
	}
	return entry.connection.info, true
}

type PostgresOpen struct {
	owner              *Owner
	op                 *session.Operation
	identity           PostgresIdentity
	request            catalog.ExternalSQLConnectRequest
	bytea              *sqltype.Descriptor
	ctx                context.Context
	cancel             context.CancelFunc
	done               chan struct{}
	invalid, published bool // Owner.mu protects invalid; opening goroutine owns published.
	connection         *connection
}

func (p *PostgresOpen) Open() (catalog.ExternalSQLConnectionInfo, error) {
	raw, err := p.identity.Key.Provider.Provider.Open(p.ctx, p.request)
	p.request = catalog.ExternalSQLConnectRequest{}
	err = exec.ForeignCallbackError(err)
	if Nil(raw) {
		if err != nil {
			return catalog.ExternalSQLConnectionInfo{}, err
		}
		return catalog.ExternalSQLConnectionInfo{}, errors.New("stuntgres: external SQL provider returned nil connection")
	}
	p.connection = &connection{owner: p.owner, provider: p.identity.Key.Provider, raw: raw}
	if err != nil {
		return catalog.ExternalSQLConnectionInfo{}, err
	}
	p.connection.info = raw.Info()
	p.connection.info.ProviderVersion = p.identity.Key.Provider.Version
	return p.connection.info, p.ctx.Err()
}
func (p *PostgresOpen) Initialize() error {
	// Do not force a date order: SET datestyle=ISO preserves startup DMY/MDY.
	_, err := p.connection.Execute(p.ctx, "SET search_path = pg_catalog; SET timezone = 'GMT'; SET datestyle = ISO; SET intervalstyle = postgres; SET extra_float_digits = 3")
	return err
}
func (p *PostgresOpen) Publish() (*PostgresLease, error) {
	o, s := p.owner, p.owner.session
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if err := session.PublicationOperationLocked(s.Token().Domain(), p.op); err != nil {
		return nil, err
	}
	if err := p.ctx.Err(); err != nil {
		return nil, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if p.invalid || o.closed {
		return nil, ids.ErrCatalogReplaced
	}
	if p.connection == nil || p.published || o.postgres[p.identity.Key] != nil {
		return nil, errors.New("stuntgres: invalid postgres_fdw publication")
	}
	entry := &postgresEntry{identity: p.identity, connection: p.connection, inUse: true}
	p.connection.postgres = entry
	if o.connections == nil {
		o.connections = make(map[*connection]struct{})
	}
	if o.postgres == nil {
		o.postgres = make(map[PostgresKey]*postgresEntry)
	}
	o.connections[p.connection] = struct{}{}
	o.postgres[p.identity.Key] = entry
	p.published = true
	return &PostgresLease{owner: o, entry: entry, bytea: p.bytea}, nil
}
func (p *PostgresOpen) Finish() {
	if !p.published && p.connection != nil {
		_ = p.connection.Close(context.Background())
	}
	p.cancel()
	p.owner.mu.Lock()
	delete(p.owner.postgresPending, p)
	close(p.done)
	p.owner.mu.Unlock()
}

type PostgresCleanup struct {
	connections []*connection
	pending     []*PostgresOpen
}

func (c PostgresCleanup) Empty() bool { return len(c.connections) == 0 && len(c.pending) == 0 }
func postgresIdentityCurrent(identity PostgresIdentity, read catalog.Read) bool {
	if identity.Key.Epoch != read.Epoch() {
		return false
	}
	server, ok := read.Foreign(ids.ObjectAddress{ClassID: ids.ForeignServerClassID, ObjectID: identity.Server})
	if !ok || server.Revision() != identity.ServerRevision {
		return false
	}
	mapping, ok := read.Foreign(ids.ObjectAddress{ClassID: ids.ForeignMappingClassID, ObjectID: identity.Key.Mapping})
	return ok && mapping.Revision() == identity.MappingRevision
}

// InvalidatePostgres detaches idle entries, defers in-use physical close to the
// sole transaction's Release, and cancels/joins pending returns via cleanup.
// Replacement forces in-use closure as its old database incarnation is retired.
func (o *Owner) InvalidatePostgres(read catalog.Read, replacement bool) PostgresCleanup {
	o.mu.Lock()
	defer o.mu.Unlock()
	var cleanup PostgresCleanup
	if replacement {
		// Reuse membership is not ownership: ordinary invalidation has already
		// detached active entries, but their physical connections still belong
		// to this Owner until Release or forced incarnation retirement.
		for owned := range o.connections {
			entry := owned.postgres
			if entry == nil || read.Owner() != nil && entry.identity.Key.Database != read.Owner() {
				continue
			}
			entry.invalid = true
			if o.postgres[entry.identity.Key] == entry {
				delete(o.postgres, entry.identity.Key)
			}
			cleanup.connections = append(cleanup.connections, owned)
		}
	} else {
		for key, entry := range o.postgres {
			if read.Owner() != nil && key.Database != read.Owner() || postgresIdentityCurrent(entry.identity, read) {
				continue
			}
			entry.invalid = true
			delete(o.postgres, key)
			if !entry.inUse {
				cleanup.connections = append(cleanup.connections, entry.connection)
			}
		}
	}
	for p := range o.postgresPending {
		if read.Owner() != nil && p.identity.Key.Database != read.Owner() || !replacement && postgresIdentityCurrent(p.identity, read) {
			continue
		}
		p.invalid = true
		cleanup.pending = append(cleanup.pending, p)
	}
	return cleanup
}
func (c PostgresCleanup) Cancel() {
	for _, p := range c.pending {
		p.cancel()
	}
}
func (c PostgresCleanup) Close() error {
	c.Cancel()
	for _, p := range c.pending {
		<-p.done
	}
	var failures []error
	for _, connection := range c.connections {
		if err := connection.Close(context.Background()); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
