package catalog

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Bootstrap metadata is never consulted by live name/default/class selection.
func InstallBuiltinClasses(state *State, owner authorization.PrincipalRef) {
	classes := make([]OperatorClass, sqltype.IndexClassMetadataCount())
	for i := range classes {
		f, ok := sqltype.IndexClassMetadataAt(i)
		if !ok {
			panic("missing builtin operator class seed")
		}
		d := &classes[i]
		*d = OperatorClass{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: ids.OID(f.OID)}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, ObjectRevision: 1, ObjectName: f.Name, Owner: owner, Method: ids.OID(f.Method), Family: ids.OID(f.Family), InputType: ids.OID(f.Input), StorageType: ids.OID(f.KeyType), Default: f.Default}
		state.addSeedDescriptor(d.Address(), d)
		state.bindSeedName(d)
	}
}
func OperatorClassSeeded(d Descriptor) bool {
	if _, ok := d.(*OperatorClass); !ok {
		return false
	}
	_, ok := OperatorClassSeedName(d.Address().ObjectID)
	return ok
}

var builtinClassNamesByOID = func() map[ids.OID]string {
	names := make(map[ids.OID]string, sqltype.IndexClassMetadataCount())
	for i := range sqltype.IndexClassMetadataCount() {
		f, ok := sqltype.IndexClassMetadataAt(i)
		if !ok {
			panic("missing builtin operator class seed")
		}
		names[ids.OID(f.OID)] = f.Name
	}
	return names
}()

func OperatorClassSeedName(oid ids.OID) (string, bool) {
	name, ok := builtinClassNamesByOID[oid]
	return name, ok
}
func OperatorClassSeedModified(d *OperatorClass) bool {
	name, ok := OperatorClassSeedName(d.Address().ObjectID)
	return ok && (d.Name() != name || d.Namespace().ObjectID != ids.PGCatalogNamespaceOID || d.Owner.ID() != 10)
}
func RemapOperatorClass(d *OperatorClass, mapping map[ids.ObjectAddress]ids.ObjectAddress) *OperatorClass {
	move := func(a ids.ObjectAddress) ids.ObjectAddress {
		if n, ok := mapping[a]; ok {
			return n
		}
		return a
	}
	n := *d
	n.ObjectAddress = move(d.Address())
	n.ObjectNamespace = move(d.Namespace())
	n.Extension = move(d.Extension)
	n.Method = move(ids.ObjectAddress{ClassID: 2601, ObjectID: d.Method}).ObjectID
	n.Family = move(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: d.Family}).ObjectID
	n.InputType = move(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: d.InputType}).ObjectID
	if d.StorageType != 0 {
		n.StorageType = move(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: d.StorageType}).ObjectID
	}
	if n == *d {
		return d
	}
	return &n
}

// Bootstrap-only member ownership; live admission uses FamilyMemberInternalOwner.
func builtinMemberInternalOwner(method, family, left, right ids.OID, number int16, function bool) ids.ObjectAddress {
	if method == 783 {
		if !function || number != 1 && number != 2 && number != 5 && number != 6 && number != 7 {
			return ids.ObjectAddress{}
		}
	} else if method == 4000 {
		if !function || number < 1 || number > 5 {
			return ids.ObjectAddress{}
		}
	} else if method == 3580 {
		if left != right || function && (number < 1 || number > 4) {
			return ids.ObjectAddress{}
		}
	} else if left != right || method != 403 && method != 405 || function && number != 1 {
		return ids.ObjectAddress{}
	}
	for i := range sqltype.IndexClassMetadataCount() {
		f, ok := sqltype.IndexClassMetadataAt(i)
		if ok && ids.OID(f.Method) == method && ids.OID(f.Family) == family && ids.OID(f.Input) == left {
			return ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: ids.OID(f.OID)}
		}
	}
	if method == 783 || method == 4000 {
		return ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: family}
	}
	return ids.ObjectAddress{}
}
