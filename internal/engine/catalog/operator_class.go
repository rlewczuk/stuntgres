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
	if r.defaultClassCount(method, input) != 1 {
		return nil, false
	}
	return r.AnyDefaultOperatorClass(method, input)
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
	return sqltype.NewIndexClass(sqltype.IndexClassDefinition{OID: uint32(oid), Method: uint32(d.Method), Family: uint32(d.Family), Input: uint32(d.InputType), Storage: uint32(d.StorageType), Name: d.Name(), FamilyName: f.Name(), Hash: physical == HashMethod, GiST: physical == GistMethod, Brin: physical == BrinMethod, SPGiST: physical == SpgistMethod}), true
}
func (r Read) DefaultIndexClass(method ids.OID, t *sqltype.Descriptor) (sqltype.IndexClass, bool, error) {
	if t == nil || !t.Concrete() {
		return sqltype.IndexClass{}, false, nil
	}
	t = t.BaseType()
	if r.defaultClassCount(method, ids.OID(t.ID())) != 0 {
		return r.DefaultIndexClassID(method, t.ID())
	}
	if t.IsEnum() {
		// anyenum is compatible with the concrete enum, not an exact match.
		// Multiple compatible defaults mean no selection, not corruption.
		c, ok := r.uniqueDefaultIndexClassID(method, 3500)
		return c, ok, nil
	}
	if m, ok := r.AccessMethod(method); ok && (m.Native == GistAccessMethod || m.Native == SpgistAccessMethod) {
		if t.IsRange() {
			c, ok := r.uniqueDefaultIndexClassID(method, 3831)
			return c, ok, nil
		}
		if t.IsMultirange() && m.Native == GistAccessMethod {
			c, ok := r.uniqueDefaultIndexClassID(method, 4537)
			return c, ok, nil
		}
	}
	return r.DefaultIndexClassID(method, t.ID())
}
func (r Read) DefaultIndexClassID(method ids.OID, input sqltype.ID) (sqltype.IndexClass, bool, error) {
	if r.defaultClassCount(method, ids.OID(input)) > 1 {
		return sqltype.IndexClass{}, false, r.defaultClassAmbiguity(input)
	}
	c, ok := r.uniqueDefaultIndexClassID(method, input)
	return c, ok, nil
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
		bucket, _ := v.state.defaultClasses.Get(defaultClassPair{c.Method, c.InputType})
		found := false
		for _, oid := range bucket {
			if oid == c.Address().ObjectID {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("stuntgres: missing default operator class descriptor index")
		}
	}
	return nil
}
