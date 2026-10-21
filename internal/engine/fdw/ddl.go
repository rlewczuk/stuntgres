package fdw

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type Validation struct {
	procedure   *catalog.ForeignProcedure
	kind        catalog.ForeignObjectKind
	options     []catalog.ForeignOption
	ImportTable string
}

func Class(kind string) ids.OID {
	switch kind {
	case "wrapper":
		return ids.ForeignWrapperClassID
	case "server":
		return ids.ForeignServerClassID
	case "mapping":
		return ids.ForeignMappingClassID
	default:
		return ids.RelationClassID
	}
}

func Label(class ids.OID) string {
	switch class {
	case ids.ForeignWrapperClassID:
		return "foreign-data wrapper"
	case ids.ForeignServerClassID:
		return "server"
	case ids.ForeignMappingClassID:
		return "user mapping"
	default:
		return "foreign table"
	}
}

func Find(catalog catalog.Read, class ids.OID, name string) (*catalog.ForeignObject, error) {
	a := catalog.NameAddress(ids.NameKey{ClassID: class, Name: name})
	if d, ok := catalog.Foreign(a); ok {
		return d, nil
	}
	return nil, sqlError("42704", fmt.Sprintf(`%s "%s" does not exist`, Label(class), name))
}

func ObjectRef(view access.View, d catalog.Descriptor) ids.ObjectRef {
	return view.Ref(d.Address())
}

func MappedRole(view access.View, role parser.GrantGrantee) (authorization.PrincipalRef, error) {
	if role.Public {
		return authorization.PrincipalRef{}, nil
	}
	name, identity := access.OwnershipRoleOperand(role.Role)
	return access.SchemaOwner(view, name, identity)
}

func MappingAuthority(view access.View, env access.Environment, server *catalog.ForeignObject, role authorization.PrincipalRef) error {
	if err := view.Check(env, access.Requirement{Object: ObjectRef(view, server), OwnerOnly: true}); err == nil {
		return nil
	}
	if role == view.State.CurrentUser {
		return view.Check(env, access.Requirement{Object: ObjectRef(view, server), Privileges: authorization.UsagePrivilege})
	}
	return sqlError("42501", "must be owner of foreign server "+server.Name())
}

// ValidatorJob builds the validation of options against wrapper's
// validator. It also returns the validator procedure the job depends on; it
// is nil when the wrapper has no validator.
func ValidatorJob(read catalog.Read, wrapper *catalog.ForeignObject, kind catalog.ForeignObjectKind, options []catalog.ForeignOption) (Validation, *catalog.Procedure, error) {
	if wrapper.Validator == (ids.ObjectAddress{}) {
		return Validation{}, nil, nil
	}
	p, ok := read.Procedure(wrapper.Validator)
	if !ok || p.Foreign == nil || p.Foreign.Validator == nil {
		return Validation{}, nil, sqlError("55000", fmt.Sprintf(`foreign-data wrapper "%s" has no registered validator`, wrapper.Name()))
	}
	return Validation{procedure: p.Foreign, kind: kind, options: append([]catalog.ForeignOption{}, options...)}, p, nil
}

func RunValidations(ctx context.Context, jobs []Validation) error {
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if job.procedure != nil {
			if err := job.procedure.Validator(ctx, job.kind, job.options); err != nil {
				if job.ImportTable != "" {
					return ImportError(err, job.ImportTable)
				}
				return err
			}
		}
	}
	return ctx.Err()
}

func IsCatalogClass(class ids.OID) bool {
	return class == ids.ForeignWrapperClassID || class == ids.ForeignServerClassID || class == ids.ForeignMappingClassID
}

func CommandTag(s *parser.ForeignDDL) string {
	if s.Action == "import" {
		return "IMPORT FOREIGN SCHEMA"
	}
	kind := "FOREIGN TABLE"
	switch s.Kind {
	case "wrapper":
		kind = "FOREIGN DATA WRAPPER"
	case "server":
		kind = "SERVER"
	case "mapping":
		kind = "USER MAPPING"
	}
	return strings.ToUpper(s.Action) + " " + kind
}

func MappingDiagnostic(view access.View, role authorization.PrincipalRef, server *catalog.ForeignObject, exists bool) string {
	name := "public"
	if role != (authorization.PrincipalRef{}) {
		if descriptor, ok := view.Roles.Find(role); ok {
			name = descriptor.Name
		}
	}
	verb := "does not exist"
	if exists {
		verb = "already exists"
	}
	return fmt.Sprintf(`user mapping for "%s" %s for server "%s"`, name, verb, server.Name())
}

func OwnershipEligibility(view access.View, env access.Environment, object *catalog.ForeignObject, target authorization.PrincipalRef) error {
	actor, err := view.Roles.Descriptor(view.State.CurrentUser)
	if err != nil {
		return err
	}
	if object.Address().ClassID == ids.ForeignWrapperClassID {
		failure := func(hint string) error {
			d := sqlError("42501", fmt.Sprintf(`permission denied to change owner of foreign-data wrapper "%s"`, object.Name())).(*sqlstate.Error)
			d.Hint = hint
			return d
		}
		if !actor.Superuser {
			return failure("Must be superuser to change owner of a foreign-data wrapper.")
		}
		recipient, err := view.Roles.Descriptor(target)
		if err != nil {
			return err
		}
		if !recipient.Superuser {
			return failure("The owner of a foreign-data wrapper must be a superuser.")
		}
		return nil
	}
	if object.Owner == target || actor.Superuser {
		return nil
	}
	if err = view.Check(env, access.Requirement{Object: ObjectRef(view, object), OwnerOnly: true}); err != nil {
		return err
	}
	if err = access.OwnershipEligibility(view, env, target, ids.ObjectAddress{}); err != nil {
		return err
	}
	wrapper, ok := view.Catalog.Foreign(object.Wrapper)
	if !ok {
		return ids.ErrCatalogReplaced
	}
	// The environment only names types and temporary namespaces in
	// diagnostics; a wrapper check never reads it, so env stays the actor's.
	recipient := view
	recipient.State.CurrentUser = target
	return recipient.Check(env, access.Requirement{Object: ObjectRef(recipient, wrapper), Privileges: authorization.UsagePrivilege})
}

func GrantKind(kind string) bool {
	return kind == "foreign server" || kind == "foreign-data wrapper"
}
