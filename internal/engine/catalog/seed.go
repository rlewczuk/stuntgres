package catalog

import (
	"iter"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// The builtin seed's install steps write a state still in its bulk load,
// before the store is shared. Owners and the compiled tables of packages that
// import catalog (routine, typedef) come in as values.

// installSeedDescriptor sets d and binds its descriptor name.
func installSeedDescriptor(state *State, d Descriptor) {
	state.addSeedDescriptor(d.Address(), d)
	state.bindSeedName(d)
}

// builtinNamespaces are the namespaces the builtin seed installs.
var builtinNamespaces = [...]struct {
	oid  ids.OID
	name string
}{{ids.PGCatalogNamespaceOID, "pg_catalog"}, {ids.PGToastNamespaceOID, "pg_toast"}, {ids.PublicNamespaceOID, "public"}, {13229, "information_schema"}}

// BuiltinNamespaces iterates the addresses of the namespaces
// InstallBuiltinNamespaces installs, the only namespaces of a builtin seed.
func BuiltinNamespaces() iter.Seq[ids.ObjectAddress] {
	return func(yield func(ids.ObjectAddress) bool) {
		for _, ns := range builtinNamespaces {
			if !yield(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ns.oid}) {
				return
			}
		}
	}
}

// InstallBuiltinNamespaces installs pg_catalog, pg_toast, public and
// information_schema. publicOwner owns public; owner owns the rest.
func InstallBuiltinNamespaces(state *State, owner, publicOwner authorization.PrincipalRef) {
	for _, ns := range builtinNamespaces {
		owner := owner
		if ns.oid == ids.PublicNamespaceOID {
			owner = publicOwner
		}
		d := &Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ns.oid}, ObjectName: ns.name, ObjectRevision: 1, Owner: owner}
		if ns.oid != ids.PGToastNamespaceOID {
			d.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
				{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege},
				{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege},
			}}
		}
		d.Comment = NamespaceCommentBaseline(ns.oid)
		state.SetDescriptor(d.Address(), d)
		state.bindSeedName(d)
	}
}

// InstallBuiltinTypes installs a type descriptor for each builtin SQL type.
func InstallBuiltinTypes(state *State, types []*sqltype.Descriptor, owner authorization.PrincipalRef) {
	for _, typ := range types {
		d := &Type{SQLType: typ, Comment: BuiltinTypeComment(typ.ID()), Array: typ.AssociatedArray(), Owner: owner}
		if typ.IsDomain() && typ.Concrete() {
			d.ObjectRevision = 1
		}
		if typ.NamespaceName() == "information_schema" {
			d.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 13229}
		}
		installSeedDescriptor(state, d)
	}
}

// InstallBuiltinCollations installs copies of BuiltinCollations.
func InstallBuiltinCollations(state *State, owner authorization.PrincipalRef) {
	for i := range BuiltinCollations {
		d := new(Collation)
		*d = BuiltinCollations[i]
		d.Owner = owner
		installSeedDescriptor(state, d)
	}
}

// InstallBuiltinConversions installs copies of BuiltinConversions in
// pg_
func InstallBuiltinConversions(state *State, owner authorization.PrincipalRef) {
	for i := range BuiltinConversions {
		d := new(Conversion)
		*d = BuiltinConversions[i]
		d.Owner = owner
		d.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
		d.ObjectRevision = 1
		installSeedDescriptor(state, d)
	}
}

// InstallBuiltinProcedures installs each group of builtin routine descriptors
// in order, then the native access methods.
func InstallBuiltinProcedures(state *State, groups ...[]Procedure) {
	for _, procedures := range groups {
		for i := range procedures {
			installSeedDescriptor(state, &procedures[i])
		}
	}
	InstallBuiltinAccessMethods(state)
}

// InstallBuiltinOperators installs the shared operator-name index and the
// seeded operator descriptors.
func InstallBuiltinOperators(state *State, names *OperatorNameIndex, operators []Operator) {
	state.SetOperatorNames(names)
	for i := range operators {
		installSeedDescriptor(state, &operators[i])
	}
}

// InstallBuiltinLanguagesLocked installs internal, c and sql, then plpgsql
// with its call, inline and validator handlers at OIDs allocated from s.
func (s *Store) InstallBuiltinLanguagesLocked(owner authorization.PrincipalRef) error {
	for _, seed := range []struct {
		oid       ids.OID
		name      string
		validator ids.OID
		trusted   bool
	}{{12, "internal", 2246, false}, {13, "c", 2247, false}, {14, "sql", 2248, true}} {
		installSeedDescriptor(s.State, &Language{ObjectAddress: ids.ObjectAddress{ClassID: ids.LanguageClassID, ObjectID: seed.oid}, ObjectRevision: 1, ObjectName: seed.name, Owner: owner, Trusted: seed.trusted, Validator: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: seed.validator}})
	}
	languageOID, err := s.AllocateOIDLocked()
	if err != nil {
		return err
	}
	extensionOID, err := s.AllocateOIDLocked()
	if err != nil {
		return err
	}
	extension := &Extension{ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: extensionOID}, ObjectRevision: 1, ObjectName: "plpgsql", Version: "1.0", TargetNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, Owner: owner, Members: make([]ids.ObjectAddress, 4)}
	if control := s.Extensions["plpgsql"].Control(); control.Comment != nil {
		extension.Comment = *control.Comment
	}
	installation := &Installation{Cohort: extension.Address(), Provider: "plpgsql", Version: "1.0", Implementation: s.Extensions["plpgsql"], Members: make([]ids.ObjectAddress, 4), Declarations: make([]Descriptor, 4)}
	extension.Installation = installation
	language := &Language{ObjectAddress: ids.ObjectAddress{ClassID: ids.LanguageClassID, ObjectID: languageOID}, ObjectRevision: 1, ObjectName: "plpgsql", Owner: owner, Procedural: true, Trusted: true}
	language.Extension = extension.Address()
	language.Origin = &InstalledOrigin{Installation: installation, Declaration: 3}
	for i, kind := range []PLHandler{PLCallHandler, PLInlineHandler, PLValidator} {
		oid, err := s.AllocateOIDLocked()
		if err != nil {
			return err
		}
		handler := kind.ReleasedProcedure()
		handler.ObjectAddress = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		handler.ObjectNamespace = extension.TargetNamespace
		handler.ObjectRevision = 1
		handler.Owner = owner
		handler.Extension = extension.Address()
		canonical := handler
		installation.Declarations[i] = &canonical
		handler.Origin = &InstalledOrigin{Installation: installation, Declaration: i}
		extension.Members[i] = handler.Address()
		installation.Members[i] = handler.Address()
		installSeedDescriptor(s.State, &handler)
		switch kind {
		case PLCallHandler:
			language.Call = handler.Address()
		case PLInlineHandler:
			language.Inline = handler.Address()
		case PLValidator:
			language.Validator = handler.Address()
		}
	}
	extension.Members[3] = language.Address()
	installation.Members[3] = language.Address()
	canonicalLanguage := *language
	canonicalLanguage.Origin = nil
	installation.Declarations[3] = &canonicalLanguage
	installation.sealed = installation
	installSeedDescriptor(s.State, extension)
	installSeedDescriptor(s.State, language)
	return nil
}

// InstallCatalogSource installs a catalog relation. A same-named seeded row
// type, and its array, become owned by the relation.
func InstallCatalogSource(state *State, d *Relation) {
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
	state.bindSeedName(d)
}
