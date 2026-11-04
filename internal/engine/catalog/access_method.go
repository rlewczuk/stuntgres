package catalog

import (
	"fmt"
	"iter"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// AccessMethod is a database-global immutable SQL identity. Native selects an
// existing operation authority; it is never a PostgreSQL handler return datum.
type AccessMethod struct {
	ObjectAddress  ids.ObjectAddress
	Extension      ids.ObjectAddress
	Origin         *InstalledOrigin
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
	BrinAccessMethod
	SpgistAccessMethod
	BloomAccessMethod
	GinAccessMethod
)

func (n NativeAccessMethod) MethodKind() string {
	if n == HeapAccessMethod {
		return "t"
	}
	if n >= BtreeAccessMethod && n <= GinAccessMethod {
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
	case BrinAccessMethod:
		return BrinMethod, true
	case SpgistAccessMethod:
		return SpgistMethod, true
	case BloomAccessMethod:
		return BloomMethod, true
	case GinAccessMethod:
		return GinMethod, true
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
	{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: 2742}, ObjectRevision: 1, ObjectName: "gin", Handler: 333, Native: GinAccessMethod},
	{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: 3580}, ObjectRevision: 1, ObjectName: "brin", Handler: 335, Native: BrinAccessMethod},
	{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: 4000}, ObjectRevision: 1, ObjectName: "spgist", Handler: 334, Native: SpgistAccessMethod},
}

func InstallBuiltinAccessMethods(s *State) {
	for i := range builtinAccessMethods {
		d := &builtinAccessMethods[i]
		s.SetDescriptor(d.Address(), d)
		s.bindSeedName(d)
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
	case BrinMethod:
		return 3580
	case SpgistMethod:
		return 4000
	case GinMethod:
		return 2742
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

// ValidateNativeAccessMethodProcedure authenticates an original installed
// fmgrtab handler capability. Names and owners are mutable presentation, while
// the original address and registered handler association cannot be borrowed by
// a newly installed alias.
func ValidateNativeAccessMethodProcedure(d *Procedure) error {
	if !OriginalNativeAccessMethodProcedure(d) {
		return fmt.Errorf("stuntgres: invalid native access method procedure")
	}
	identity, _ := BuiltinRoutineIdentityByOID(d.Address().ObjectID)
	if !supportedInternalProcedureMetadata(d, identity.Name, expr.Volatile) {
		return fmt.Errorf("stuntgres: invalid native access method procedure")
	}
	return nil
}

// OriginalNativeAccessMethodProcedure is the canonical intrinsic association
// shared by current descriptor validation and fmgr applicability. SQL metadata
// edits cannot manufacture or revoke the original generated fmgr identity.
func OriginalNativeAccessMethodProcedure(d *Procedure) bool {
	if d == nil || d.AccessMethod == NoAccessMethod || d.Address().ClassID != ids.ProcedureClassID || d.Address().SubID != 0 || d.Revision() == 0 {
		return false
	}
	identity, known := BuiltinRoutineIdentityByOID(d.Address().ObjectID)
	result, _ := BuiltinRoutineResultByOID(d.Address().ObjectID)
	return known && NativeHandler(d.Address().ObjectID) == d.AccessMethod &&
		d.BrinSupport == 0 && d.GinSupport == 0 &&
		originalInternalProcedure(d, d.Address().ObjectID, identity.Signature, result)
}

// originalInternalProcedure is the common fixed fmgr ABI and exclusive
// implementation proof for the special AM, BRIN and GIN declarations. Current
// SQL metadata is checked separately by their providers, not used as origin.
func originalInternalProcedure(d *Procedure, oid ids.OID, signature ProcedureSignature, result ids.OID) bool {
	return d.Address() == (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}) &&
		d.Revision() != 0 && d.Signature == signature && d.Result == result && d.RoutineKind == FunctionRoutine &&
		d.Builtin == nil && d.Installed == (InstalledRoutineDeclaration{}) && d.SQL == nil && d.SQLBody == nil && d.SQLDefaults == nil && d.Aggregate == nil && d.Foreign == nil && d.RangeConstructor == nil &&
		d.Origin == nil && d.PLHandler == NoPLHandler && d.Implementation == nil && d.Host == nil && d.HostHandler == nil && d.HostConversion == nil && d.Sepgsql == nil &&
		d.Conversion.Name() == "" && d.Dictionary == (ids.ObjectAddress{}) && d.SnowballHelper == NoSnowballHelper && len(d.Body) == 0 && len(d.DeclarationDependencies) == 0 &&
		d.DeclarationBody == nil && d.LiteralDefaults == nil && d.InputNames == nil && d.Transforms == nil
}

// These non-set native providers support rename, namespace/owner/ACL COW and
// Parallel/Cost overrides. They do not implement replacement INTERNAL sources,
// strictness/volatility changes or SQL execution environments.
func supportedInternalProcedureMetadata(d *Procedure, source string, volatility expr.Volatility) bool {
	return d.Strict && d.Volatility == volatility && !d.SecurityDefiner && d.Configuration == nil &&
		(d.Parallel == 0 || d.Parallel == 's' || d.Parallel == 'r' || d.Parallel == 'u') &&
		d.Cost >= 0 && d.Rows == 0 && d.Language == (ids.ObjectAddress{}) &&
		d.Display == (RoutineDisplay{Language: InternalRoutineLanguage, Source: source})
}

type AccessMethodProvider struct{}

func (AccessMethodProvider) ClassID() ids.OID { return ids.AccessMethodClassID }
func (AccessMethodProvider) Validate(v Read, d Descriptor) error {
	m, ok := d.(*AccessMethod)
	if !ok || m.Address().ClassID != ids.AccessMethodClassID || m.Address().ObjectID == 0 || m.Address().SubID != 0 || m.Revision() == 0 || m.Name() == "" || m.Native.MethodKind() == "" || v.NameAddress(DescriptorNameKey(m)) != m.Address() {
		return fmt.Errorf("stuntgres: invalid access method descriptor")
	}
	if err := ValidateExtensionMembership(v, m); err != nil {
		return err
	}
	p, ok := v.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: m.Handler})
	if !ok || p.AccessMethod != m.Native || NativeHandler(m.Handler) != m.Native && !(m.Native == BloomAccessMethod && p.Origin.Valid(p.Address())) {
		return fmt.Errorf("stuntgres: invalid access method handler capability")
	}
	if v.Owner() == nil || v.Owner().Provider(ids.ProcedureClassID) == nil {
		return fmt.Errorf("stuntgres: missing native handler provider")
	}
	if err := v.Owner().Provider(ids.ProcedureClassID).Validate(v, p); err != nil {
		return err
	}
	if err := ValidateInstalledClassOrigin(v, m); err != nil {
		return err
	}
	return nil
}
func AccessMethodDependencies(m *AccessMethod) []FamilyDependency {
	return []FamilyDependency{{Address: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: m.Handler}, Kind: 'n'}}
}
