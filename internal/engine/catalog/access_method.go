package catalog

import (
	"fmt"
	"iter"
	"stuntgres/internal/engine/ids"
)

// AccessMethod is a database-global immutable SQL identity. Native selects an
// existing operation authority; it is never a PostgreSQL handler return datum.
type AccessMethod struct {
	ObjectAddress  ids.ObjectAddress
	ObjectRevision ids.DefinitionRevision
	ObjectName     string
	Handler        ids.OID
	Native         NativeAccessMethod
}

func (d *AccessMethod) Address() ids.ObjectAddress       { return d.ObjectAddress }
func (d *AccessMethod) Revision() ids.DefinitionRevision { return d.ObjectRevision }
func (*AccessMethod) Namespace() ids.ObjectAddress       { return ids.ObjectAddress{} }
func (d *AccessMethod) Name() string                     { return d.ObjectName }
func (*AccessMethod) Kind() ids.ObjectKind               { return ids.AccessMethodObject }

// NativeAccessMethod is a finite association to the engine's actual heap and
// index operation authorities, not an extensible ABI or routine-name fallback.
type NativeAccessMethod uint8

const (
	NoAccessMethod NativeAccessMethod = iota
	HeapAccessMethod
	BtreeAccessMethod
	HashAccessMethod
	GistAccessMethod
)

func (n NativeAccessMethod) MethodKind() string {
	if n == HeapAccessMethod {
		return "t"
	}
	if n >= BtreeAccessMethod && n <= GistAccessMethod {
		return "i"
	}
	return ""
}
func (n NativeAccessMethod) IndexImplementation() (IndexMethod, bool) {
	switch n {
	case BtreeAccessMethod:
		return BtreeMethod, true
	case HashAccessMethod:
		return HashMethod, true
	case GistAccessMethod:
		return GistMethod, true
	}
	return 0, false
}
func (d *AccessMethod) IndexImplementation() (IndexMethod, bool) {
	return d.Native.IndexImplementation()
}
func (d *AccessMethod) MethodKind() string { return d.Native.MethodKind() }
func (r Read) AccessMethod(oid ids.OID) (*AccessMethod, bool) {
	return descriptorAs[*AccessMethod](r.state, ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: oid})
}
func (r Read) LookupAccessMethod(name string) (*AccessMethod, bool) {
	return descriptorAs[*AccessMethod](r.state, r.NameAddress(ids.NameKey{ClassID: ids.AccessMethodClassID, Name: name}))
}
func (r Read) AccessMethods() iter.Seq[*AccessMethod] {
	return func(yield func(*AccessMethod) bool) {
		for _, d := range r.Descriptors() {
			if m, ok := d.(*AccessMethod); ok && !yield(m) {
				return
			}
		}
	}
}

// Bootstrap facts are input only. Runtime lookup always uses the captured Read.
var builtinAccessMethods = [...]AccessMethod{
	{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: 2}, ObjectRevision: 1, ObjectName: "heap", Handler: 3, Native: HeapAccessMethod},
	{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: 403}, ObjectRevision: 1, ObjectName: "btree", Handler: 330, Native: BtreeAccessMethod},
	{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: 405}, ObjectRevision: 1, ObjectName: "hash", Handler: 331, Native: HashAccessMethod},
	{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: 783}, ObjectRevision: 1, ObjectName: "gist", Handler: 332, Native: GistAccessMethod},
}

func InstallBuiltinAccessMethods(s *State) {
	for i := range builtinAccessMethods {
		d := &builtinAccessMethods[i]
		s.SetDescriptor(d.Address(), d)
		s.BindName(DescriptorNameKey(d), d.Address())
	}
}
func AccessMethodSeeded(d *AccessMethod) bool {
	for i := range builtinAccessMethods {
		if d.Address() == builtinAccessMethods[i].Address() {
			return true
		}
	}
	return false
}
func (m IndexMethod) OID() ids.OID {
	switch m {
	case BtreeMethod:
		return 403
	case HashMethod:
		return 405
	case GistMethod:
		return 783
	}
	return 0
}

// NativeHandler is capability registration, distinct from lookup-only released
// signature metadata. Every accepted association is installed as a Procedure.
func NativeHandler(oid ids.OID) NativeAccessMethod {
	for i := range builtinAccessMethods {
		if builtinAccessMethods[i].Handler == oid {
			return builtinAccessMethods[i].Native
		}
	}
	return NoAccessMethod
}

type AccessMethodProvider struct{}

func (AccessMethodProvider) ClassID() ids.OID { return ids.AccessMethodClassID }
func (AccessMethodProvider) Validate(v Read, d Descriptor) error {
	m, ok := d.(*AccessMethod)
	if !ok || m.Address().ClassID != ids.AccessMethodClassID || m.Address().ObjectID == 0 || m.Address().SubID != 0 || m.Revision() == 0 || m.Name() == "" || m.Native.MethodKind() == "" || v.NameAddress(DescriptorNameKey(m)) != m.Address() {
		return fmt.Errorf("stuntgres: invalid access method descriptor")
	}
	p, ok := v.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: m.Handler})
	if !ok || p.AccessMethod != m.Native || NativeHandler(m.Handler) != m.Native {
		return fmt.Errorf("stuntgres: invalid access method handler capability")
	}
	if v.Owner() == nil || v.Owner().Provider(ids.ProcedureClassID) == nil {
		return fmt.Errorf("stuntgres: missing native handler provider")
	}
	if err := v.Owner().Provider(ids.ProcedureClassID).Validate(v, p); err != nil {
		return err
	}
	return nil
}
func AccessMethodDependencies(m *AccessMethod) []FamilyDependency {
	return []FamilyDependency{{Address: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: m.Handler}, Kind: 'n'}}
}
