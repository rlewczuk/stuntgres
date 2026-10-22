package fdw

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// stageTarget is the object one WRAPPER, SERVER or USER MAPPING
// statement names. A mapping also carries its role, server and wrapper.
type stageTarget struct {
	class   ids.OID
	name    string
	role    authorization.PrincipalRef
	server  *catalog.ForeignObject
	wrapper *catalog.ForeignObject
}

// StageDDL stages CREATE, ALTER and DROP of a foreign-data wrapper,
// server or user mapping, and routes CREATE and DROP FOREIGN TABLE to
// StageTable. It returns the validator jobs, which the engine runs after releasing
// Engine.mu and replacementMu, and the dropped roots, whose dependents the
// engine stages. A nil, nil, nil result means there is nothing to publish.
func StageDDL(h Host, auth ddl.Authority, s *parser.ForeignDDL, input sqltype.InputEnvironment) ([]Validation, []ids.ObjectAddress, error) {
	view := h.AccessView(auth)
	actor, err := view.Roles.Descriptor(view.State.CurrentUser)
	if err != nil {
		return nil, nil, err
	}
	var successor authorization.PrincipalRef
	if s.Owner != nil {
		targetName, identity := access.OwnershipRoleOperand(*s.Owner)
		successor, err = access.SchemaOwner(view, targetName, identity)
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
		return StageTable(h, auth, s, input)
	}
	target := stageTarget{class: Class(s.Kind)}
	if len(s.Name.Parts) != 0 {
		if len(s.Name.Parts) != 1 {
			return nil, nil, sqlError("42601", "SQL/MED global names cannot be qualified")
		}
		target.name = s.Name.Parts[0].Text
	}
	if s.Kind == "mapping" {
		target.role, err = MappedRole(view, s.Role)
		if err != nil {
			if s.Action == "drop" && s.IfExists {
				if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42704" {
					ddl.Notice(h, "00000", diagnostic.Message+", skipping")
					return nil, nil, nil
				}
			}
			return nil, nil, err
		}
		target.server, err = Find(view.Catalog, ids.ForeignServerClassID, s.Server.Text)
		if err != nil {
			if s.Action == "drop" && s.IfExists {
				ddl.Notice(h, "00000", fmt.Sprintf(`server "%s" does not exist, skipping`, s.Server.Text))
				return nil, nil, nil
			}
			return nil, nil, err
		}
		if err = MappingAuthority(view, auth, target.server, target.role); err != nil {
			return nil, nil, err
		}
		target.name = catalog.ForeignMappingName(target.server.Address(), target.role)
		h.AddDomainBase(target.server)
		target.wrapper, _ = view.Catalog.Foreign(target.server.Wrapper)
	}
	if s.Action == "drop" {
		roots, err := stageDrop(h, auth, s, target)
		return nil, roots, err
	}
	return stageDefinition(h, auth, s, target, successor)
}

// stageDefinition stages CREATE or ALTER of target. successor is the
// resolved new owner of ALTER ... OWNER TO.
func stageDefinition(h Host, auth ddl.Authority, s *parser.ForeignDDL, target stageTarget, successor authorization.PrincipalRef) ([]Validation, []ids.ObjectAddress, error) {
	view := h.AccessView(auth)
	class, name, role, server, wrapper := target.class, target.name, target.role, target.server, target.wrapper
	old, _ := Find(view.Catalog, class, name)
	if s.Action == "create" && old != nil {
		message := fmt.Sprintf(`%s "%s" already exists`, catalog.ForeignLabel(class), name)
		if class == ids.ForeignMappingClassID {
			message = MappingDiagnostic(view, role, server, true)
		}
		if s.IfNotExists {
			ddl.Notice(h, "42710", message+", skipping")
			return nil, nil, nil
		}
		return nil, nil, sqlError("42710", message)
	}
	var err error
	if s.Action == "alter" && old == nil {
		if class == ids.ForeignMappingClassID {
			return nil, nil, sqlError("42704", MappingDiagnostic(view, role, server, false))
		}
		_, err = Find(view.Catalog, class, name)
		return nil, nil, err
	}
	var next catalog.ForeignObject
	if old != nil {
		if class != ids.ForeignMappingClassID && s.Owner == nil {
			if err = ddl.Check(h, auth, access.Requirement{Object: ObjectRef(view, old), OwnerOnly: true}); err != nil {
				return nil, nil, err
			}
		}
		next = *old
	} else {
		oid, er := h.AllocateOID()
		if er != nil {
			return nil, nil, er
		}
		next = catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: class, ObjectID: oid}, ObjectName: name, Owner: view.State.CurrentUser}
	}
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return nil, nil, err
	}
	switch class {
	case ids.ForeignWrapperClassID:
		if s.HandlerSet {
			p, er := handlerProcedure(h, auth, s.Handler, catalog.ForeignHandlerSignature)
			if er != nil {
				return nil, nil, er
			}
			next.Handler = ids.ObjectAddress{}
			if p != nil {
				next.Handler = p.Address()
				h.AddDomainBase(p)
			}
		}
		if s.ValidatorSet {
			p, er := handlerProcedure(h, auth, s.Validator, catalog.ForeignValidatorSignature)
			if er != nil {
				return nil, nil, er
			}
			next.Validator = ids.ObjectAddress{}
			if p != nil {
				next.Validator = p.Address()
				h.AddDomainBase(p)
			}
		}
		wrapper = &next
	case ids.ForeignServerClassID:
		if old == nil {
			wrapper, err = Find(view.Catalog, ids.ForeignWrapperClassID, s.Server.Text)
			if err != nil {
				return nil, nil, err
			}
			if err = ddl.Check(h, auth, access.Requirement{Object: ObjectRef(view, wrapper), Privileges: authorization.UsagePrivilege}); err != nil {
				return nil, nil, err
			}
			next.Wrapper = wrapper.Address()
			next.ServerType = s.ServerType
		} else {
			wrapper, _ = view.Catalog.Foreign(old.Wrapper)
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
			mapped, er := view.Roles.Descriptor(role)
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
		next.Options, err = TransformOptions(next.Options, s.Options)
		if err != nil {
			return nil, nil, err
		}
	}
	if s.NewName.Text != "" {
		next.ObjectName = s.NewName.Text
	}
	if s.Owner != nil {
		if er := access.ForeignOwnershipEligibility(view, auth, old, successor); er != nil {
			return nil, nil, er
		}
		if successor == old.Owner {
			return nil, nil, nil
		}
		next.ACL = authorization.SchemaACLOwner(next.ACL, next.Owner, successor)
		next.Owner = successor
	}
	if a := h.Stage().LookupName(catalog.DescriptorNameKey(&next)); a != (ids.ObjectAddress{}) && (old == nil || a != old.Address()) {
		return nil, nil, sqlError("42710", fmt.Sprintf(`%s "%s" already exists`, catalog.ForeignLabel(class), next.Name()))
	}
	h.AddDomainBase(wrapper)
	var jobs []Validation
	if old == nil || s.Options != nil {
		job, er := stageValidatorJob(h, wrapper, catalog.ForeignObjectKind(class), next.Options)
		if er != nil {
			return nil, nil, er
		}
		jobs = append(jobs, job)
	}
	if old == nil {
		err = h.Stage().Create(&next)
	} else {
		err = h.Stage().Replace(&next)
	}
	if err != nil {
		return nil, nil, err
	}
	if s.Action == "alter" && class == ids.ForeignWrapperClassID {
		if s.HandlerSet {
			h.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: "changing the foreign-data wrapper handler can change behavior of existing foreign tables"})
		}
		if s.ValidatorSet && next.Validator != (ids.ObjectAddress{}) {
			h.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: "changing the foreign-data wrapper validator can cause the options for dependent objects to become invalid"})
		}
	}
	return jobs, nil, nil
}
