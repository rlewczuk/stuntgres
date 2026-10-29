package routine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func checkRoutineOwner(h DDLHost, auth ddl.Authority, d *catalog.Procedure) error {
	err := ddl.Check(h, auth, access.Requirement{Object: ddl.ObjectRef(h, auth, d.Address()), OwnerOnly: true})
	if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42501" {
		kind := "function"
		if d.RoutineKind == catalog.ProcedureRoutine {
			kind = "procedure"
		}
		return sqlError("42501", "must be owner of "+kind+" "+d.Name())
	}
	return err
}

// StageRoutineNamespace is the lower utility mover shared by ALTER ROUTINE and
// ALTER EXTENSION. It retains declaration/provider identity and effective ACL.
func StageRoutineNamespace(h DDLHost, auth ddl.Authority, d *catalog.Procedure, destination ids.ObjectAddress) (ids.ObjectAddress, error) {
	previous := d.Namespace()
	if previous == destination {
		return previous, nil
	}
	view := h.AccessView(auth)
	if err := catalog.CheckSetNamespace(view.Catalog, previous, destination); err != nil {
		return ids.ObjectAddress{}, err
	}
	if err := checkRoutineOwner(h, auth, d); err != nil {
		return ids.ObjectAddress{}, err
	}
	if err := h.CheckCreateIn(auth, ddl.ObjectRef(h, auth, destination)); err != nil {
		return ids.ObjectAddress{}, err
	}
	next := *d
	next.ObjectNamespace = destination
	if address := h.Stage().LookupName(catalog.DescriptorNameKey(&next)); address != (ids.ObjectAddress{}) {
		canonical, err := utilityRoutineSignatureDiagnostic(h, auth, d.Signature)
		if err != nil {
			return ids.ObjectAddress{}, err
		}
		return ids.ObjectAddress{}, sqlError("42723", fmt.Sprintf("function %s(%s) already exists in schema %q", catalog.DependencyIdentifier(next.Name()), canonical, view.Catalog.MustNamespace(destination).Name()))
	}
	var err error
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return ids.ObjectAddress{}, err
	}
	if err := h.Stage().Replace(&next); err != nil {
		return ids.ObjectAddress{}, err
	}
	return previous, h.RecordDependencyPath(auth)
}
