package catalog

import (
	"fmt"
	"stuntgres/internal/engine/host"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Directional procedures and pairs have real catalog identities. Neither carries
// an extension Origin nor belongs to the released hstore member cohort.
type HostConversion struct {
	Token   *host.Conversion
	Type    *sqltype.Descriptor
	Handler ids.ObjectAddress
	From    bool
}
type Transform struct {
	ObjectAddress            ids.ObjectAddress
	ObjectRevision           ids.DefinitionRevision
	Type, Language, From, To ids.ObjectAddress
	SQLType                  *sqltype.Descriptor
	Runtime                  *host.Runtime
	Conversion               *host.Conversion
}

func (d *Transform) Address() ids.ObjectAddress       { return d.ObjectAddress }
func (d *Transform) Revision() ids.DefinitionRevision { return d.ObjectRevision }
func (*Transform) Namespace() ids.ObjectAddress       { return ids.ObjectAddress{} }
func (d *Transform) Name() string                     { return fmt.Sprintf("%d,%d", d.Type.ObjectID, d.Language.ObjectID) }
func (*Transform) Kind() ids.ObjectKind               { return ids.TransformObject }
func (d *Transform) Dependencies(visit func(ids.ObjectAddress) bool) bool {
	for _, a := range []ids.ObjectAddress{d.Type, d.Language, d.From, d.To} {
		if a.ObjectID != 0 && !visit(a) {
			return false
		}
	}
	return true
}

type TransformProvider struct{}

func (TransformProvider) ClassID() ids.OID { return ids.TransformClassID }
func (TransformProvider) Validate(v Read, descriptor Descriptor) error {
	d, ok := descriptor.(*Transform)
	invalid := func() error { return fmt.Errorf("stuntgres: invalid host conversion pair authority") }
	if !ok || d == nil || d.Address().ClassID != ids.TransformClassID || d.Address().ObjectID == 0 || d.Revision() == 0 || v.Get(d.Address()) != d || v.NameAddress(DescriptorNameKey(d)) != d.Address() || !HostRuntimeInstalled(v, d.Runtime) || d.Conversion == nil || d.Conversion.Runtime() != d.Runtime || !d.Conversion.Definition().Bootstrap || d.From.ObjectID == 0 && d.To.ObjectID == 0 {
		return invalid()
	}
	if d.Address().SubID != 0 || d.From.ObjectID == 0 && d.From != (ids.ObjectAddress{}) || d.To.ObjectID == 0 && d.To != (ids.ObjectAddress{}) || d.Runtime.Conversion(d.Conversion.Definition().ID) != d.Conversion {
		return invalid()
	}
	t, ok := v.Type(d.Type)
	if !ok || t.SQLType != d.SQLType || HostLogicalType(t.SQLType) != d.Conversion.Definition().Type {
		return invalid()
	}
	language, ok := v.Get(d.Language).(*Language)
	if !ok || language.Host != d.Runtime || (LanguageProvider{}).Validate(v, language) != nil {
		return invalid()
	}
	for _, direction := range []struct {
		address ids.ObjectAddress
		from    bool
	}{{d.From, true}, {d.To, false}} {
		if direction.address.ObjectID == 0 {
			continue
		}
		p, ok := v.Procedure(direction.address)
		if !ok || p.HostConversion == nil || p.HostConversion.Token != d.Conversion || p.HostConversion.Type != d.SQLType || p.HostConversion.From != direction.from || ValidateHostConversion(v, p) != nil {
			return invalid()
		}
	}
	return nil
}
func ValidateHostConversion(v Read, d *Procedure) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid host directional procedure authority") }
	if d == nil || d.HostConversion == nil || d.HostConversion.Type == nil {
		return invalid()
	}
	if d.Display.Language != GoRoutineLanguage {
		return invalid()
	}
	if d.Address().ClassID != ids.ProcedureClassID || d.Address().ObjectID == 0 || d.Address().SubID != 0 || d.Revision() == 0 || v.Get(d.Address()) != d || v.NameAddress(DescriptorNameKey(d)) != d.Address() {
		return invalid()
	}
	if d.BrinSupport != 0 || d.SnowballHelper != NoSnowballHelper || d.InputNames != nil || d.LiteralDefaults != nil || d.DeclarationBody != nil || len(d.DeclarationDependencies) != 0 {
		return invalid()
	}
	if d.Dictionary != (ids.ObjectAddress{}) {
		return invalid()
	}
	c := d.HostConversion
	if c == nil || c.Token == nil || !HostRuntimeInstalled(v, c.Token.Runtime()) || c.Token.Runtime().Conversion(c.Token.Definition().ID) != c.Token || d.Host != nil || d.HostHandler != nil || d.SQL != nil || d.Builtin != nil || d.PLHandler != NoPLHandler || d.Foreign != nil || d.Origin != nil || d.Implementation != nil || d.Aggregate != nil || d.RangeConstructor != nil || d.AccessMethod != NoAccessMethod || d.Conversion.Name() != "" || len(d.Body) != 0 || d.SQLBody != nil || d.SQLDefaults != nil || d.Language != (ids.ObjectAddress{}) || d.SecurityDefiner || d.Configuration != nil || d.RoutineKind != FunctionRoutine || d.Signature != "2281" {
		return invalid()
	}
	t, ok := v.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(c.Type.ID())})
	if !ok || t.SQLType != c.Type || HostLogicalType(c.Type) != c.Token.Definition().Type {
		return invalid()
	}
	handler, ok := v.Procedure(c.Handler)
	if !ok || handler.HostHandler != c.Token.Runtime() || ValidateHostProcedure(v, handler) != nil {
		return invalid()
	}
	def := c.Token.Definition()
	if c.From {
		if def.FromSQL == nil || d.Result != 2281 {
			return invalid()
		}
	} else if def.ToSQL == nil || d.Result != ids.OID(c.Type.ID()) {
		return invalid()
	}
	return nil
}
