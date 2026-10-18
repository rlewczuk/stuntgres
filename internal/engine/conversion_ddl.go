package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (e *Engine) findUtilityConversion(auth AuthorizationView, name parser.ObjectName) (*conversionDescriptor, error) {
	path, err := e.lookupNamespaces(auth, name, conversionLookup)
	if err != nil {
		return nil, err
	}
	object := name.Parts[len(name.Parts)-1].Text
	for i := range path.count() {
		address := auth.catalog.NameAddress(ids.NameKey{ClassID: ids.ConversionClassID, Namespace: path.at(i), Name: object})
		if d, ok := auth.catalog.Conversion(address); ok {
			return d, nil
		}
	}
	return nil, sqlError("42704", fmt.Sprintf(`conversion "%s" does not exist`, objectNameText(name)))
}

// Rebuilding captured owners is the existing trusted native Restore boundary,
// not a general permission bypass for user DDL or arbitrary callers.
func (e *Engine) conversionCheckCreate(op *Operation, auth AuthorizationView, namespace ids.ObjectRef) error {
	err := auth.CheckCreate(namespace)
	if err == nil {
		return nil
	}
	denied, ok := err.(*sqlstate.Error)
	if op == nil || !op.restoreTableDestination || !ok || denied.Code != "42501" {
		return err
	}
	actor, actorErr := auth.roles.Descriptor(auth.state.Authenticated)
	if actorErr != nil || !actor.Superuser || e.importTargets == nil {
		return fmt.Errorf("stuntgres: invalid trusted restore conversion authority")
	}
	return nil
}

func (e *Engine) stageCreateConversionLocked(op *Operation, auth AuthorizationView, stage *CatalogStage, s *parser.CreateConversion) error {
	namespace, err := e.collationCreationNamespaceLocked(&auth, stage, s.Name)
	if err != nil {
		return err
	}
	if err = e.conversionCheckCreate(op, auth, namespace); err != nil {
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
	path, err := e.lookupNamespaces(auth, s.Function, functionLookup)
	if err != nil {
		return err
	}
	name := s.Function.Parts[len(s.Function.Parts)-1].Text
	procedure := procedureInNamespaces(auth.catalog, path, name, conversionSignature)
	functionName := objectNameText(s.Function)
	if procedure == nil {
		return sqlError("42883", "function "+functionName+"(integer, integer, cstring, internal, integer, boolean) does not exist")
	}
	if procedure.Result != 23 {
		return sqlError("42P17", "encoding conversion function "+functionName+" must return type integer")
	}
	if err = auth.Check(AccessRequirement{Object: e.catalog.domain.Ref(auth.catalog.Epoch(), procedure.address), Privileges: authorization.ExecutePrivilege}); err != nil {
		if d, ok := err.(*sqlstate.Error); ok && d.Code == "42501" {
			return sqlError("42501", "permission denied for function "+functionName)
		}
		return err
	}
	if procedure.Conversion.Name() == "" {
		return sqlError("0A000", "encoding conversion routine has no registered provider")
	}
	_, consumed, err := procedure.Conversion.Convert(op.Context(), "", source, destination)
	if err != nil {
		return err
	}
	if err = conversionProbeResult(functionName, consumed); err != nil {
		return err
	}
	object := s.Name.Parts[len(s.Name.Parts)-1].Text
	key := ids.NameKey{ClassID: ids.ConversionClassID, Namespace: namespace.Address, Name: object}
	if stage.lookupName(key) != (ids.ObjectAddress{}) {
		return sqlError("42710", fmt.Sprintf(`conversion "%s" already exists`, object))
	}
	if s.Default {
		for _, descriptor := range e.stageRead(stage).Descriptors() {
			if d, ok := descriptor.(*conversionDescriptor); ok && d.Namespace() == namespace.Address && d.Source == source && d.Destination == destination && d.DefaultConversion {
				return sqlError("42710", "default conversion for "+sqltype.EncodingName(source)+" to "+sqltype.EncodingName(destination)+" already exists")
			}
		}
	}
	oid, err := e.catalog.allocateOIDLocked()
	if err != nil {
		return err
	}
	revision, err := e.catalog.revisionLocked()
	if err != nil {
		return err
	}
	return stage.Create(&conversionDescriptor{OID: oid, name: object, namespace: namespace.Address, revision: revision, Owner: auth.state.CurrentUser, Source: source, Destination: destination, Handler: procedure.address, DefaultConversion: s.Default})
}

func conversionProbeResult(name string, consumed int) error {
	if consumed != 0 {
		return sqlError("42P17", "encoding conversion function "+name+" returned incorrect result for empty input")
	}
	return nil
}

func (e *Engine) stageAlterConversionLocked(op *Operation, auth AuthorizationView, stage *CatalogStage, s *parser.AlterConversion) error {
	d, err := e.findUtilityConversion(auth, s.Name)
	if err != nil {
		return err
	}
	var target authorization.PrincipalRef
	if s.Action == "owner" {
		target, err = schemaOwner(auth, s.Target.Text, s.OwnerIdentity)
		if err != nil {
			return err
		}
		if target == d.Owner {
			return nil
		}
	}
	var destination ids.ObjectRef
	if s.Action == "schema" {
		destination, err = e.collationCreationNamespaceLocked(&auth, stage, parser.ObjectName{Parts: []parser.Identifier{s.Target, {Text: d.name}}})
		if err != nil {
			return err
		}
		if destination.Address == d.Namespace() {
			return nil
		}
		sourceNamespace := auth.catalog.MustNamespace(d.Namespace())
		targetNamespace := auth.catalog.MustNamespace(destination.Address)
		if sourceNamespace.Temporary != (ids.SessionToken{}) || targetNamespace.Temporary != (ids.SessionToken{}) {
			return sqlError("0A000", "cannot move objects into or out of temporary schemas")
		}
	}
	ref := e.catalog.domain.Ref(auth.catalog.Epoch(), d.Address())
	if err = auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
		return err
	}
	next := *d
	switch s.Action {
	case "rename":
		if err = e.conversionCheckCreate(op, auth, e.catalog.domain.Ref(ref.Epoch, d.Namespace())); err != nil {
			return err
		}
		next.name = s.Target.Text
	case "schema":
		if err = e.conversionCheckCreate(op, auth, destination); err != nil {
			return err
		}
		next.namespace = destination.Address
	case "owner":
		if err = ownershipEligibility(auth, target, d.Namespace()); err != nil {
			return err
		}
		next.Owner = target
	default:
		return fmt.Errorf("stuntgres: invalid conversion alteration")
	}
	if address := stage.lookupName(descriptorNameKey(&next)); address != (ids.ObjectAddress{}) && (address != d.Address() || s.Action == "rename") {
		return sqlError("42710", fmt.Sprintf(`conversion "%s" already exists in schema "%s"`, next.name, auth.catalog.Get(next.Namespace()).Name()))
	}
	// CREATE alone checks default-pair uniqueness; legal schema moves may leave
	// multiple defaults and must never be rejected by a universal invariant.
	next.revision, err = e.catalog.revisionLocked()
	if err != nil {
		return err
	}
	return stage.Replace(&next)
}

func (e *Engine) stageDropConversionLocked(op *Operation, auth AuthorizationView, stage *CatalogStage, s *parser.DropConversion) error {
	seen := make(map[ids.ObjectAddress]bool, len(s.Names))
	for _, name := range s.Names {
		d, err := e.findUtilityConversion(auth, name)
		if err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); s.IfExists && ok && (diagnostic.Code == "42704" || diagnostic.Code == "3F000") {
				if diagnostic.Code == "3F000" {
					schemaNotice(op, "00000", diagnostic.Message+", skipping")
				} else {
					schemaNotice(op, "00000", fmt.Sprintf(`conversion "%s" does not exist, skipping`, objectNameText(name)))
				}
				continue
			}
			return err
		}
		if err = auth.Check(AccessRequirement{Object: e.catalog.domain.Ref(auth.catalog.Epoch(), d.Address()), OwnerOnly: true}); err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42501" {
				return sqlError("42501", "must be owner of conversion "+objectNameText(name))
			}
			return err
		}
		if d.seeded() {
			path, pathErr := e.referencePath(auth)
			if pathErr != nil {
				return pathErr
			}
			return sqlError("2BP01", "cannot drop "+dependencyObject(auth.catalog, d.Address(), path)+" because it is required by the database system")
		}
		if !seen[d.Address()] {
			seen[d.Address()] = true
			stage.domainTargets = append(stage.domainTargets, d.Address())
		}
	}
	stage.dependencyPath, _ = e.referencePath(auth)
	return nil
}
