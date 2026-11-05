package remote

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// DblinkName implements PostgreSQL's UTF8-safe NAMEDATALEN-1 key. Only creation
// emits a notice; lookup and removal use this same key silently.
func DblinkName(name string) string {
	if len(name) <= 63 {
		return name
	}
	n := 63
	for n > 0 && !utf8.RuneStart(name[n]) {
		n--
	}
	return name[:n]
}

// DblinkOpen is registered before transport entry, while the core gateway holds
// Engine.Mu. Only the admitted installed gateway receives it. Its completion
// includes late-return cleanup and publication; replacement joins it unlocked.
type DblinkOpen struct {
	owner            *Owner
	op               *session.Operation
	provider         *catalog.ExternalSQLProviderDefinition
	request          catalog.ExternalSQLConnectRequest
	ctx              context.Context
	cancel           context.CancelFunc
	done             chan struct{}
	generation       uint64
	name             string
	named, published bool
	connection       *connection
}

func (o *Owner) BeginDblink(ctx context.Context, op *session.Operation, provider *catalog.ExternalSQLProviderDefinition, request catalog.ExternalSQLConnectRequest, name string, named bool) (*DblinkOpen, error) {
	if err := session.ValidateOperation(o.session.Token().Domain(), op); err != nil {
		return nil, err
	}
	if op.Session != o.session {
		return nil, errors.New("stuntgres: foreign dblink operation")
	}
	if provider == nil {
		return nil, &catalog.ExternalSQLUnavailableError{Capability: "dblink direct conninfo"}
	}
	if !request.ConninfoPresent || request.ServerOptions != nil || request.MappingOptions != nil {
		return nil, errors.New("stuntgres: dblink requires explicit conninfo")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil, errors.New("stuntgres: external SQL owner has ended")
	}
	name = DblinkName(name)
	if named {
		if o.dblinkNamed[name] != nil {
			return nil, duplicateDblink()
		}
		for pending := range o.dblinkPending {
			if pending.named && pending.name == name && pending.generation == o.dblinkGeneration {
				return nil, duplicateDblink()
			}
		}
	}
	child, cancel := context.WithCancel(ctx)
	pending := &DblinkOpen{owner: o, op: op, provider: provider, request: request, ctx: child, cancel: cancel, done: make(chan struct{}), generation: o.dblinkGeneration, name: name, named: named}
	if o.dblinkPending == nil {
		o.dblinkPending = make(map[*DblinkOpen]struct{})
	}
	o.dblinkPending[pending] = struct{}{}
	return pending, nil
}
func duplicateDblink() error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: "duplicate connection name"}
}

func (p *DblinkOpen) Open() (catalog.ExternalSQLConnectionInfo, error) {
	raw, err := p.provider.Provider.Open(p.ctx, p.request)
	// Do not retain runtime secrets after the host call returns.
	p.request = catalog.ExternalSQLConnectRequest{}
	err = exec.ForeignCallbackError(err)
	if Nil(raw) {
		if err != nil {
			return catalog.ExternalSQLConnectionInfo{}, err
		}
		return catalog.ExternalSQLConnectionInfo{}, errors.New("stuntgres: external SQL provider returned nil connection")
	}
	c := &connection{owner: p.owner, provider: p.provider, raw: raw}
	p.connection = c
	if err != nil {
		return catalog.ExternalSQLConnectionInfo{}, err
	}
	c.info = raw.Info()
	c.info.ProviderVersion = p.provider.Version
	if err := p.ctx.Err(); err != nil {
		return catalog.ExternalSQLConnectionInfo{}, err
	}
	return c.info, nil
}

// Publish runs under Engine.Mu, after current caller/catalog admission, then
// Session.Mu -> Owner.mu. No callbacks or cleanup occur under these locks.
func (p *DblinkOpen) Publish() (catalog.ExternalSQLConnection, error) {
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
	if o.closed || o.dblinkGeneration != p.generation {
		return nil, ids.ErrCatalogReplaced
	}
	if p.connection == nil || p.published {
		return nil, errors.New("stuntgres: invalid dblink publication")
	}
	if o.connections == nil {
		o.connections = make(map[*connection]struct{})
	}
	o.connections[p.connection] = struct{}{}
	var old *connection
	if p.named {
		if o.dblinkNamed == nil {
			o.dblinkNamed = make(map[string]*connection)
		}
		o.dblinkNamed[p.name] = p.connection
	} else {
		old, o.dblinkDefault = o.dblinkDefault, p.connection
	}
	p.published = true
	if old != nil {
		return old, nil
	}
	return nil, nil
}

// Finish belongs to the opening goroutine. Detached generations cannot publish;
// even a transport ignoring child cancellation closes before done is signaled.
func (p *DblinkOpen) Finish() {
	p.request = catalog.ExternalSQLConnectRequest{}
	if !p.published && p.connection != nil {
		_ = p.connection.Close(context.Background())
	}
	p.cancel()
	p.owner.mu.Lock()
	delete(p.owner.dblinkPending, p)
	close(p.done)
	p.owner.mu.Unlock()
}
func (o *Owner) DblinkNames() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	names := make([]string, 0, len(o.dblinkNamed))
	for name := range o.dblinkNamed {
		names = append(names, name)
	}
	return names
}
func (o *Owner) DisconnectDblink(ctx context.Context, name string, named bool) error {
	o.mu.Lock()
	var c *connection
	if named {
		c = o.dblinkNamed[DblinkName(name)]
	} else {
		c = o.dblinkDefault
	}
	o.mu.Unlock()
	if c == nil {
		message := "connection not available"
		if named {
			message = fmt.Sprintf("connection \"%s\" not available", name)
		}
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "08003", Message: message}
	}
	err := c.Close(ctx)
	o.mu.Lock()
	if named {
		key := DblinkName(name)
		if o.dblinkNamed[key] == c {
			delete(o.dblinkNamed, key)
		}
	} else if o.dblinkDefault == c {
		o.dblinkDefault = nil
	}
	o.mu.Unlock()
	return err
}

// DblinkCleanup is a detached generation, not another resource owner. Detach is
// lock-only; Close must run after Engine/Session/Owner/Replacement locks release.
type DblinkCleanup struct {
	connections []*connection
	pending     []*DblinkOpen
}

func (o *Owner) DetachDblink() DblinkCleanup {
	o.mu.Lock()
	defer o.mu.Unlock()
	var cleanup DblinkCleanup
	if o.dblinkDefault != nil {
		cleanup.connections = append(cleanup.connections, o.dblinkDefault)
	}
	for _, c := range o.dblinkNamed {
		cleanup.connections = append(cleanup.connections, c)
	}
	for p := range o.dblinkPending {
		cleanup.pending = append(cleanup.pending, p)
	}
	o.dblinkDefault, o.dblinkNamed = nil, nil
	o.dblinkGeneration++
	return cleanup
}
func (c DblinkCleanup) Cancel() {
	for _, p := range c.pending {
		p.cancel()
	}
}
func (c DblinkCleanup) Close() error {
	c.Cancel()
	for _, p := range c.pending {
		<-p.done
	}
	var failures []error
	for _, owned := range c.connections {
		if err := owned.Close(context.Background()); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
