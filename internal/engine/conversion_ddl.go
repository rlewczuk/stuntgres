package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (e *Engine) findUtilityConversion(auth AuthorizationView, name parser.ObjectName) (*catalog.Conversion, error) {
	path, err := e.lookupNamespaces(auth, name, analyze.ConversionLookup)
	if err != nil {
		return nil, err
	}
	object := name.Parts[len(name.Parts)-1].Text
	for i := range path.Count() {
		address := auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.ConversionClassID, Namespace: path.At(i), Name: object})
		if d, ok := auth.Catalog.Conversion(address); ok {
			return d, nil
		}
	}
	return nil, sqlError("42704", fmt.Sprintf(`conversion "%s" does not exist`, catalog.ObjectNameText(name)))
}

// Rebuilding captured owners is the existing trusted native Restore boundary,
// not a general permission bypass for user DDL or arbitrary callers.
func (e *Engine) conversionCheckCreate(op *Operation, auth AuthorizationView, namespace ids.ObjectRef) error {
	err := auth.CheckCreate(namespace)
	if err == nil {
		return nil
	}
	denied, ok := err.(*sqlstate.Error)
	if op == nil || !op.replayOrZero().tableDestination || !ok || denied.Code != "42501" {
		return err
	}
	actor, actorErr := auth.Roles.Descriptor(auth.State.Authenticated)
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
	path, err := e.lookupNamespaces(auth, s.Function, analyze.FunctionLookup)
	if err != nil {
		return err
	}
	name := s.Function.Parts[len(s.Function.Parts)-1].Text
	procedure := procedureInNamespaces(auth.Catalog, path, name, catalog.ConversionSignature)
	functionName := catalog.ObjectNameText(s.Function)
	if procedure == nil {
		return sqlError("42883", "function "+functionName+"(integer, integer, cstring, internal, integer, boolean) does not exist")
	}
	if procedure.Result != 23 {
		return sqlError("42P17", "encoding conversion function "+functionName+" must return type integer")
	}
	if err = auth.Check(access.Requirement{Object: e.catalog.Domain.Ref(auth.Catalog.Epoch(), procedure.Address()), Privileges: authorization.ExecutePrivilege}); err != nil {
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
	if err = typedef.ConversionProbeResult(functionName, consumed); err != nil {
		return err
	}
	object := s.Name.Parts[len(s.Name.Parts)-1].Text
	key := ids.NameKey{ClassID: ids.ConversionClassID, Namespace: namespace.Address, Name: object}
	if stage.LookupName(key) != (ids.ObjectAddress{}) {
		return sqlError("42710", fmt.Sprintf(`conversion "%s" already exists`, object))
	}
	if s.Default {
		for _, descriptor := range e.stageRead(stage).Descriptors() {
			if d, ok := descriptor.(*catalog.Conversion); ok && d.Namespace() == namespace.Address && d.Source == source && d.Destination == destination && d.DefaultConversion {
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
	return stage.Create(&catalog.Conversion{OID: oid, ObjectName: object, ObjectNamespace: namespace.Address, ObjectRevision: revision, Owner: auth.State.CurrentUser, Source: source, Destination: destination, Handler: procedure.Address(), DefaultConversion: s.Default})
}

func (e *Engine) stageAlterConversionLocked(op *Operation, auth AuthorizationView, stage *CatalogStage, s *parser.AlterConversion) error {
	d, err := e.findUtilityConversion(auth, s.Name)
	if err != nil {
		return err
	}
	var target authorization.PrincipalRef
	if s.Action == "owner" {
		target, err = access.SchemaOwner(auth.View, s.Target.Text, s.OwnerIdentity)
		if err != nil {
			return err
		}
		if target == d.Owner {
			return nil
		}
	}
	var destination ids.ObjectRef
	if s.Action == "schema" {
		destination, err = e.collationCreationNamespaceLocked(&auth, stage, parser.ObjectName{Parts: []parser.Identifier{s.Target, {Text: d.Name()}}})
		if err != nil {
			return err
		}
		if destination.Address == d.Namespace() {
			return nil
		}
		sourceNamespace := auth.Catalog.MustNamespace(d.Namespace())
		targetNamespace := auth.Catalog.MustNamespace(destination.Address)
		if sourceNamespace.Temporary != (ids.SessionToken{}) || targetNamespace.Temporary != (ids.SessionToken{}) {
			return sqlError("0A000", "cannot move objects into or out of temporary schemas")
		}
	}
	ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), d.Address())
	if err = auth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
		return err
	}
	next := *d
	switch s.Action {
	case "rename":
		if err = e.conversionCheckCreate(op, auth, e.catalog.Domain.Ref(ref.Epoch, d.Namespace())); err != nil {
			return err
		}
		next.ObjectName = s.Target.Text
	case "schema":
		if err = e.conversionCheckCreate(op, auth, destination); err != nil {
			return err
		}
		next.ObjectNamespace = destination.Address
	case "owner":
		if err = access.OwnershipEligibility(auth.View, auth, target, d.Namespace()); err != nil {
			return err
		}
		next.Owner = target
	default:
		return fmt.Errorf("stuntgres: invalid conversion alteration")
	}
	if address := stage.LookupName(catalog.DescriptorNameKey(&next)); address != (ids.ObjectAddress{}) && (address != d.Address() || s.Action == "rename") {
		return sqlError("42710", fmt.Sprintf(`conversion "%s" already exists in schema "%s"`, next.Name(), auth.Catalog.Get(next.Namespace()).Name()))
	}
	// CREATE alone checks default-pair uniqueness; legal schema moves may leave
	// multiple defaults and must never be rejected by a universal invariant.
	next.ObjectRevision, err = e.catalog.revisionLocked()
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
					schemaNotice(op, "00000", fmt.Sprintf(`conversion "%s" does not exist, skipping`, catalog.ObjectNameText(name)))
				}
				continue
			}
			return err
		}
		if err = auth.Check(access.Requirement{Object: e.catalog.Domain.Ref(auth.Catalog.Epoch(), d.Address()), OwnerOnly: true}); err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42501" {
				return sqlError("42501", "must be owner of conversion "+catalog.ObjectNameText(name))
			}
			return err
		}
		if d.Seeded() {
			path, pathErr := e.referencePath(auth)
			if pathErr != nil {
				return pathErr
			}
			return sqlError("2BP01", "cannot drop "+dependencyObject(auth.Catalog, d.Address(), path)+" because it is required by the database system")
		}
		if !seen[d.Address()] {
			seen[d.Address()] = true
			stage.domainTargets = append(stage.domainTargets, d.Address())
		}
	}
	stage.dependencyPath, _ = e.referencePath(auth)
	return nil
}
