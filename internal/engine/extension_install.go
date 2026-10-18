package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// extensionInstaller is the engine side of an implementation: staging needs
// the engine and its catalog stage, which catalog code never names.
type extensionInstaller interface {
	extensionImplementation
	stage(*Engine, *CatalogStage, *extensionDescriptor, authorization.PrincipalRef, AuthorizationView) error
}

const unaccentTemplateContext = "SQL statement \"CREATE TEXT SEARCH TEMPLATE unaccent (\n\tINIT = unaccent_init,\n\tLEXIZE = unaccent_lexize\n)\"\nextension script file \"unaccent--1.1.sql\", near line 26"

// Registration is initialization-only and duplicate checked, like catalog
// provider registration. It does not execute scripts or load external packages.
func (e *Engine) registerExtension(name string, implementation extensionInstaller) error {
	if e.catalog.state != nil {
		return fmt.Errorf("stuntgres: extension registry is closed")
	}
	if name == "" || implementation == nil || implementation.Control().Version == "" {
		return fmt.Errorf("stuntgres: invalid extension registration")
	}
	if e.catalog.Extensions == nil {
		e.catalog.Extensions = make(map[string]extensionImplementation)
	}
	if _, exists := e.catalog.Extensions[name]; exists {
		return fmt.Errorf("stuntgres: duplicate extension registration %q", name)
	}
	e.catalog.Extensions[name] = implementation
	return nil
}

func (e *Engine) executeExtension(scope *StatementScope, s *parser.CreateExtension, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	stage, err := e.stageExtensionLocked(scope, s)
	e.mu.Unlock()
	if err != nil || stage == nil {
		return Result{}, err
	}
	defer e.DiscardCatalogStage(stage)
	if _, err = e.ValidateCatalogStage(scope.op.Context(), stage); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	changes := cloneCatalogChanges(scope.owner.catalog)
	for a, d := range stage.Descriptors {
		e.catalogChangeLocked(changes, a, d)
	}
	for key, original := range stage.Touched {
		changes.names[key] = original
	}
	changes.extensionAuthority = append(changes.extensionAuthority, extensionAuthorityObservation{roles: stage.roleRevision, namespace: stage.Base.Get(stage.extensionNamespace), database: stage.databasePredecessor})
	scope.catalog = changes
	scope.catalogAuthority = &catalogAuthority{session: scope.op.session, authorization: stage.authRevision, roles: stage.roleRevision, ref: ids.ObjectRef{Address: catalog.CurrentDatabaseAddress}, descriptor: stage.databasePredecessor, databaseAuthority: stage.databaseAuthority, databasePredecessor: stage.databasePredecessor}
	e.mu.Unlock()
	return Result{}, nil
}

func (e *Engine) stageExtensionLocked(scope *StatementScope, s *parser.CreateExtension) (*CatalogStage, error) {
	op := scope.op
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return nil, err
	}
	key := ids.NameKey{ClassID: ids.ExtensionClassID, Name: s.Name.Text}
	if auth.catalog.NameAddress(key) != (ids.ObjectAddress{}) {
		if s.IfNotExists {
			op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: fmt.Sprintf(`extension "%s" already exists, skipping`, s.Name.Text)})
			return nil, nil
		}
		return nil, sqlError("42710", fmt.Sprintf(`extension "%s" already exists`, s.Name.Text))
	}
	implementation, ok := e.catalog.Extensions[s.Name.Text]
	installer, installable := implementation.(extensionInstaller)
	if !ok || !installable {
		return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: fmt.Sprintf(`extension "%s" is not available`, s.Name.Text)}
	}
	registration := implementation.Control()
	version := registration.Version
	var schema *parser.Identifier
	seen := map[string]bool{}
	for _, option := range s.Options {
		if seen[option.Name] {
			return nil, expr.ErrorAt("42601", "conflicting or redundant options", option.Pos)
		}
		seen[option.Name] = true
		switch option.Name {
		case "version":
			version = option.Value.Text
		case "schema":
			value := option.Value
			schema = &value
		default:
			return nil, fmt.Errorf("stuntgres: invalid extension option")
		}
	}
	detail := ""
	switch {
	case version == "":
		detail = "Version names must not be empty."
	case strings.Contains(version, "--"):
		detail = `Version names must not contain "--".`
	case strings.ContainsAny(version, "/\\"):
		detail = "Version names must not contain directory separator characters."
	case strings.HasPrefix(version, "-") || strings.HasSuffix(version, "-"):
		detail = `Version names must not begin or end with "-".`
	}
	if detail != "" {
		return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`invalid extension version name: "%s"`, version), Detail: detail}
	}
	if version != registration.Version {
		return nil, sqlError("22023", fmt.Sprintf(`extension "%s" has no installation script nor update path for version "%s"`, s.Name.Text, version))
	}
	var namespace ids.ObjectAddress
	if schema != nil {
		namespace = auth.catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: schema.Text})
		if namespace == (ids.ObjectAddress{}) {
			return nil, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, schema.Text))
		}
	} else {
		path, _, pathErr := e.explicitPath(auth)
		if pathErr != nil {
			return nil, pathErr
		}
		if len(path) == 0 {
			return nil, sqlError("3F000", "no schema has been selected to create in")
		}
		namespace = path[0]
	}
	actor, err := auth.roles.Descriptor(auth.state.CurrentUser)
	if err != nil {
		return nil, err
	}
	if !actor.Superuser && !op.restoreTableDestination {
		if !registration.Trusted {
			return nil, sqlError("42501", fmt.Sprintf(`permission denied to create extension "%s"`, s.Name.Text))
		}
		rights, err := auth.Effective(e.catalog.Domain.Ref(auth.catalog.Epoch(), catalog.CurrentDatabaseAddress), false)
		if err != nil {
			return nil, err
		}
		if rights&authorization.CreatePrivilege == 0 {
			return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: fmt.Sprintf(`permission denied to create extension "%s"`, s.Name.Text), Hint: "Must have CREATE privilege on current database to create this extension."}
		}
	}
	memberOwner := actor.Ref
	if registration.Superuser && !actor.Superuser {
		memberOwner = e.authorizationDomain.Principal(10)
	}
	oid, err := e.catalog.allocateOIDLocked()
	if err != nil {
		return nil, err
	}
	rev, err := e.catalog.revisionLocked()
	if err != nil {
		return nil, err
	}
	ext := &extensionDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: oid}, ObjectRevision: rev, ObjectName: s.Name.Text, Version: version, TargetNamespace: namespace, Owner: actor.Ref}
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authorizeLocked(auth, op.session)
	stage.extensionNamespace = namespace
	if err := stage.Create(ext); err != nil {
		return nil, err
	}
	if err := installer.stage(e, stage, ext, memberOwner, auth); err != nil {
		return nil, err
	}
	return stage, nil
}

func (p *unaccentProvider) stage(e *Engine, stage *CatalogStage, ext *extensionDescriptor, owner authorization.PrincipalRef, auth AuthorizationView) error {
	ext.Members = make([]ids.ObjectAddress, 6)
	for i, class := range [6]ids.OID{ids.ProcedureClassID, ids.ProcedureClassID, ids.ProcedureClassID, ids.ProcedureClassID, ids.TSTemplateClassID, ids.TSDictionaryClassID} {
		oid, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return err
		}
		ext.Members[i] = ids.ObjectAddress{ClassID: class, ObjectID: oid}
	}
	signatures := [4]procedureSignature{unaccentDictionarySignature, unaccentTextSignature, unaccentInitSignature, unaccentLexizeSignature}
	for i, signature := range signatures {
		rev, err := e.catalog.revisionLocked()
		if err != nil {
			return err
		}
		name := "unaccent"
		result := ids.OID(25)
		vol := expr.Stable
		strict := true
		if i == 2 {
			name = "unaccent_init"
			result = 2281
			vol = expr.Volatile
			strict = false
		}
		if i == 3 {
			name = "unaccent_lexize"
			result = 2281
			vol = expr.Volatile
			strict = false
		}
		proc := &procedureDescriptor{ObjectAddress: ext.Members[i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: rev, ObjectName: name, Signature: signature, RoutineKind: functionRoutine, Owner: owner, Result: result, Volatility: vol, Strict: strict, Implementation: p}
		if i < 2 {
			proc.Dictionary = ext.Members[5]
		}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return extensionMemberConflict(proc, ext.TargetNamespace)
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	// The script resolves supplied helper names against its coherent staged
	// search path, under the actual script execution principal. In particular,
	// unqualified function lookup excludes the installer's own TEMP namespace.
	auth.catalog = e.stageRead(stage)
	auth.recaptureCatalog(auth.catalog)
	auth.state.CurrentUser = owner
	if err := auth.CanUse(e.catalog.Domain.Ref(auth.catalog.Epoch(), ext.TargetNamespace)); err != nil {
		return err
	}
	// Extension scripts replace, rather than prefix, the caller's path. The
	// stock script has no prerequisite schemas; implicit pg_catalog remains,
	// and the script's explicit pg_temp entry is excluded from function lookup.
	path := e.effectivePath(auth, []ids.ObjectAddress{ext.TargetNamespace}, installedFunctionLookup)
	var helpers [2]ids.ObjectAddress
	for i, helper := range [...]struct {
		name      string
		signature procedureSignature
		arguments string
	}{{"unaccent_init", unaccentInitSignature, "internal"}, {"unaccent_lexize", unaccentLexizeSignature, "internal, internal, internal, internal"}} {
		procedure := procedureInNamespaces(auth.catalog, namespaceSelection{path: path}, helper.name, helper.signature)
		if procedure == nil {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: fmt.Sprintf("function %s(%s) does not exist", helper.name, helper.arguments), Context: unaccentTemplateContext}
		}
		helpers[i] = procedure.Address()
	}
	rev, err := e.catalog.revisionLocked()
	if err != nil {
		return err
	}
	tmpl := &tsTemplateDescriptor{ObjectAddress: ext.Members[4], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: rev, ObjectName: "unaccent", Init: helpers[0], Lexize: helpers[1], Implementation: p}
	if stage.LookupName(catalog.DescriptorNameKey(tmpl)) != (ids.ObjectAddress{}) {
		return extensionMemberConflict(tmpl, ext.TargetNamespace)
	}
	if err := stage.Create(tmpl); err != nil {
		return err
	}
	rev, err = e.catalog.revisionLocked()
	if err != nil {
		return err
	}
	dict := &tsDictionaryDescriptor{ObjectAddress: ext.Members[5], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: rev, ObjectName: "unaccent", Template: tmpl.Address(), Owner: owner, Implementation: p}
	if stage.LookupName(catalog.DescriptorNameKey(dict)) != (ids.ObjectAddress{}) {
		return extensionMemberConflict(dict, ext.TargetNamespace)
	}
	return stage.Create(dict)
}
func extensionMemberConflict(d Descriptor, ns ids.ObjectAddress) error {
	err := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR"}
	switch d := d.(type) {
	case *procedureDescriptor:
		err.Code = "42723"
		err.Message = fmt.Sprintf(`function "%s" already exists with same argument types`, d.Name())
		args, result, symbol, properties, line := "text", "text", "unaccent_dict", "STABLE STRICT PARALLEL SAFE", 11
		switch d.Signature {
		case unaccentDictionarySignature:
			args, line = "regdictionary, text", 6
		case unaccentInitSignature:
			args, result, symbol, properties, line = "internal", "internal", "unaccent_init", "PARALLEL SAFE", 16
		case unaccentLexizeSignature:
			args, result, symbol, properties, line = "internal,internal,internal,internal", "internal", "unaccent_lexize", "PARALLEL SAFE", 21
		}
		err.Context = fmt.Sprintf("SQL statement \"CREATE FUNCTION %s(%s)\n\tRETURNS %s\n\tAS '$libdir/unaccent', '%s'\n\tLANGUAGE C %s\"\nextension script file \"unaccent--1.1.sql\", near line %d", d.Name(), args, result, symbol, properties, line)
	case *tsTemplateDescriptor:
		err.Code = "23505"
		err.Schema = "pg_catalog"
		err.Table = "pg_ts_template"
		err.Constraint = "pg_ts_template_tmplname_index"
		err.Detail = fmt.Sprintf("Key (tmplname, tmplnamespace)=(unaccent, %d) already exists.", ns.ObjectID)
		err.Context = unaccentTemplateContext
	case *tsDictionaryDescriptor:
		err.Code = "23505"
		err.Schema = "pg_catalog"
		err.Table = "pg_ts_dict"
		err.Constraint = "pg_ts_dict_dictname_index"
		err.Detail = fmt.Sprintf("Key (dictname, dictnamespace)=(unaccent, %d) already exists.", ns.ObjectID)
		err.Context = "SQL statement \"CREATE TEXT SEARCH DICTIONARY unaccent (\n\tTEMPLATE = unaccent,\n\tRULES    = 'unaccent'\n)\"\nextension script file \"unaccent--1.1.sql\", near line 31"
	}
	if err.Code == "23505" {
		err.Message = fmt.Sprintf(`duplicate key value violates unique constraint "%s"`, err.Constraint)
	}
	return err
}

type extensionAuthorityObservation struct {
	roles               uint64
	namespace, database Descriptor
}

func extensionNameConflict(name string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "pg_extension_name_index"`, Detail: fmt.Sprintf("Key (extname)=(%s) already exists.", name), Schema: "pg_catalog", Table: "pg_extension", Constraint: "pg_extension_name_index"}
}
