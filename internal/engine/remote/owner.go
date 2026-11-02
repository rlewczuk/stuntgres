package remote

import (
	"context"
	"errors"
	"slices"
	"sync"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// Owner is attached before remote Open. Its mutex protects only membership;
// physical callbacks run unlocked. Session teardown joins every connection.
type Owner struct {
	mu          sync.Mutex
	session     *session.Session
	connections map[*connection]struct{}
	closed      bool
}

func NewOwner(s *session.Session) *Owner { return &Owner{session: s} }

type capability struct {
	owner    *Owner
	op       *session.Operation
	provider *catalog.ExternalSQLProviderDefinition
	request  catalog.ExternalSQLConnectRequest
}

func (o *Owner) Capability(op *session.Operation, provider *catalog.ExternalSQLProviderDefinition, request catalog.ForeignRequest) catalog.ExternalSQLSession {
	captured := catalog.ExternalSQLConnectRequest{
		Role: request.Role, RoleOID: request.RoleOID,
		ServerOID: request.ServerOID, MappingOID: request.MappingOID,
		MappingPresent: request.MappingOID != 0,
		ServerOptions:  slices.Clone(request.ServerOptions),
		MappingOptions: slices.Clone(request.MappingOptions),
	}
	return &capability{o, op, provider, captured}
}

func (c *capability) Open(ctx context.Context, request catalog.ExternalSQLConnectRequest) (catalog.ExternalSQLConnection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := session.ValidateOperation(c.owner.session.Token().Domain(), c.op); err != nil {
		return nil, err
	}
	if !catalog.ExternalSQLContextMatches(ctx, c) {
		return nil, errors.New("stuntgres: foreign external SQL callback context")
	}
	if request.Conninfo != "" && (request.ServerOptions != nil || request.MappingOptions != nil) {
		return nil, errors.New("stuntgres: external SQL conninfo and options are mutually exclusive")
	}
	request.Role, request.RoleOID = c.request.Role, c.request.RoleOID
	request.ServerOID, request.MappingOID = c.request.ServerOID, c.request.MappingOID
	request.MappingPresent = c.request.MappingPresent
	if request.Conninfo == "" {
		request.ServerOptions = slices.Clone(c.request.ServerOptions)
		request.MappingOptions = slices.Clone(c.request.MappingOptions)
	}
	raw, err := c.provider.Provider.Open(ctx, request)
	err = exec.ForeignCallbackError(err)
	if Nil(raw) {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("stuntgres: external SQL provider returned nil connection")
	}
	owned := &connection{owner: c.owner, provider: c.provider, raw: raw}
	if err == nil {
		owned.info = raw.Info()
	}
	owned.info.ProviderVersion = c.provider.Version
	s := c.owner.session
	s.Mu.Lock()
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = session.PublicationOperationLocked(s.Token().Domain(), c.op)
	}
	c.owner.mu.Lock()
	if err == nil && c.owner.closed {
		err = errors.New("stuntgres: external SQL owner has ended")
	}
	if err == nil {
		if c.owner.connections == nil {
			c.owner.connections = make(map[*connection]struct{})
		}
		c.owner.connections[owned] = struct{}{}
	}
	c.owner.mu.Unlock()
	s.Mu.Unlock()
	if err != nil {
		_ = raw.Close(context.WithoutCancel(ctx))
		return nil, err
	}
	return owned, nil
}

type cleanupKey struct{}

// CleanupContext grants only the existing joined owner cleanup path permission
// to settle already-owned resources after session command admission has ended.
func CleanupContext(ctx context.Context, token ids.SessionToken) context.Context {
	return context.WithValue(catalog.WithoutExternalSQLContext(context.WithoutCancel(ctx)), cleanupKey{}, token)
}

func (o *Owner) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if token, ok := ctx.Value(cleanupKey{}).(ids.SessionToken); ok && token != o.session.Token() {
		return errors.New("stuntgres: foreign external SQL cleanup context")
	}
	if active, ok := catalog.ExternalSQLContextCapability(ctx).(*capability); ok && active.owner != o {
		return errors.New("stuntgres: foreign external SQL connection context")
	}
	o.session.Mu.Lock()
	live := o.session.State == session.Live || o.session.State == session.Ending && ctx.Value(cleanupKey{}) == o.session.Token()
	o.session.Mu.Unlock()
	if !live {
		return errors.New("stuntgres: external SQL session has ended")
	}
	return nil
}

func (o *Owner) EndSession(token ids.SessionToken, _ session.EndReason) error {
	if token != o.session.Token() {
		return errors.New("stuntgres: foreign external SQL owner")
	}
	o.mu.Lock()
	o.closed = true
	connections := make([]*connection, 0, len(o.connections))
	for c := range o.connections {
		connections = append(connections, c)
	}
	o.mu.Unlock()
	var failures []error
	for _, c := range connections {
		if err := c.Close(context.Background()); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
