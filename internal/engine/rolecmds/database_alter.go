package rolecmds

import (
	"fmt"
	"math"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func databaseAlterAuthorityLocked(e *core.Engine, scope *core.StatementScope, name string) (core.AuthorizationView, *catalog.Database, *core.Engine, error) {
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return auth, nil, nil, err
	}
	address := auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.DatabaseClassID, Name: name})
	database, ok := auth.Catalog.Database(address)
	if !ok {
		return auth, nil, nil, core.SQLError("3D000", fmt.Sprintf(`database "%s" does not exist`, name))
	}
	if err := auth.Check(access.Requirement{Object: auth.View.Ref(address), OwnerOnly: true}); err != nil {
		return auth, nil, nil, err
	}
	target := e.DatabaseTargetLocked(address)
	if target == nil {
		return auth, nil, nil, catalog.Conflict{}
	}
	return auth, database, target, nil
}

// ExecuteAlterDatabase stages logical database authority on the ordinary utility
// journal. The core publisher alone installs registry names and projections.
func ExecuteAlterDatabase(e *core.Engine, scope *core.StatementScope, statement *parser.AlterDatabaseStmt, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	options := ddl.AlterDatabaseOptions{}
	var err error
	if statement.Action == "options" {
		options, err = ddl.ParseAlterDatabaseOptions(statement.Options)
		if err != nil {
			return core.Result{}, err
		}
	}
	if statement.Action == "tablespace" || options.HasTablespace {
		scope.Owner.Mu.Lock()
		err = session.ExplicitUtilityRefusal(statement, scope.Owner.State, scope.Owner.InBlock())
		scope.Owner.Mu.Unlock()
		if err != nil {
			return core.Result{}, err
		}
	}
	e.Mu.Lock()
	_, old, _, err := databaseAlterAuthorityLocked(e, scope, statement.Name)
	e.Mu.Unlock()
	if err != nil {
		return core.Result{}, err
	}
	if err := scope.AcquireDatabase(old.Address()); err != nil {
		return core.Result{}, err
	}
	if statement.Action == "rename" {
		if err := scope.AcquireDatabaseName(statement.NewName); err != nil {
			return core.Result{}, err
		}
	}
	e.Mu.Lock()
	auth, current, target, err := databaseAlterAuthorityLocked(e, scope, statement.Name)
	fail := func(err error) (core.Result, error) { e.Mu.Unlock(); return core.Result{}, err }
	if err != nil {
		return fail(err)
	}
	if current.Address() != old.Address() {
		return fail(catalog.Conflict{})
	}
	next := *current
	var notice *sqlstate.Error
	switch statement.Action {
	case "rename":
		actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
		if err != nil {
			return fail(err)
		}
		if !actor.Superuser && !actor.CreateDB {
			return fail(core.SQLError("42501", "permission denied to rename database"))
		}
		if auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.DatabaseClassID, Name: statement.NewName}) != (ids.ObjectAddress{}) {
			return fail(core.SQLError("42P04", fmt.Sprintf(`database "%s" already exists`, statement.NewName)))
		}
		if target == e {
			return fail(core.SQLError("0A000", "current database cannot be renamed"))
		}
		if err := target.DatabaseBusyLocked(nil, false); err != nil {
			return fail(err)
		}
		next.ObjectName = statement.NewName
	case "options":
		if !options.HasTablespace {
			if options.HasAllow && !options.Allow && target == e {
				return fail(core.SQLError("22023", "cannot disallow connections for current database"))
			}
			if options.HasTemplate {
				next.IsTemplate = options.Template
			}
			if options.HasAllow {
				next.AllowConnections = options.Allow
			}
			if options.HasLimit {
				next.ConnectionLimit = options.Limit
			}
			break
		}
		fallthrough
	case "tablespace":
		if target == e {
			return fail(core.SQLError("55006", "cannot change the tablespace of the currently open database"))
		}
		name := statement.Tablespace
		if options.HasTablespace {
			name = options.Tablespace
		}
		ref, err := ddl.TablespaceRef(auth.View, name)
		if err != nil {
			return fail(err)
		}
		if err := auth.View.CheckCreate(auth, ref); err != nil {
			return fail(err)
		}
		if ref.Address.ObjectID == catalog.GlobalTablespaceOID {
			return fail(core.SQLError("22023", "pg_global cannot be used as default tablespace"))
		}
		if ref.Address.ObjectID != current.DefaultTablespace {
			return fail(core.SQLError("42704", fmt.Sprintf(`tablespace "%s" does not exist`, name)))
		}
	case "refresh":
		actual, err := pgcatalog.DatabaseCollationActualVersion()
		if err != nil {
			return fail(err)
		}
		version, changed, err := ddl.DatabaseCollationVersion(current.CollationVersion, actual)
		if err != nil {
			return fail(err)
		}
		message := "version has not changed"
		if changed {
			message = fmt.Sprintf("changing version from %s to %s", *current.CollationVersion, *version)
			next.CollationVersion = version
		}
		notice = &sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message}
	default:
		return fail(fmt.Errorf("stuntgres: invalid database alteration action"))
	}
	if catalog.DatabaseAuthorityCompatible(current, &next) {
		e.Mu.Unlock()
		scope.CompleteUtility()
		if err := scope.Op.Context().Err(); err != nil {
			return core.Result{}, err
		}
		if notice != nil {
			scope.Op.EmitNotice(session.LevelNotice, *notice)
		}
		return core.Result{CommandTag: "ALTER DATABASE"}, nil
	}
	if current.Revision() == ids.DefinitionRevision(math.MaxUint64) {
		return fail(fmt.Errorf("stuntgres: database revision exhausted"))
	}
	next.ObjectRevision = current.Revision() + 1
	stage := core.OwnershipStage(e, auth, scope.Op)
	err = stage.Replace(&next)
	e.Mu.Unlock()
	if err != nil {
		e.DiscardCatalogStage(stage)
		return core.Result{}, err
	}
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	result, err := core.PublishOwnership(e, scope, stage)
	if err == nil && notice != nil {
		scope.Op.EmitNotice(session.LevelNotice, *notice)
	}
	result.CommandTag = "ALTER DATABASE"
	return result, err
}
