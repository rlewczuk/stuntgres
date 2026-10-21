package typedef

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func findUtilityConversion(h StageHost, auth ddl.Authority, name parser.ObjectName) (*catalog.Conversion, error) {
	path, err := h.SearchNamespaces(auth, name, ids.ConversionClassID)
	if err != nil {
		return nil, err
	}
	view := h.AccessView(auth)
	object := name.Parts[len(name.Parts)-1].Text
	for _, namespace := range path {
		address := view.Catalog.NameAddress(ids.NameKey{ClassID: ids.ConversionClassID, Namespace: namespace, Name: object})
		if d, ok := view.Catalog.Conversion(address); ok {
			return d, nil
		}
	}
	return nil, sqlError("42704", fmt.Sprintf(`conversion "%s" does not exist`, catalog.ObjectNameText(name)))
}

func StageCreateConversionLocked(h StageHost, auth ddl.Authority, s *parser.CreateConversion) error {
	namespace, auth, err := h.CreationNamespace(auth, s.Name)
	if err != nil {
		return err
	}
	if err = h.CheckCreateIn(auth, namespace); err != nil {
		return err
	}
	source := sqltype.EncodingID(s.Source)
	if source < 0 {
		return sqlError("42704", fmt.Sprintf(`source encoding "%s" does not exist`, s.Source))
	}
	destination := sqltype.EncodingID(s.Destination)
	if destination < 0 {
		return sqlError("42704", fmt.Sprintf(`destination encoding "%s" does not exist`, s.Destination))
	}
	if source == 0 || destination == 0 {
		return sqlError("42P17", `encoding conversion to or from "SQL_ASCII" is not supported`)
	}
	procedure, err := h.LookupFunction(auth, s.Function, catalog.ConversionSignature)
	if err != nil {
		return err
	}
	functionName := catalog.ObjectNameText(s.Function)
	if procedure == nil {
		return sqlError("42883", "function "+functionName+"(integer, integer, cstring, internal, integer, boolean) does not exist")
	}
	if procedure.Result != 23 {
		return sqlError("42P17", "encoding conversion function "+functionName+" must return type integer")
	}
	if err = ddl.Check(h, auth, access.Requirement{Object: ddl.ObjectRef(h, auth, procedure.Address()), Privileges: authorization.ExecutePrivilege}); err != nil {
		if d, ok := err.(*sqlstate.Error); ok && d.Code == "42501" {
			return sqlError("42501", "permission denied for function "+functionName)
		}
		return err
	}
	if procedure.Conversion.Name() == "" {
		return sqlError("0A000", "encoding conversion routine has no registered provider")
	}
	_, consumed, err := procedure.Conversion.Convert(h.Context(), "", source, destination)
	if err != nil {
		return err
	}
	if err = ConversionProbeResult(functionName, consumed); err != nil {
		return err
	}
	object := s.Name.Parts[len(s.Name.Parts)-1].Text
	key := ids.NameKey{ClassID: ids.ConversionClassID, Namespace: namespace.Address, Name: object}
	if h.Stage().LookupName(key) != (ids.ObjectAddress{}) {
		return sqlError("42710", fmt.Sprintf(`conversion "%s" already exists`, object))
	}
	if s.Default {
		for _, descriptor := range h.StageRead().Descriptors() {
			if d, ok := descriptor.(*catalog.Conversion); ok && d.Namespace() == namespace.Address && d.Source == source && d.Destination == destination && d.DefaultConversion {
				return sqlError("42710", "default conversion for "+sqltype.EncodingName(source)+" to "+sqltype.EncodingName(destination)+" already exists")
			}
		}
	}
	oid, err := h.AllocateOID()
	if err != nil {
		return err
	}
	revision, err := h.Revision()
	if err != nil {
		return err
	}
	return h.Stage().Create(&catalog.Conversion{OID: oid, ObjectName: object, ObjectNamespace: namespace.Address, ObjectRevision: revision, Owner: h.AccessView(auth).State.CurrentUser, Source: source, Destination: destination, Handler: procedure.Address(), DefaultConversion: s.Default})
}

func StageAlterConversionLocked(h StageHost, auth ddl.Authority, s *parser.AlterConversion) error {
	d, err := findUtilityConversion(h, auth, s.Name)
	if err != nil {
		return err
	}
	view := h.AccessView(auth)
	var target authorization.PrincipalRef
	if s.Action == "owner" {
		target, err = access.SchemaOwner(view, s.Target.Text, s.OwnerIdentity)
		if err != nil {
			return err
		}
		if target == d.Owner {
			return nil
		}
	}
	var destination ids.ObjectRef
	if s.Action == "schema" {
		destination, auth, err = h.CreationNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{s.Target, {Text: d.Name()}}})
		if err != nil {
			return err
		}
		if destination.Address == d.Namespace() {
			return nil
		}
		view = h.AccessView(auth)
		sourceNamespace := view.Catalog.MustNamespace(d.Namespace())
		targetNamespace := view.Catalog.MustNamespace(destination.Address)
		if sourceNamespace.Temporary != (ids.SessionToken{}) || targetNamespace.Temporary != (ids.SessionToken{}) {
			return sqlError("0A000", "cannot move objects into or out of temporary schemas")
		}
	}
	ref := ddl.ObjectRef(h, auth, d.Address())
	if err = ddl.Check(h, auth, access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
		return err
	}
	next := *d
	switch s.Action {
	case "rename":
		if err = h.CheckCreateIn(auth, view.Owner.Domain.Ref(ref.Epoch, d.Namespace())); err != nil {
			return err
		}
		next.ObjectName = s.Target.Text
	case "schema":
		if err = h.CheckCreateIn(auth, destination); err != nil {
			return err
		}
		next.ObjectNamespace = destination.Address
	case "owner":
		if err = access.OwnershipEligibility(view, auth, target, d.Namespace()); err != nil {
			return err
		}
		next.Owner = target
	default:
		return fmt.Errorf("stuntgres: invalid conversion alteration")
	}
	if address := h.Stage().LookupName(catalog.DescriptorNameKey(&next)); address != (ids.ObjectAddress{}) && (address != d.Address() || s.Action == "rename") {
		return sqlError("42710", fmt.Sprintf(`conversion "%s" already exists in schema "%s"`, next.Name(), view.Catalog.Get(next.Namespace()).Name()))
	}
	// CREATE alone checks default-pair uniqueness; legal schema moves may leave
	// multiple defaults and must never be rejected by a universal invariant.
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	return h.Stage().Replace(&next)
}

func StageDropConversionLocked(h StageHost, auth ddl.Authority, s *parser.DropConversion) error {
	seen := make(map[ids.ObjectAddress]bool, len(s.Names))
	for _, name := range s.Names {
		d, err := findUtilityConversion(h, auth, name)
		if err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); s.IfExists && ok && (diagnostic.Code == "42704" || diagnostic.Code == "3F000") {
				if diagnostic.Code == "3F000" {
					ddl.Notice(h, "00000", diagnostic.Message+", skipping")
				} else {
					ddl.Notice(h, "00000", fmt.Sprintf(`conversion "%s" does not exist, skipping`, catalog.ObjectNameText(name)))
				}
				continue
			}
			return err
		}
		if err = ddl.Check(h, auth, access.Requirement{Object: ddl.ObjectRef(h, auth, d.Address()), OwnerOnly: true}); err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42501" {
				return sqlError("42501", "must be owner of conversion "+catalog.ObjectNameText(name))
			}
			return err
		}
		if d.Seeded() {
			object, pathErr := h.DependencyObject(auth, d.Address())
			if pathErr != nil {
				return pathErr
			}
			return sqlError("2BP01", "cannot drop "+object+" because it is required by the database system")
		}
		if !seen[d.Address()] {
			seen[d.Address()] = true
			h.DropTarget(d.Address())
		}
	}
	h.RecordDependencyPath(auth)
	return nil
}
