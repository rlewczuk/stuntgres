package catalog

import (
	"fmt"
	"sort"
	"strconv"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// OperatorClass is immutable catalog identity. StorageType zero means InputType.
type OperatorClass struct {
	ObjectAddress, ObjectNamespace         ids.ObjectAddress
	Extension                              ids.ObjectAddress
	ObjectRevision                         ids.DefinitionRevision
	ObjectName                             string
	Owner                                  authorization.PrincipalRef
	Method, Family, InputType, StorageType ids.OID
	Default                                bool
}

func (d *OperatorClass) Address() ids.ObjectAddress       { return d.ObjectAddress }
func (d *OperatorClass) Namespace() ids.ObjectAddress     { return d.ObjectNamespace }
func (d *OperatorClass) Revision() ids.DefinitionRevision { return d.ObjectRevision }
func (d *OperatorClass) Name() string                     { return d.ObjectName }
func (*OperatorClass) Kind() ids.ObjectKind               { return ids.OperatorClassObject }
func ClassNameKey(namespace ids.ObjectAddress, method ids.OID, name string) ids.NameKey {
	return ids.NameKey{ClassID: ids.OperatorClassCatalogID, Namespace: namespace, Name: name, Signature: strconv.FormatUint(uint64(method), 10)}
}

// Default keys occupy a namespace-free secondary name slot; SQL classes always
// have a real namespace. They participate in the ordinary publication fences.
func classDefaultKey(method, input ids.OID) ids.NameKey {
	return ClassNameKey(ids.ObjectAddress{}, method, strconv.FormatUint(uint64(input), 10))
}
func descriptorDefaultKey(d Descriptor) (ids.NameKey, bool) {
	c, ok := d.(*OperatorClass)
	if !ok || !c.Default {
		return ids.NameKey{}, false
	}
	return classDefaultKey(c.Method, c.InputType), true
}
func (r Read) OperatorClass(oid ids.OID) (*OperatorClass, bool) {
	return descriptorAs[*OperatorClass](r.state, ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: oid})
}
func (r Read) OperatorClasses() []*OperatorClass {
	var classes []*OperatorClass
	for _, d := range r.Descriptors() {
		if c, ok := d.(*OperatorClass); ok {
			classes = append(classes, c)
		}
	}
	sort.Slice(classes, func(i, j int) bool { return classes[i].Address().ObjectID < classes[j].Address().ObjectID })
	return classes
}
func (r Read) LookupOperatorClass(namespace ids.ObjectAddress, method ids.OID, name string) (*OperatorClass, bool) {
	return r.OperatorClass(r.NameAddress(ClassNameKey(namespace, method, name)).ObjectID)
}
func (r Read) DefaultOperatorClass(method, input ids.OID) (*OperatorClass, bool) {
	return r.OperatorClass(r.NameAddress(classDefaultKey(method, input)).ObjectID)
}
func (r Read) IndexClass(oid ids.OID) (sqltype.IndexClass, bool) {
	d, ok := r.OperatorClass(oid)
	if !ok {
		return sqltype.IndexClass{}, false
	}
	method, ok := r.AccessMethod(d.Method)
	if !ok {
		return sqltype.IndexClass{}, false
	}
	return r.IndexClassForMethod(oid, method)
}

// IndexClassForMethod constructs a class from the captured method descriptor.
func (r Read) IndexClassForMethod(oid ids.OID, method *AccessMethod) (sqltype.IndexClass, bool) {
	if method == nil {
		return sqltype.IndexClass{}, false
	}
	if owned, ok := r.AccessMethod(method.Address().ObjectID); !ok || owned != method {
		return sqltype.IndexClass{}, false
	}
	d, ok := r.OperatorClass(oid)
	if !ok {
		return sqltype.IndexClass{}, false
	}
	f, ok := r.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: d.Family})
	if !ok || f.Method != d.Method {
		return sqltype.IndexClass{}, false
	}
	if method.Address().ObjectID != d.Method || method.MethodKind() != "i" {
		return sqltype.IndexClass{}, false
	}
	physical, _ := method.IndexImplementation()
	return sqltype.NewIndexClass(sqltype.IndexClassDefinition{OID: uint32(oid), Method: uint32(d.Method), Family: uint32(d.Family), Input: uint32(d.InputType), Storage: uint32(d.StorageType), Name: d.Name(), FamilyName: f.Name(), Hash: physical == HashMethod, GiST: physical == GistMethod}), true
}
func (r Read) DefaultIndexClass(method ids.OID, t *sqltype.Descriptor) (sqltype.IndexClass, bool) {
	if t == nil || !t.Concrete() {
		return sqltype.IndexClass{}, false
	}
	t = t.BaseType()
	if d, ok := r.DefaultOperatorClass(method, ids.OID(t.ID())); ok {
		return r.IndexClass(d.Address().ObjectID)
	}
	if t.IsEnum() {
		return r.DefaultIndexClassID(method, 3500)
	}
	return r.DefaultIndexClassID(method, t.ID())
}
func (r Read) DefaultIndexClassID(method ids.OID, input sqltype.ID) (sqltype.IndexClass, bool) {
	// Exact defaults win. Binary-compatible alternatives preserve existing
	// directional provider adaptation without consulting bootstrap class names.
	if d, ok := r.DefaultOperatorClass(method, ids.OID(input)); ok {
		return r.IndexClass(d.Address().ObjectID)
	}
	var adapted sqltype.ID
	switch input {
	case sqltype.VarChar, sqltype.PgNodeTree:
		adapted = sqltype.Text
	case sqltype.Cidr:
		adapted = sqltype.Inet
	case sqltype.Regclass, sqltype.Regtype, sqltype.Regproc, sqltype.Regprocedure, sqltype.Regrole, sqltype.Regoper, sqltype.Regoperator, sqltype.Regcollation, sqltype.Regnamespace, sqltype.Regconfig, sqltype.Regdictionary:
		adapted = sqltype.Oid
	}
	if adapted != 0 {
		if d, ok := r.DefaultOperatorClass(method, ids.OID(adapted)); ok {
			return r.IndexClass(d.Address().ObjectID)
		}
	}
	if m, ok := r.AccessMethod(method); ok && m.Native == GistAccessMethod {
		if d, ok := r.DefaultOperatorClass(method, 3831); ok {
			if c, ok := r.IndexClass(d.Address().ObjectID); ok && c.AcceptsID(input) {
				return c, true
			}
		}
	}
	return sqltype.IndexClass{}, false
}

type OperatorClassProvider struct{}

func (OperatorClassProvider) ClassID() ids.OID { return ids.OperatorClassCatalogID }
func (OperatorClassProvider) Validate(v Read, d Descriptor) error {
	c, ok := d.(*OperatorClass)
	if !ok || c.Address().ClassID != ids.OperatorClassCatalogID || c.Address().ObjectID == 0 || c.Address().SubID != 0 || c.Revision() == 0 || c.Name() == "" || v.NameAddress(DescriptorNameKey(c)) != c.Address() {
		return fmt.Errorf("stuntgres: invalid operator class authority")
	}
	if err := ValidateExtensionMembership(v, c); err != nil {
		return err
	}
	if method, ok := v.AccessMethod(c.Method); !ok || method.MethodKind() != "i" {
		return fmt.Errorf("stuntgres: invalid operator class method")
	}
	if _, ok := v.Namespace(c.Namespace()); !ok {
		return fmt.Errorf("stuntgres: invalid operator class namespace")
	}
	f, ok := v.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: c.Family})
	if !ok || f.Method != c.Method || c.InputType == 0 {
		return fmt.Errorf("stuntgres: invalid operator class family/input")
	}
	if c.Default {
		if selected, ok := v.DefaultOperatorClass(c.Method, c.InputType); !ok || selected.Address() != c.Address() {
			return Conflict{}
		}
	}
	return nil
}
