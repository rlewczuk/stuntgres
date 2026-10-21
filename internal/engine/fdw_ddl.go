package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) foreignProcedureLocked(auth AuthorizationView, name *parser.ObjectName, signature catalog.ProcedureSignature) (*catalog.Procedure, error) {
	if name == nil {
		return nil, nil
	}
	path, err := e.lookupNamespaces(auth, *name, analyze.FunctionLookup)
	if err != nil {
		return nil, err
	}
	p := analyze.ProcedureInNamespaces(auth.Catalog, path, name.Parts[len(name.Parts)-1].Text, signature)
	if p == nil {
		args := ""
		if signature == catalog.ForeignValidatorSignature {
			args = "text[], oid"
		}
		return nil, sqlError("42883", "function "+catalog.ObjectNameText(*name)+"("+args+") does not exist")
	}
	if err := auth.Check(access.Requirement{Object: fdw.ObjectRef(auth.View, p), Privileges: authorization.ExecutePrivilege}); err != nil {
		return nil, err
	}
	result := ids.OID(3115)
	if signature == catalog.ForeignValidatorSignature {
		result = 2278
	}
	if p.Result != result {
		return nil, sqlError("42804", "foreign-data wrapper function has incorrect return type")
	}
	return p, nil
}

// stageForeignValidatorJob builds the job against the stage base and records
// its validator procedure among the stage's observed dependencies.
func stageForeignValidatorJob(stage *CatalogStage, wrapper *catalog.ForeignObject, kind catalog.ForeignObjectKind, options []catalog.ForeignOption) (fdw.Validation, error) {
	job, validator, err := fdw.ValidatorJob(stage.Base, wrapper, kind, options)
	if validator != nil {
		stage.domainBases = append(stage.domainBases, validator)
	}
	return job, err
}

func (e *Engine) executeForeignDDL(scope *StatementScope, s *parser.ForeignDDL, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if s.Kind == "table" && s.Action == "alter" {
		return e.executeForeignTableAlter(scope, s)
	}
	if s.Action == "import" {
		return e.executeForeignImport(scope, s)
	}
	op := scope.op
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.Session)
	jobs, roots, err := e.stageForeignLocked(scope, auth, stage, s)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		return Result{}, err
	}
	if len(roots) != 0 {
		if err := scope.stageExpressionDependents(stage, roots, s.Mode, nil); err != nil {
			return Result{}, err
		}
	}
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	if err := fdw.RunValidations(op.Context(), jobs); err != nil {
		return Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	return Result{}, err
}

func (e *Engine) stageForeignLocked(scope *StatementScope, auth AuthorizationView, stage *CatalogStage, s *parser.ForeignDDL) ([]fdw.Validation, []ids.ObjectAddress, error) {
	op := scope.op
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return nil, nil, err
	}
	var successor authorization.PrincipalRef
	if s.Owner != nil {
		targetName, identity := access.OwnershipRoleOperand(*s.Owner)
		successor, err = access.SchemaOwner(auth.View, targetName, identity)
		if err != nil {
			return nil, nil, err
		}
	}
	changesCapability := s.Action == "create" || s.Action == "alter" && (s.HandlerSet || s.ValidatorSet || s.Options != nil)
	if s.Kind == "wrapper" && changesCapability && !actor.Superuser {
		d := sqlError("42501", fmt.Sprintf(`permission denied to %s foreign-data wrapper "%s"`, s.Action, s.Name.Parts[len(s.Name.Parts)-1].Text)).(*sqlstate.Error)
		d.Hint = "Must be superuser to " + s.Action + " a foreign-data wrapper."
		return nil, nil, d
	}
	if s.Kind == "table" {
		return e.stageForeignTableLocked(scope, auth, stage, s)
	}
	class := fdw.Class(s.Kind)
	var old, server, wrapper *catalog.ForeignObject
	var role authorization.PrincipalRef
	name := ""
	if len(s.Name.Parts) != 0 {
		if len(s.Name.Parts) != 1 {
			return nil, nil, sqlError("42601", "SQL/MED global names cannot be qualified")
		}
		name = s.Name.Parts[0].Text
	}
	if s.Kind == "mapping" {
		role, err = fdw.MappedRole(auth.View, s.Role)
		if err != nil {
			if s.Action == "drop" && s.IfExists {
				if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42704" {
					schemaNotice(op, "00000", diagnostic.Message+", skipping")
					return nil, nil, nil
				}
			}
			return nil, nil, err
		}
		server, err = fdw.Find(auth.Catalog, ids.ForeignServerClassID, s.Server.Text)
		if err != nil {
			if s.Action == "drop" && s.IfExists {
				schemaNotice(op, "00000", fmt.Sprintf(`server "%s" does not exist, skipping`, s.Server.Text))
				return nil, nil, nil
			}
			return nil, nil, err
		}
		if err = fdw.MappingAuthority(auth.View, auth, server, role); err != nil {
			return nil, nil, err
		}
		name = catalog.ForeignMappingName(server.Address(), role)
		stage.domainBases = append(stage.domainBases, server)
		wrapper, _ = auth.Catalog.Foreign(server.Wrapper)
	}
	if s.Action == "drop" {
		names := s.Names
		if s.Kind == "mapping" {
			names = []parser.ObjectName{{Parts: []parser.Identifier{{Text: name}}}}
		}
		var roots []ids.ObjectAddress
		for _, n := range names {
			if len(n.Parts) != 1 {
				return nil, nil, sqlError("42601", "SQL/MED global names cannot be qualified")
			}
			d, er := fdw.Find(auth.Catalog, class, n.Parts[0].Text)
			if er != nil {
				if class == ids.ForeignMappingClassID {
					message := fdw.MappingDiagnostic(auth.View, role, server, false)
					if s.IfExists {
						schemaNotice(op, "00000", message+", skipping")
						continue
					}
					return nil, nil, sqlError("42704", message)
				}
				if s.IfExists {
					schemaNotice(op, "00000", fmt.Sprintf(`%s "%s" does not exist, skipping`, fdw.Label(class), n.Parts[0].Text))
					continue
				}
				return nil, nil, er
			}
			if class != ids.ForeignMappingClassID {
				if er = auth.Check(access.Requirement{Object: fdw.ObjectRef(auth.View, d), OwnerOnly: true}); er != nil {
					return nil, nil, er
				}
			}
			if er = stage.removeObjectLocked(fdw.ObjectRef(auth.View, d), false); er != nil {
				return nil, nil, er
			}
			roots = append(roots, d.Address())
		}
		return nil, roots, nil
	}
	old, _ = fdw.Find(auth.Catalog, class, name)
	if s.Action == "create" && old != nil {
		message := fmt.Sprintf(`%s "%s" already exists`, fdw.Label(class), name)
		if class == ids.ForeignMappingClassID {
			message = fdw.MappingDiagnostic(auth.View, role, server, true)
		}
		if s.IfNotExists {
			schemaNotice(op, "42710", message+", skipping")
			return nil, nil, nil
		}
		return nil, nil, sqlError("42710", message)
	}
	if s.Action == "alter" && old == nil {
		if class == ids.ForeignMappingClassID {
			return nil, nil, sqlError("42704", fdw.MappingDiagnostic(auth.View, role, server, false))
		}
		_, err = fdw.Find(auth.Catalog, class, name)
		return nil, nil, err
	}
	var next catalog.ForeignObject
	if old != nil {
		if class != ids.ForeignMappingClassID && s.Owner == nil {
			if err = auth.Check(access.Requirement{Object: fdw.ObjectRef(auth.View, old), OwnerOnly: true}); err != nil {
				return nil, nil, err
			}
		}
		next = *old
	} else {
		oid, er := e.catalog.allocateOIDLocked()
		if er != nil {
			return nil, nil, er
		}
		next = catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: class, ObjectID: oid}, ObjectName: name, Owner: auth.State.CurrentUser}
	}
	next.ObjectRevision, err = e.catalog.revisionLocked()
	if err != nil {
		return nil, nil, err
	}
	switch class {
	case ids.ForeignWrapperClassID:
		if s.HandlerSet {
			p, er := e.foreignProcedureLocked(auth, s.Handler, catalog.ForeignHandlerSignature)
			if er != nil {
				return nil, nil, er
			}
			next.Handler = ids.ObjectAddress{}
			if p != nil {
				next.Handler = p.Address()
				stage.domainBases = append(stage.domainBases, p)
			}
		}
		if s.ValidatorSet {
			p, er := e.foreignProcedureLocked(auth, s.Validator, catalog.ForeignValidatorSignature)
			if er != nil {
				return nil, nil, er
			}
			next.Validator = ids.ObjectAddress{}
			if p != nil {
				next.Validator = p.Address()
				stage.domainBases = append(stage.domainBases, p)
			}
		}
		wrapper = &next
	case ids.ForeignServerClassID:
		if old == nil {
			wrapper, err = fdw.Find(auth.Catalog, ids.ForeignWrapperClassID, s.Server.Text)
			if err != nil {
				return nil, nil, err
			}
			if err = auth.Check(access.Requirement{Object: fdw.ObjectRef(auth.View, wrapper), Privileges: authorization.UsagePrivilege}); err != nil {
				return nil, nil, err
			}
			next.Wrapper = wrapper.Address()
			next.ServerType = s.ServerType
		} else {
			wrapper, _ = auth.Catalog.Foreign(old.Wrapper)
		}
		if s.VersionSet {
			next.Version = s.Version
		}
	case ids.ForeignMappingClassID:
		next.Server = server.Address()
		next.MappedRole = role
		next.Owner = role
		next.MappedName = "public"
		if role != (authorization.PrincipalRef{}) {
			mapped, er := auth.Roles.Descriptor(role)
			if er != nil {
				return nil, nil, er
			}
			next.MappedName = mapped.Name
		}
	}
	if wrapper == nil {
		return nil, nil, fmt.Errorf("stuntgres: missing foreign-data wrapper descriptor")
	}
	if old == nil || s.Options != nil {
		next.Options, err = fdw.TransformOptions(next.Options, s.Options)
		if err != nil {
			return nil, nil, err
		}
	}
	if s.NewName.Text != "" {
		next.ObjectName = s.NewName.Text
	}
	if s.Owner != nil {
		target := successor
		if er := fdw.OwnershipEligibility(auth.View, auth, old, target); er != nil {
			return nil, nil, er
		}
		if target == old.Owner {
			return nil, nil, nil
		}
		next.ACL = authorization.SchemaACLOwner(next.ACL, next.Owner, target)
		next.Owner = target
	}
	if a := stage.LookupName(catalog.DescriptorNameKey(&next)); a != (ids.ObjectAddress{}) && (old == nil || a != old.Address()) {
		return nil, nil, sqlError("42710", fmt.Sprintf(`%s "%s" already exists`, fdw.Label(class), next.Name()))
	}
	stage.domainBases = append(stage.domainBases, wrapper)
	var jobs []fdw.Validation
	if old == nil || s.Options != nil {
		job, er := stageForeignValidatorJob(stage, wrapper, catalog.ForeignObjectKind(class), next.Options)
		if er != nil {
			return nil, nil, er
		}
		jobs = append(jobs, job)
	}
	if old == nil {
		err = stage.Create(&next)
	} else {
		err = stage.Replace(&next)
	}
	if err != nil {
		return nil, nil, err
	}
	if s.Action == "alter" && class == ids.ForeignWrapperClassID {
		if s.HandlerSet {
			op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: "changing the foreign-data wrapper handler can change behavior of existing foreign tables"})
		}
		if s.ValidatorSet && next.Validator != (ids.ObjectAddress{}) {
			op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: "changing the foreign-data wrapper validator can cause the options for dependent objects to become invalid"})
		}
	}
	return jobs, nil, nil
}
