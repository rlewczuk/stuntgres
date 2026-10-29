package routine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// StageAlterRoutine changes a descriptor in place by address. Declaration and
// provider payloads remain immutable; the shared stage owns publication/fences.
func StageAlterRoutine(h DDLHost, auth ddl.Authority, s *parser.AlterRoutine) error {
	target := s.Routine
	d, canonical, err := ResolveUtilityRoutine(h, auth, s.Kind, target)
	if err != nil {
		return err
	}
	next := *d
	view := h.AccessView(auth)
	if s.Action == "owner" {
		owner, err := access.SchemaOwner(view, s.Target.Text, s.OwnerIdentity)
		if err != nil {
			return err
		}
		if owner == d.Owner {
			ddl.RecordCommand(h.Stage(), "ALTER "+strings.ToUpper(s.Kind), d.Address())
			return nil
		}
		next.Owner = owner
	}
	if s.Action == "schema" {
		var ns ids.ObjectRef
		ns, auth, err = h.CreationNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{s.Target, {Text: d.Name()}}})
		if err != nil {
			return err
		}
		if err = h.CheckCreateIn(auth, ns); err != nil {
			return err
		}
		if _, err = StageRoutineNamespace(h, auth, d, ns.Address); err != nil {
			return err
		}
		ddl.RecordCommand(h.Stage(), "ALTER "+strings.ToUpper(s.Kind), d.Address())
		return nil
	}
	if err = checkRoutineOwner(h, auth, d); err != nil {
		return err
	}
	switch s.Action {
	case "rename":
		if err = h.CheckCreateIn(auth, ddl.ObjectRef(h, auth, d.Namespace())); err != nil {
			return err
		}
		next.ObjectName = s.Target.Text
	case "owner":
		if err = access.OwnershipEligibility(view, auth, next.Owner, d.Namespace()); err != nil {
			return err
		}
		next.ACL = authorization.SchemaACLOwner(d.ACL, d.Owner, next.Owner)
	case "attributes":
		if err = checkRoutineProviderAttributes(d, s.Attributes); err != nil {
			return err
		}
		if err = normalizeRoutineAttributes(h, auth, &next, s.Attributes, d.ReturnsSet()); err != nil {
			return err
		}
	}
	if s.Action == "rename" || s.Action == "schema" {
		if address := h.Stage().LookupName(catalog.DescriptorNameKey(&next)); address != (ids.ObjectAddress{}) {
			if !target.SignatureSpecified && d.Signature != "" {
				canonical, err = utilityRoutineSignatureDiagnostic(h, auth, d.Signature)
				if err != nil {
					return err
				}
			}
			return sqlError("42723", fmt.Sprintf(`function %s(%s) already exists in schema "%s"`, catalog.DependencyIdentifier(next.Name()), canonical, view.Catalog.MustNamespace(next.Namespace()).Name()))
		}
	}
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	if err = h.Stage().Replace(&next); err != nil {
		return err
	}
	ddl.RecordCommand(h.Stage(), "ALTER "+strings.ToUpper(s.Kind), d.Address())
	return h.RecordDependencyPath(auth)
}
