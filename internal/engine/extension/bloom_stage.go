package extension

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func (p *BloomProvider) Stage(stage Stage, e *catalog.Extension, owner authorization.PrincipalRef) error {
	e.Members = make([]ids.ObjectAddress, 6)
	for i, class := range [...]ids.OID{
		ids.ProcedureClassID, ids.AccessMethodClassID,
		ids.OperatorFamilyClassID, ids.OperatorClassCatalogID,
		ids.OperatorFamilyClassID, ids.OperatorClassCatalogID,
	} {
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		e.Members[i] = ids.ObjectAddress{ClassID: class, ObjectID: oid}
	}
	revision, err := stage.Revision()
	if err != nil {
		return err
	}
	handler := &catalog.Procedure{
		ObjectAddress: e.Members[0], ObjectNamespace: e.TargetNamespace,
		Extension: e.Address(), ObjectRevision: revision, ObjectName: "blhandler",
		Owner: owner, Signature: "2281", Result: 325,
		RoutineKind: catalog.FunctionRoutine, Volatility: expr.Volatile,
		Implementation: p, AccessMethod: catalog.BloomAccessMethod,
		Installed: catalog.InstalledRoutineDeclaration{Parallel: 'u'},
		Display: catalog.RoutineDisplay{
			Language: catalog.CRoutineLanguage, Source: "blhandler",
			Binary: "$libdir/bloom", HasBinary: true,
		},
	}
	if stage.LookupName(catalog.DescriptorNameKey(handler)) != (ids.ObjectAddress{}) {
		return MemberConflict(handler, e.TargetNamespace)
	}
	if err := stage.Create(handler); err != nil {
		return err
	}
	revision, err = stage.Revision()
	if err != nil {
		return err
	}
	method := &catalog.AccessMethod{
		ObjectAddress: e.Members[1], Extension: e.Address(), ObjectRevision: revision,
		ObjectName: "bloom", Handler: handler.Address().ObjectID, Native: catalog.BloomAccessMethod,
	}
	if stage.LookupName(catalog.DescriptorNameKey(method)) != (ids.ObjectAddress{}) {
		return &sqlstate.Error{
			Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710",
			Message: `access method "bloom" already exists`,
			Context: "SQL statement \"CREATE ACCESS METHOD bloom TYPE INDEX HANDLER blhandler\"\nextension script file \"bloom--1.0.sql\", near line 12",
		}
	}
	if err := stage.Create(method); err != nil {
		return err
	}
	for i, spec := range [...]struct {
		name                  string
		input, equality, hash ids.OID
	}{{"int4_ops", 23, 96, 450}, {"text_ops", 25, 98, 400}} {
		familySlot, classSlot := 2+i*2, 3+i*2
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		family := &catalog.OperatorFamily{
			ObjectAddress: e.Members[familySlot], ObjectNamespace: e.TargetNamespace,
			Extension: e.Address(), ObjectRevision: revision, ObjectName: spec.name,
			Method: method.Address().ObjectID, Owner: owner,
		}
		if err := stage.Create(family); err != nil {
			return err
		}
		revision, err = stage.Revision()
		if err != nil {
			return err
		}
		class := &catalog.OperatorClass{
			ObjectAddress: e.Members[classSlot], ObjectNamespace: e.TargetNamespace,
			Extension: e.Address(), ObjectRevision: revision, ObjectName: spec.name,
			Method: method.Address().ObjectID, Family: family.Address().ObjectID,
			InputType: spec.input, Default: true, Owner: owner,
		}
		if err := stage.Create(class); err != nil {
			return err
		}
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		revision, err = stage.Revision()
		if err != nil {
			return err
		}
		if err := stage.Create(&catalog.OperatorFamilyOperator{
			ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodOperatorClassID, ObjectID: oid},
			ObjectRevision: revision, Family: class.Family, Left: spec.input, Right: spec.input,
			Operator: spec.equality, Method: class.Method, InternalOwner: class.Address(),
			Number: 1, Purpose: 's',
		}); err != nil {
			return err
		}
		oid, err = stage.AllocateOID()
		if err != nil {
			return err
		}
		revision, err = stage.Revision()
		if err != nil {
			return err
		}
		if err := stage.Create(&catalog.OperatorFamilySupport{
			ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodProcedureClassID, ObjectID: oid},
			ObjectRevision: revision, Family: class.Family, Left: spec.input, Right: spec.input,
			Function: spec.hash, InternalOwner: class.Address(), Number: 1, CatalogVisible: true,
		}); err != nil {
			return err
		}
	}
	return nil
}
