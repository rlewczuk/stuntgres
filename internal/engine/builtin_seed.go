package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// The install steps below write a state still in the seed's bulk load, before
// the engine is shared. Owners and the compiled tables of packages that import
// catalog come in as values.

// installSeedDescriptor sets d and binds its descriptor name.
func installSeedDescriptor(state *catalog.State, d catalog.Descriptor) {
	state.SetDescriptor(d.Address(), d)
	state.BindName(catalog.DescriptorNameKey(d), d.Address())
}

// installBuiltinNamespaces installs pg_catalog, pg_toast, public and
// information_schema. publicOwner owns public; owner owns the rest.
func installBuiltinNamespaces(state *catalog.State, owner, publicOwner authorization.PrincipalRef) {
	for _, ns := range []struct {
		oid  ids.OID
		name string
	}{{ids.PGCatalogNamespaceOID, "pg_catalog"}, {ids.PGToastNamespaceOID, "pg_toast"}, {ids.PublicNamespaceOID, "public"}, {13229, "information_schema"}} {
		owner := owner
		if ns.oid == ids.PublicNamespaceOID {
			owner = publicOwner
		}
		d := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ns.oid}, ObjectName: ns.name, ObjectRevision: 1, Owner: owner}
		if ns.oid != ids.PGToastNamespaceOID {
			d.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
				{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege},
				{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege},
			}}
		}
		d.Comment = catalog.NamespaceCommentBaseline(ns.oid)
		state.SetDescriptor(d.Address(), d)
		state.BindName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: d.Name()}, d.Address())
	}
}

// installBuiltinTypes installs a type descriptor for each builtin SQL type.
func installBuiltinTypes(state *catalog.State, types []*sqltype.Descriptor, owner authorization.PrincipalRef) {
	for _, typ := range types {
		d := &catalog.Type{SQLType: typ, Comment: catalog.BuiltinTypeComment(typ.ID()), Array: typ.AssociatedArray(), Owner: owner}
		if typ.IsDomain() && typ.Concrete() {
			d.ObjectRevision = 1
		}
		if typ.NamespaceName() == "information_schema" {
			d.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 13229}
		}
		installSeedDescriptor(state, d)
	}
}

// installBuiltinCollations installs copies of catalog.BuiltinCollations.
func installBuiltinCollations(state *catalog.State, owner authorization.PrincipalRef) {
	for i := range catalog.BuiltinCollations {
		d := new(catalog.Collation)
		*d = catalog.BuiltinCollations[i]
		d.Owner = owner
		installSeedDescriptor(state, d)
	}
}

// installBuiltinConversions installs copies of catalog.BuiltinConversions in
// pg_catalog.
func installBuiltinConversions(state *catalog.State, owner authorization.PrincipalRef) {
	for i := range catalog.BuiltinConversions {
		d := new(catalog.Conversion)
		*d = catalog.BuiltinConversions[i]
		d.Owner = owner
		d.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
		d.ObjectRevision = 1
		installSeedDescriptor(state, d)
	}
}

// installBuiltinProcedures installs each group of builtin routine descriptors
// in order, then the native access methods.
func installBuiltinProcedures(state *catalog.State, groups ...[]catalog.Procedure) {
	for _, procedures := range groups {
		for i := range procedures {
			installSeedDescriptor(state, &procedures[i])
		}
	}
	catalog.InstallBuiltinAccessMethods(state)
}

// installBuiltinOperators installs the shared operator-name index and the
// seeded operator descriptors.
func installBuiltinOperators(state *catalog.State, names *catalog.OperatorNameIndex, operators []catalog.Operator) {
	state.SetOperatorNames(names)
	for i := range operators {
		installSeedDescriptor(state, &operators[i])
	}
}

// installBuiltinTablespaces installs catalog.BuiltinTablespaces.
func installBuiltinTablespaces(state *catalog.State, owner authorization.PrincipalRef) {
	for _, builtin := range catalog.BuiltinTablespaces {
		installSeedDescriptor(state, &catalog.Tablespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: builtin.OID}, ObjectRevision: 1, ObjectName: builtin.Name, Owner: owner})
	}
}

// installBuiltinLanguagesLocked installs internal, c and sql, then plpgsql
// with its call, inline and validator handlers at allocated OIDs.
func installBuiltinLanguagesLocked(s *catalog.Store, owner authorization.PrincipalRef) error {
	for _, seed := range []struct {
		oid       ids.OID
		name      string
		validator ids.OID
		trusted   bool
	}{{12, "internal", 2246, false}, {13, "c", 2247, false}, {14, "sql", 2248, true}} {
		installSeedDescriptor(s.State, &catalog.Language{ObjectAddress: ids.ObjectAddress{ClassID: ids.LanguageClassID, ObjectID: seed.oid}, ObjectRevision: 1, ObjectName: seed.name, Owner: owner, Trusted: seed.trusted, Validator: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: seed.validator}})
	}
	languageOID, err := s.AllocateOIDLocked()
	if err != nil {
		return err
	}
	language := &catalog.Language{ObjectAddress: ids.ObjectAddress{ClassID: ids.LanguageClassID, ObjectID: languageOID}, ObjectRevision: 1, ObjectName: "plpgsql", Owner: owner, Procedural: true, Trusted: true}
	for _, kind := range []catalog.PLHandler{catalog.PLCallHandler, catalog.PLInlineHandler, catalog.PLValidator} {
		oid, err := s.AllocateOIDLocked()
		if err != nil {
			return err
		}
		name, signature, result := kind.Declaration()
		handler := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, ObjectRevision: 1, ObjectName: name, Signature: signature, Result: result, Owner: owner, RoutineKind: catalog.FunctionRoutine, Volatility: expr.Volatile, PLHandler: kind}
		installSeedDescriptor(s.State, handler)
		switch kind {
		case catalog.PLCallHandler:
			language.Call = handler.Address()
		case catalog.PLInlineHandler:
			language.Inline = handler.Address()
		case catalog.PLValidator:
			language.Validator = handler.Address()
		}
	}
	installSeedDescriptor(s.State, language)
	return nil
}

// installCatalogSource installs a catalog relation. A same-named seeded row
// type, and its array, become owned by the relation.
func installCatalogSource(state *catalog.State, d *catalog.Relation) {
	if address, ok := state.LookupName(ids.NameKey{ClassID: ids.TypeClassID, Namespace: d.Namespace(), Name: d.Name()}); ok {
		row := state.MustType(address)
		row.OwningRelation, row.Owner = d.Address(), d.Owner
		d.RowType = row.SQLType.ID()
		if row.Array != 0 {
			array := state.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)})
			array.OwningRelation, array.Owner = d.Address(), d.Owner
		}
	}
	state.SetDescriptor(d.Address(), d)
	state.BindName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: d.Namespace(), Name: d.Name()}, d.Address())
}
