package fdw

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

type Validation struct {
	procedure   *catalog.Procedure
	kind        catalog.ForeignObjectKind
	options     []catalog.ForeignOption
	Attribute   int32 // Stable configured-column identity; zero is the table phase.
	raw         []parser.ForeignOption // Immutable statement syntax, resolved only at this column's turn.
	wrapper     ids.ObjectAddress
}

// HasValidator reports an already resolved executable job, not raw column syntax.
func (v Validation) HasValidator() bool { return v.procedure != nil }

// PrepareColumnValidation admits a scheduled column only after preceding phases.
// The returned options own the installation payload; ValidatorJob separately
// copies them, so application callbacks cannot mutate future catalog metadata.
func PrepareColumnValidation(read catalog.Read, scheduled Validation) (Validation, []catalog.ForeignOption, error) {
	if scheduled.Attribute <= 0 || scheduled.raw == nil || scheduled.wrapper == (ids.ObjectAddress{}) {
		return Validation{}, nil, fmt.Errorf("stuntgres: invalid foreign column schedule")
	}
	options, err := TransformOptions(nil, scheduled.raw)
	if err != nil {
		return Validation{}, nil, err
	}
	wrapper, ok := read.Foreign(scheduled.wrapper)
	if !ok {
		return Validation{}, nil, ids.ErrCatalogReplaced
	}
	job, _, err := ValidatorJob(read, wrapper, catalog.ForeignAttribute, options)
	job.Attribute = scheduled.Attribute
	return job, options, err
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

func Find(read catalog.Read, class ids.OID, name string) (*catalog.ForeignObject, error) {
	a := read.NameAddress(ids.NameKey{ClassID: class, Name: name})
	if d, ok := read.Foreign(a); ok {
		return d, nil
	}
	return nil, sqlError("42704", fmt.Sprintf(`%s "%s" does not exist`, catalog.ForeignLabel(class), name))
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
	if !ok || p.SQL == nil && (p.Foreign == nil || p.Foreign.Validator == nil) {
		return Validation{}, nil, sqlError("55000", fmt.Sprintf(`foreign-data wrapper "%s" has no registered validator`, wrapper.Name()))
	}
	return Validation{procedure: p, kind: kind, options: append([]catalog.ForeignOption{}, options...)}, p, nil
}

// SQLValidator borrows the caller's admitted statement to execute a SQL routine.
type SQLValidator func(*catalog.Procedure, catalog.ForeignObjectKind, []catalog.ForeignOption) error

func RunValidations(ctx context.Context, jobs []Validation, invoke SQLValidator) error {
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if job.raw != nil {
			return fmt.Errorf("stuntgres: unprepared foreign column validation")
		}
		if job.procedure != nil {
			var err error
			if job.procedure.SQL != nil {
				err = invoke(job.procedure, job.kind, job.options)
			} else {
				err = job.procedure.Foreign.Validator(ctx, job.kind, job.options)
			}
			if err != nil {
				return err
			}
		}
	}
	return ctx.Err()
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
