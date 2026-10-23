package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type databaseCreateOptions struct {
	owner          string
	template       string
	encoding       int
	collate, ctype string
	capability     string
}

func databaseOptionError(option parser.DatabaseOption, code, message string) error {
	position := ""
	if option.Position > 0 {
		position = strconv.Itoa(option.Position)
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position}
}

func parseDatabaseCreateOptions(options []parser.DatabaseOption) (databaseCreateOptions, error) {
	result := databaseCreateOptions{template: "template1", encoding: 6, collate: "C", ctype: "C"}
	seen := make(map[string]parser.DatabaseOption, len(options))
	for _, option := range options {
		name := option.Name
		if _, exists := seen[name]; exists {
			return result, databaseOptionError(option, "42601", "conflicting or redundant options")
		}
		switch name {
		case "owner", "template", "encoding", "lc_collate", "lc_ctype", "locale", "locale_provider", "strategy", "tablespace", "is_template", "allow_connections", "connection_limit", "icu_locale", "icu_rules", "builtin_locale", "collation_version", "oid":
		default:
			return result, databaseOptionError(option, "42601", fmt.Sprintf(`option "%s" not recognized`, option.Name))
		}
		seen[name] = option
	}
	if option, ok := seen["encoding"]; ok && !option.Default {
		if option.Numeric {
			id, err := strconv.Atoi(option.Value)
			if err != nil || sqltype.EncodingName(id) == "" {
				return result, databaseOptionError(option, "42704", option.Value+" is not a valid encoding code")
			}
			result.encoding = id
		} else {
			result.encoding = sqltype.EncodingID(option.Value)
			if result.encoding < 0 {
				return result, databaseOptionError(option, "42704", option.Value+" is not a valid encoding name")
			}
		}
	}
	if option, ok := seen["owner"]; ok && !option.Default {
		result.owner = option.Value
	}
	if option, ok := seen["template"]; ok && !option.Default {
		result.template = option.Value
	}
	if option, ok := seen["locale"]; ok && !option.Default {
		result.collate, result.ctype = option.Value, option.Value
	}
	if option, ok := seen["lc_collate"]; ok && !option.Default {
		result.collate = option.Value
	}
	if option, ok := seen["lc_ctype"]; ok && !option.Default {
		result.ctype = option.Value
	}
	if option, ok := seen["locale_provider"]; ok && !option.Default {
		if !strings.EqualFold(option.Value, "builtin") && !strings.EqualFold(option.Value, "icu") && !strings.EqualFold(option.Value, "libc") {
			return result, sqlError("42P17", "unrecognized locale provider: "+option.Value)
		}
	}
	// These valid mechanisms are deliberately not simulated. Values with actual
	// syntax/type errors are rejected before actor/owner authorization.
	for _, name := range []string{"is_template", "allow_connections"} {
		if option, ok := seen[name]; ok && !option.Default {
			switch strings.ToLower(option.Value) {
			case "true", "false", "on", "off", "yes", "no", "1", "0":
			default:
				return result, databaseOptionError(option, "22023", fmt.Sprintf("%s requires a Boolean value", name))
			}
		}
	}
	if option, ok := seen["connection_limit"]; ok && !option.Default {
		limit, err := strconv.ParseInt(option.Value, 10, 32)
		if err != nil {
			return result, databaseOptionError(option, "22P02", fmt.Sprintf(`invalid input syntax for type integer: "%s"`, option.Value))
		}
		if limit < -1 {
			return result, sqlError("22023", fmt.Sprintf("invalid connection limit: %d", limit))
		}
	}
	for _, name := range []string{"locale_provider", "strategy", "tablespace", "is_template", "allow_connections", "connection_limit", "icu_locale", "icu_rules", "builtin_locale", "collation_version", "oid"} {
		if _, ok := seen[name]; ok {
			result.capability = strings.ToUpper(name)
			break
		}
	}
	if option, ok := seen["owner"]; ok && !option.Default && option.Value == "" {
		return result, sqlError("42704", `role "" does not exist`)
	}
	return result, nil
}

func (e *Engine) databaseCreateOwnerLocked(scope *StatementScope, name string) (authorization.PrincipalRef, error) {
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return authorization.PrincipalRef{}, err
	}
	owner := auth.State.CurrentUser
	if name != "" {
		descriptor, ok := auth.Roles.Lookup(name)
		if !ok {
			return owner, sqlError("42704", fmt.Sprintf(`role "%s" does not exist`, name))
		}
		owner = descriptor.Ref
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return owner, err
	}
	if !actor.Superuser && !actor.CreateDB {
		return owner, sqlError("42501", "permission denied to create database")
	}
	allowed, err := auth.Roles.CanSetRole(actor.Ref, owner)
	if err != nil {
		return owner, err
	}
	if !allowed {
		target, err := auth.Roles.Descriptor(owner)
		if err != nil {
			return owner, err
		}
		return owner, sqlError("42501", fmt.Sprintf(`must be able to SET ROLE "%s"`, target.Name))
	}
	return owner, nil
}

func (e *Engine) executeCreateDatabase(scope *StatementScope, statement *parser.CreateDatabaseStmt, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	ctx := scope.op.Context()
	options, err := parseDatabaseCreateOptions(statement.Options)
	if err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	owner, err := e.databaseCreateOwnerLocked(scope, options.owner)
	if err == nil {
		switch options.template {
		case "template1":
		case "template0":
			err = sqlError("0A000", "CREATE DATABASE with TEMPLATE other than template1 is not supported")
		default:
			if e.cluster.targets[options.template] == nil {
				err = sqlError("3D000", fmt.Sprintf(`template database "%s" does not exist`, options.template))
			} else {
				err = sqlError("0A000", "CREATE DATABASE with TEMPLATE other than template1 is not supported")
			}
		}
	}
	if err == nil && options.encoding != 6 {
		err = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf("new encoding (%s) is incompatible with the encoding of the template database (UTF8)", sqltype.EncodingName(options.encoding)), Hint: "Use the same encoding as in the template database, or use template0 as template."}
	}
	if err == nil {
		for _, locale := range []struct{ value, kind string }{{options.collate, "LC_COLLATE"}, {options.ctype, "LC_CTYPE"}} {
			if locale.value != "C" && locale.value != "POSIX" {
				err = sqlError("0A000", "CREATE DATABASE with "+locale.kind+" other than C is not supported")
				break
			}
		}
	}
	if err == nil && options.capability != "" {
		err = sqlError("0A000", "CREATE DATABASE with "+options.capability+" is not supported")
	}
	if err == nil && e.cluster.targets[statement.Name] != nil {
		err = sqlError("42P04", fmt.Sprintf(`database "%s" already exists`, statement.Name))
	}
	if err == nil {
		err = ctx.Err()
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	return e.createDatabaseTarget(ctx, scope, statement.Name, options.owner, owner)
}

// createDatabaseTarget constructs storage privately. Only the final no-failure
// installation below can expose a name, descriptor, target or allocator advance.
func (e *Engine) createDatabaseTarget(ctx context.Context, scope *StatementScope, name, ownerName string, owner authorization.PrincipalRef) (Result, error) {
	e.cluster.seedOnce.Do(func() {
		candidate, err := newEngine(nil, nil, "template1", e.visibility.TracksCommitTimestamps())
		if err == nil {
			err = candidate.InstallForeignDefinitions(e.foreign.Definitions())
		}
		e.cluster.seed, e.cluster.seedError = candidate, err
	})
	if e.cluster.seedError != nil {
		return Result{}, e.cluster.seedError
	}
	seed := e.cluster.seed
	target := e.newDatabaseRuntime(seed, name, owner)
	if _, err := target.replaceCatalog(ctx, seed, seed.CatalogRead(), nil); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	currentOwner, err := e.databaseCreateOwnerLocked(scope, ownerName)
	if err != nil {
		return Result{}, err
	}
	if currentOwner != owner {
		return Result{}, catalog.Conflict{}
	}
	scope.op.Session.Mu.Lock()
	defer scope.op.Session.Mu.Unlock()
	if err := publicationOperationLocked(e, scope.op); err != nil {
		return Result{}, err
	}
	if scope.txRef != (ids.TxRef{}) && scope.txRef.Epoch() != e.catalog.state.Epoch() {
		return Result{}, ids.ErrCatalogReplaced
	}
	result, err := e.publishDatabaseTargetLocked(ctx, name, target, owner)
	if err == nil {
		scope.completed = true
	}
	return result, err
}

func (e *Engine) publishDatabaseTargetLocked(ctx context.Context, name string, target *Engine, owner authorization.PrincipalRef) (Result, error) {
	if e.cluster.targets[name] != nil {
		return Result{}, sqlError("42P04", fmt.Sprintf(`database "%s" already exists`, name))
	}
	if target == nil || target.mu != e.mu || target.roles != e.roles || target.cluster != nil || target.databaseName != name || target.catalog.Domain == e.catalog.Domain || target.catalog.Principals != &e.roles.domain || target.catalog.ACLAuthority != &e.roles.aclAuthority {
		return Result{}, fmt.Errorf("stuntgres: foreign or already registered database target")
	}
	if _, err := e.roles.published.Root.Read(authorization.PrincipalRef{}).Descriptor(owner); err != nil {
		return Result{}, err
	}
	oid, err := e.databaseOIDLocked(target)
	if err != nil {
		return Result{}, err
	}
	address := ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: oid}
	database := &catalog.Database{ObjectAddress: address, ObjectRevision: 1, ObjectName: name, Owner: owner, FrozenXID: target.databaseFrozenXID}
	targets := make(map[string]*Engine, len(e.cluster.targets)+1)
	roots := make(map[*Engine]*catalog.State, len(e.cluster.targets)+1)
	for key, runtime := range e.cluster.targets {
		targets[key] = runtime
	}
	targets[name] = target
	for _, runtime := range targets {
		next := catalog.CloneState(runtime.catalog.state)
		if runtime == target {
			for a, d := range next.Descriptors() {
				if a.ClassID == ids.DatabaseClassID {
					next.UnbindName(catalog.DescriptorNameKey(d))
					next.RemoveDescriptor(a)
				}
			}
			for a, d := range e.catalog.state.Descriptors() {
				if a.ClassID == ids.DatabaseClassID {
					next.SetDescriptor(a, d)
					next.BindName(catalog.DescriptorNameKey(d), a)
				}
			}
		}
		next.SetDescriptor(address, database)
		next.BindName(catalog.DescriptorNameKey(database), address)
		roots[runtime] = next
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	target.catalog.Database = address
	target.cluster = e.cluster
	installDatabaseProjectionLocked(roots)
	e.cluster.targets = targets
	e.cluster.nextOID = oid + 1
	return Result{CommandTag: "CREATE DATABASE"}, nil
}
