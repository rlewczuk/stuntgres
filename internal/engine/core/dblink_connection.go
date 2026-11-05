package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/contrib"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/remote"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func dblinkSQLError(code, message string) *sqlstate.Error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}
func dblinkPasswordError(post bool) error {
	err := dblinkSQLError("2F003", "password or GSSAPI delegated credentials required")
	if post {
		err.Detail = "Non-superusers may only connect using credentials they provide, eg: password in connection string or delegated GSSAPI credentials"
		err.Hint = "Ensure provided credentials match target server's authentication method."
	} else {
		err.Detail = "Non-superusers must provide a password in the connection string or send delegated GSSAPI credentials."
	}
	return err
}
func dblinkOpenError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var establishment *catalog.ExternalSQLConnectError
	if errors.As(err, &establishment) && establishment.Err != nil {
		projected := dblinkSQLError("08001", "could not establish connection")
		projected.Detail = strings.TrimRight(establishment.Error(), "\r\n")
		return projected
	}
	var remoteError *sqlstate.Error
	if errors.As(err, &remoteError) {
		projected := dblinkSQLError("08001", "could not establish connection")
		projected.Detail = remoteError.Message
		if remoteError.Detail != "" {
			projected.Detail += "\n" + remoteError.Detail
		}
		return projected
	}
	return err
}

// Called after exec's installed admission, argument evaluation and strictness.
// The shared environment below installs the current body's owner/configuration;
// native expression body initialization alone does not install that environment.
func (s *StatementScope) dblinkConnection(n plan.InstalledDblinkConnectionExpr, values [2]sqltype.Value, caller authorization.PrincipalRef) (result sqltype.Value, err error) {
	e := s.Engine()
	s.foreignUnlocked()
	// Installed admission, not an inherited host callback bearer, owns this path.
	// Preserve cancellation while removing any enclosing FDW-private context tag.
	ctx := catalog.WithoutExternalSQLContext(s.executionContext())
	e.Mu.Lock()
	auth, err := s.RoutineAuthorizationLocked()
	if err == nil {
		callerAuth := auth
		callerAuth.State.CurrentUser = caller
		err = contrib.ValidateDblinkConnection(callerAuth, n)
	}
	if err != nil {
		e.Mu.Unlock()
		return sqltype.Value{}, err
	}
	n.Procedure, _ = auth.Catalog.Procedure(n.Ref.Address)
	n.RoutineReference = n.Procedure
	e.Mu.Unlock()
	leave, err := s.enterRoutineEnvironmentMode(n.Procedure, false)
	if err != nil {
		return sqltype.Value{}, err
	}
	defer func() { err = leave(err) }()
	e.Mu.Lock()
	auth, err = s.RoutineAuthorizationLocked()
	if err != nil {
		e.Mu.Unlock()
		return sqltype.Value{}, err
	}
	physical := s.Op.Session
	physical.Mu.Lock()
	owner := PayloadOf(physical).externalSQL
	physical.Mu.Unlock()
	if n.Operation == functions.DblinkConnections {
		e.Mu.Unlock()
		if owner == nil {
			return sqltype.Null(n.Usage.Type), nil
		}
		names := owner.DblinkNames()
		if len(names) == 0 {
			return sqltype.Null(n.Usage.Type), nil
		}
		elements := make([]sqltype.Value, len(names))
		for i, name := range names {
			elements[i] = sqltype.String(n.Usage.Type.Element(), name)
		}
		return sqltype.OwnedArrayValue(n.Usage.Type, []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(names))}}, elements)
	}
	name, named := "", n.Count == 2
	if n.Operation == functions.DblinkDisconnect {
		named = n.Count == 1
	}
	if named {
		name, err = values[0].Text()
		if err != nil {
			e.Mu.Unlock()
			return sqltype.Value{}, err
		}
	}
	if n.Operation == functions.DblinkDisconnect {
		e.Mu.Unlock()
		if owner == nil {
			message := "connection not available"
			if named {
				message = fmt.Sprintf("connection \"%s\" not available", name)
			}
			return sqltype.Value{}, dblinkSQLError("08003", message)
		}
		if err := owner.DisconnectDblink(ctx, name, named); err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.String(n.Usage.Type, "OK"), nil
	}
	conninfo, err := values[n.Count-1].Text()
	if err != nil {
		e.Mu.Unlock()
		return sqltype.Value{}, err
	}
	provider, request, err := s.dblinkRequestLocked(auth, conninfo)
	if err != nil {
		e.Mu.Unlock()
		return sqltype.Value{}, err
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		e.Mu.Unlock()
		return sqltype.Value{}, err
	}
	var parsed remote.Conninfo
	if !actor.Superuser {
		parsed, err = remote.ParseConninfo(request.Conninfo)
		if err != nil || !parsed.PasswordPresent {
			e.Mu.Unlock()
			return sqltype.Value{}, dblinkPasswordError(false)
		}
	}
	if named {
		if clipped := remote.DblinkName(name); clipped != name {
			s.Op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: fmt.Sprintf("identifier \"%s\" will be truncated to \"%s\"", name, clipped)})
		}
	}
	if owner == nil {
		owner, err = s.externalSQLOwner()
	}
	if err != nil {
		e.Mu.Unlock()
		return sqltype.Value{}, err
	}
	pending, err := owner.BeginDblink(ctx, s.Op, provider, request, name, named)
	e.Mu.Unlock()
	if err != nil {
		return sqltype.Value{}, err
	}
	defer pending.Finish()
	if actor.Superuser {
		parsed, err = remote.ParseConninfo(request.Conninfo)
	}
	if err == nil {
		err = parsed.Validate()
	}
	if err != nil {
		diagnostic := dblinkSQLError("08001", "could not establish connection")
		diagnostic.Detail = err.Error()
		return sqltype.Value{}, diagnostic
	}
	info, err := pending.Open()
	if err != nil {
		return sqltype.Value{}, dblinkOpenError(err)
	}
	if !actor.Superuser {
		switch info.CredentialMechanism {
		case "password", "md5", "scram-sha-256":
		default:
			return sqltype.Value{}, dblinkPasswordError(true)
		}
	}
	e.Mu.Lock()
	currentAuth, err := s.RoutineAuthorizationLocked()
	if err == nil {
		currentAuth.State.CurrentUser = caller
		err = contrib.ValidateDblinkConnection(currentAuth, n)
	}
	var old catalog.ExternalSQLConnection
	if err == nil {
		old, err = pending.Publish()
	}
	e.Mu.Unlock()
	if err != nil {
		return sqltype.Value{}, err
	}
	if old != nil {
		if err := old.Close(ctx); err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.String(n.Usage.Type, "OK"), nil
}

// Resolution observes effective-body mapping before server USAGE. An alias
// obtains authority only from its authentic captured handler binding; it never
// falls back to the direct binding when that authority is absent.
func (s *StatementScope) dblinkRequestLocked(auth AuthorizationView, conninfo string) (*catalog.ExternalSQLProviderDefinition, catalog.ExternalSQLConnectRequest, error) {
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return nil, catalog.ExternalSQLConnectRequest{}, err
	}
	request := catalog.ExternalSQLConnectRequest{ConninfoPresent: true, Conninfo: conninfo, Role: actor.Name, RoleOID: uint32(auth.State.CurrentUser.ID())}
	server, exists := auth.Catalog.Foreign(auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.ForeignServerClassID, Name: remote.DblinkName(conninfo)}))
	if !exists {
		return s.Engine().Foreign.DblinkProvider(), request, nil
	}
	mapping, err := fdw.MappingFor(auth.View, server)
	if err != nil {
		return nil, request, err
	}
	if err := auth.Check(access.Requirement{Object: auth.Ref(server.Address()), Privileges: authorization.UsagePrivilege}); err != nil {
		return nil, request, err
	}
	wrapper, exists := auth.Catalog.Foreign(server.Wrapper)
	if !exists {
		return nil, request, ids.ErrCatalogReplaced
	}
	procedure, exists := auth.Catalog.Procedure(wrapper.Handler)
	if !exists || procedure.Foreign == nil || procedure.Foreign.ExternalProvider == nil {
		return nil, request, &catalog.ExternalSQLUnavailableError{Capability: "dblink foreign server provider binding"}
	}
	genuine := false
	for token := range s.Engine().Foreign.ProcedureTokens() {
		if token == procedure.Foreign {
			genuine = true
			break
		}
	}
	if !genuine {
		return nil, request, errors.New("stuntgres: foreign dblink provider installation token")
	}
	request.ServerOID, request.MappingOID, request.MappingPresent = uint32(server.Address().ObjectID), uint32(mapping.Address().ObjectID), true
	request.Conninfo = remote.ConninfoOptions(server.Options, mapping.Options)
	return procedure.Foreign.ExternalProvider, request, nil
}
