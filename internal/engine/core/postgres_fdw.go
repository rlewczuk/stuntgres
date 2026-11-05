package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"
	"syscall"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/remote"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// postgresFDWCapability lives for one admitted operation. Catalog facts/options
// are captured under Engine.Mu; no transport, parsing or cleanup holds that lock.
type postgresFDWCapability struct {
	scope                                        *StatementScope
	owner                                        *remote.Owner
	identity                                     remote.PostgresIdentity
	request                                      catalog.ExternalSQLConnectRequest
	server, mapping                              *catalog.ForeignObject
	handler                                      *catalog.Procedure
	role                                         authorization.PrincipalRef
	superuser, passwordRequired, passwordPresent bool
	bytea                                        *sqltype.Descriptor
	localColumns                                 []postgresFDWColumnType
}

type postgresFDWColumnType struct {
	attribute int32
	base      sqltype.ID
}

func (c *postgresFDWCapability) LocalColumnBaseType(attribute int32) (sqltype.ID, error) {
	for _, column := range c.localColumns {
		if column.attribute == attribute {
			return column.base, nil
		}
	}
	return 0, errors.New("stuntgres: postgres_fdw missing admitted local attribute")
}

func (c *postgresFDWCapability) ExternalSQLSessionToken() ids.SessionToken {
	return c.scope.Op.Session.Token()
}
func (c *postgresFDWCapability) ExternalSQLProviderToken() *catalog.ExternalSQLProviderDefinition {
	return c.identity.Key.Provider
}
func (*postgresFDWCapability) Open(context.Context, catalog.ExternalSQLConnectRequest) (catalog.ExternalSQLConnection, error) {
	return nil, &catalog.ExternalSQLUnavailableError{Capability: "postgres_fdw requires a transaction-owned connection lease"}
}
func postgresOption(options []catalog.ForeignOption, name, fallback string) string {
	for _, option := range options {
		if option.Name == name {
			return option.Value
		}
	}
	return fallback
}
func postgresBoolean(options []catalog.ForeignOption, name string, fallback bool) bool {
	for _, option := range options {
		if option.Name == name {
			return strings.EqualFold(option.Value, "true") || strings.EqualFold(option.Value, "on")
		}
	}
	return fallback
}
func postgresPasswordError(post bool) error {
	d := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2F003", Message: "password or GSSAPI delegated credentials required", Detail: "Non-superusers must delegate GSSAPI credentials, provide a password, or enable SCRAM pass-through in user mapping."}
	if post {
		d.Detail = "Non-superuser cannot connect if the server does not request a password or use GSSAPI with delegated credentials."
		d.Hint = "Target server's authentication method must be changed or password_required=false set in the user mapping attributes."
	}
	return d
}
func (c *postgresFDWCapability) security(info *catalog.ExternalSQLConnectionInfo) error {
	if c.superuser || !c.passwordRequired {
		return nil
	}
	if !c.passwordPresent {
		return postgresPasswordError(false)
	}
	if info != nil {
		switch info.CredentialMechanism {
		case "password", "md5", "scram-sha-256":
		default:
			return postgresPasswordError(true)
		}
	}
	return nil
}

func (s *StatementScope) postgresFDWCapability(handler *catalog.ForeignProcedure, request *catalog.ForeignRequest) error {
	owner, err := s.externalSQLOwner()
	if err != nil {
		return err
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		return err
	}
	procedure, ok := auth.Catalog.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(request.HandlerOID)})
	if !ok || procedure.Foreign != handler || !extension.StockPostgresFDW.SupportsHandler(procedure) {
		return fmt.Errorf("stuntgres: invalid postgres_fdw handler authority")
	}
	if err := extension.StockPostgresFDW.ValidateProcedure(auth.Catalog, procedure); err != nil {
		return err
	}
	server, ok := auth.Catalog.Foreign(ids.ObjectAddress{ClassID: ids.ForeignServerClassID, ObjectID: ids.OID(request.ServerOID)})
	if !ok {
		return ids.ErrCatalogReplaced
	}
	mapping, ok := auth.Catalog.Foreign(ids.ObjectAddress{ClassID: ids.ForeignMappingClassID, ObjectID: ids.OID(request.MappingOID)})
	if !ok || mapping.Server != server.Address() || !slices.Equal(server.Options, request.ServerOptions) || !slices.Equal(mapping.Options, request.MappingOptions) {
		return ids.ErrCatalogReplaced
	}
	role, ok := auth.Roles.Lookup(request.Role)
	if !ok || uint32(role.Ref.ID()) != request.RoleOID {
		return ids.ErrCatalogReplaced
	}
	selected := auth.View
	selected.State.CurrentUser = role.Ref
	currentMapping, err := fdw.MappingFor(selected, server)
	if err != nil {
		return err
	}
	if currentMapping.Address() != mapping.Address() {
		return ids.ErrCatalogReplaced
	}
	provider := e.Foreign.PostgresFDWProvider()
	if provider == nil {
		return &catalog.ExternalSQLUnavailableError{Capability: "postgres_fdw external SQL provider binding"}
	}
	bytea, ok := auth.Catalog.LookupType(sqltype.Bytea)
	if !ok {
		return fmt.Errorf("stuntgres: missing builtin bytea descriptor")
	}
	c := &postgresFDWCapability{scope: s, owner: owner, server: server, mapping: mapping, handler: procedure, role: role.Ref, superuser: role.Superuser, passwordRequired: postgresBoolean(mapping.Options, "password_required", true), passwordPresent: postgresOption(mapping.Options, "password", "") != "", bytea: bytea}
	relation, ok := auth.Catalog.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(request.TableOID)})
	if !ok || relation.Foreign == nil || relation.Foreign.Server != server.Address() {
		return ids.ErrCatalogReplaced
	}
	c.localColumns = make([]postgresFDWColumnType, len(request.RequiredAttributes))
	for i, attribute := range request.RequiredAttributes {
		slot := relation.IndexForAttribute(attribute)
		if slot < 0 {
			return ids.ErrCatalogReplaced
		}
		c.localColumns[i] = postgresFDWColumnType{attribute: attribute, base: relation.Columns[slot].Usage.BaseUsage().Type.ID()}
	}
	c.identity = remote.PostgresIdentity{Key: remote.PostgresKey{Database: auth.Catalog.Owner(), Epoch: auth.Catalog.Epoch(), Provider: provider, Mapping: mapping.Address().ObjectID}, Server: server.Address().ObjectID, ServerRevision: server.Revision(), MappingRevision: mapping.Revision(), Keep: postgresBoolean(server.Options, "keep_connections", true)}
	if err := c.security(nil); err != nil {
		return err
	}
	if info, exists := owner.PostgresInfo(c.identity.Key); exists {
		if err := c.security(&info); err != nil {
			return err
		}
	}
	c.request = catalog.ExternalSQLConnectRequest{Role: request.Role, RoleOID: request.RoleOID, ServerOID: request.ServerOID, MappingOID: request.MappingOID, MappingPresent: true, ServerOptions: fdw.PostgresTransportOptions(server.Options), MappingOptions: fdw.PostgresTransportOptions(mapping.Options)}
	c.request.MappingOptions = append(c.request.MappingOptions, catalog.ForeignOption{Name: "client_encoding", Value: "UTF8"}, catalog.ForeignOption{Name: "fallback_application_name", Value: "postgres_fdw"})
	request.ExternalSQL = c
	return nil
}
func (c *postgresFDWCapability) currentLocked() error {
	s := c.scope
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		return err
	}
	if auth.Catalog.Owner() != c.identity.Key.Database || auth.Catalog.Epoch() != c.identity.Key.Epoch {
		return ids.ErrCatalogReplaced
	}
	server, ok := auth.Catalog.Foreign(c.server.Address())
	if !ok || server.Revision() != c.server.Revision() {
		return ids.ErrCatalogReplaced
	}
	mapping, ok := auth.Catalog.Foreign(c.mapping.Address())
	if !ok || mapping.Revision() != c.mapping.Revision() {
		return ids.ErrCatalogReplaced
	}
	current, err := exec.ValidateRoutineDescriptor(auth.Catalog, auth.Roles, c.handler)
	if err != nil {
		return err
	}
	if err := extension.StockPostgresFDW.ValidateProcedure(auth.Catalog, current); err != nil {
		return err
	}
	actor, err := auth.Roles.Descriptor(c.role)
	if err != nil {
		return err
	}
	if actor.Superuser != c.superuser {
		return catalog.Conflict{}
	}
	selected := auth.View
	selected.State.CurrentUser = c.role
	currentMapping, err := fdw.MappingFor(selected, server)
	if err != nil {
		return err
	}
	if currentMapping.Address() != mapping.Address() {
		return ids.ErrCatalogReplaced
	}
	return nil
}
func (c *postgresFDWCapability) AcquirePostgresFDW(ctx context.Context) (catalog.PostgresFDWLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !catalog.ExternalSQLContextMatches(ctx, c) {
		return nil, fmt.Errorf("stuntgres: foreign postgres_fdw callback context")
	}
	if err := session.ValidateOperation(c.scope.Op.Session.Token().Domain(), c.scope.Op); err != nil {
		return nil, err
	}
	e := c.scope.Engine()
	e.Mu.Lock()
	err := c.currentLocked()
	var pending *remote.PostgresOpen
	var lease *remote.PostgresLease
	if err == nil {
		pending, lease, err = c.owner.BeginPostgres(ctx, c.scope.Op, c.identity, c.request, c.bytea)
	}
	e.Mu.Unlock()
	if err != nil {
		return nil, err
	}
	if lease != nil {
		return lease, nil
	}
	defer pending.Finish()
	info, err := pending.Open()
	if err != nil {
		return nil, c.connectError(err)
	}
	if err := c.security(&info); err != nil {
		return nil, err
	}
	if err := pending.Initialize(); err != nil {
		return nil, err
	}
	e.Mu.Lock()
	err = c.currentLocked()
	if err == nil {
		lease, err = pending.Publish()
	}
	e.Mu.Unlock()
	if err != nil {
		return nil, err
	}
	return lease, nil
}
func (c *postgresFDWCapability) connectError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var marker *catalog.ExternalSQLConnectError
	var response *sqlstate.Error
	detail := ""
	endpoint := fmt.Sprintf("connection to server at %q, port %s failed: ", postgresOption(c.server.Options, "host", ""), postgresOption(c.server.Options, "port", "5432"))
	if errors.As(err, &response) && strings.HasPrefix(response.Code, "28") {
		detail = endpoint + "FATAL:  " + response.Message
	} else if errors.As(err, &marker) {
		var network *net.OpError
		if errors.As(marker, &network) {
			cause := network.Err
			var system *os.SyscallError
			if errors.As(cause, &system) {
				cause = system.Err
			}
			message := cause.Error()
			if message != "" {
				message = strings.ToUpper(message[:1]) + message[1:]
			}
			detail = endpoint + message
			if errors.Is(cause, syscall.ECONNREFUSED) {
				detail += "\n\tIs the server running on that host and accepting TCP/IP connections?"
			}
		} else {
			detail = strings.TrimRight(marker.Error(), "\r\n")
		}
	} else {
		return err
	}
	return sqlstate.WithDiagnostic(err, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "08001", Message: fmt.Sprintf(`could not connect to server "%s"`, c.server.Name()), Detail: detail})
}
