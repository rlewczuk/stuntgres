package engine

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

type extensionCatalogProvider struct{ class ids.OID }

func (p extensionCatalogProvider) ClassID() ids.OID { return p.class }
func (p extensionCatalogProvider) Validate(v catalog.Read, d catalog.Descriptor) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid extension provider descriptor") }
	a := d.Address()
	if a.ClassID != p.class || a.ObjectID == 0 || a.SubID != 0 || d.Revision() == 0 || v.NameAddress(catalog.DescriptorNameKey(d)) != a {
		return invalid()
	}
	namespace := d.Namespace()
	if namespace != (ids.ObjectAddress{}) {
		if _, ok := v.Namespace(namespace); !ok {
			return invalid()
		}
	}
	var ext ids.ObjectAddress
	switch d := d.(type) {
	case *catalog.Extension:
		implementation, registered := v.Owner().Extensions[d.Name()]
		if !registered || implementation.Versions().InstallPath(d.Version) == nil || d.Namespace() != (ids.ObjectAddress{}) {
			return invalid()
		}
		if _, ok := v.Namespace(d.TargetNamespace); !ok {
			return invalid()
		}
		seen := map[ids.ObjectAddress]bool{}
		for _, a := range d.Members {
			if seen[a] {
				return invalid()
			}
			seen[a] = true
			member := v.Get(a)
			if member == nil || member.Namespace() != d.TargetNamespace || extensionMembership(member) != d.Address() {
				return invalid()
			}
		}
		if err := implementation.ValidateExtension(v, d); err != nil {
			return err
		}
	case *catalog.Procedure:
		if d.SQL != nil {
			if !d.SQL.Result.Valid() || d.SQL.Query == nil {
				return invalid()
			}
			result, ok := v.LookupType(d.SQL.Result.Type.ID())
			if !ok || !result.Concrete() {
				return invalid()
			}
			var signature strings.Builder
			for i, input := range d.SQL.Inputs {
				if !input.Valid() {
					return invalid()
				}
				typ, ok := v.LookupType(input.Type.ID())
				if !ok || !typ.Concrete() {
					return invalid()
				}
				if i != 0 {
					signature.WriteByte(',')
				}
				signature.WriteString(strconv.FormatUint(uint64(typ.ID()), 10))
			}
			if string(d.Signature) != signature.String() {
				return invalid()
			}
			if d.Builtin != nil || d.Aggregate != nil || d.Foreign != nil || d.Implementation != nil || d.Conversion.Name() != "" || d.Extension != (ids.ObjectAddress{}) || d.Dictionary != (ids.ObjectAddress{}) || len(d.Body) != 0 || d.RoutineKind != catalog.FunctionRoutine || d.Strict || d.Volatility != expr.Volatile || ids.OID(d.SQL.Result.Type.ID()) != d.Result {
				return invalid()
			}
			break
		}
		if uuidOsspProviderOf(d) != nil && (d.Aggregate != nil || d.Foreign != nil || d.Builtin != nil) {
			return invalid()
		}
		if d.Aggregate != nil && intaggProviderOf(d) != stockIntagg {
			return invalid()
		}
		if d.Foreign != nil {
			p := d.Foreign
			if p.Name.Name != d.Name() || d.Extension != (ids.ObjectAddress{}) || d.Implementation != nil || d.Conversion.Name() != "" || d.Builtin != nil {
				return invalid()
			}
			if p.Wrapper != nil {
				if p.Validator != nil || d.Signature != catalog.ForeignHandlerSignature || d.Result != 3115 {
					return invalid()
				}
			} else if p.Validator == nil || d.Signature != catalog.ForeignValidatorSignature || d.Result != 2278 {
				return invalid()
			}
			break
		}
		if d.Builtin != nil {
			declared := d.Builtin
			if routine.BuiltinRoutine(d.Address().ObjectID) != declared || d.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) || d.Name() != declared.Identity.Name || d.Signature != declared.Identity.Signature || d.RoutineKind != declared.Kind || d.Result != declared.Result || d.Strict != declared.Strict || d.Volatility != declared.Volatility || d.Implementation != nil || d.Conversion.Name() != "" || d.Extension != (ids.ObjectAddress{}) || d.Dictionary != (ids.ObjectAddress{}) || d.Foreign != nil {
				return invalid()
			}
			break
		}
		if intaggProviderOf(d) != nil {
			if err := intaggProviderOf(d).validateProcedure(v, d); err != nil {
				return err
			}
			ext = d.Extension
			break
		}
		if d.RoutineKind != catalog.FunctionRoutine {
			return invalid()
		}
		if uuidOsspProviderOf(d) != nil {
			if d.Conversion.Name() != "" || d.Dictionary != (ids.ObjectAddress{}) {
				return invalid()
			}
			if err := uuidOsspProviderOf(d).validateProcedure(v, d); err != nil {
				return err
			}
			ext = d.Extension
			break
		}
		if fuzzystrmatchProviderOf(d) != nil {
			if err := fuzzystrmatchProviderOf(d).validateProcedure(v, d); err != nil {
				return err
			}
			ext = d.Extension
			break
		}
		if pgcryptoProviderOf(d) != nil {
			if err := pgcryptoProviderOf(d).validateProcedure(v, d); err != nil {
				return err
			}
			ext = d.Extension
			break
		}
		if intarrayProviderOf(d) != nil {
			if err := intarrayProviderOf(d).validateProcedure(v, d); err != nil {
				return err
			}
			ext = d.Extension
			break
		} else if cubeProviderOf(d) != nil {
			if err := cubeProviderOf(d).validateProcedure(v, d); err != nil {
				return err
			}
			ext = d.Extension
			break
		}
		if d.Conversion.Name() != "" {
			registered, ok := sqltype.RegisteredEncodingConversion(d.Name())
			if !ok || d.Conversion != registered || unaccentProviderOf(d) != nil || d.Signature != catalog.ConversionSignature || d.Result != 23 || d.Volatility != expr.Immutable || !d.Strict || d.Extension != (ids.ObjectAddress{}) || d.Dictionary != (ids.ObjectAddress{}) || d.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) || typedef.ConversionRoutineName(d.Address().ObjectID) != d.Name() {
				return invalid()
			}
			break
		}
		if unaccentProviderOf(d) != stockUnaccent {
			return invalid()
		}
		ext = d.Extension
		switch d.Signature {
		case catalog.UnaccentTextSignature, catalog.UnaccentDictionarySignature:
			if d.Name() != "unaccent" || d.Result != 25 || d.Volatility != expr.Stable || !d.Strict {
				return invalid()
			}
			if ext != (ids.ObjectAddress{}) {
				dict, ok := v.TSDictionary(d.Dictionary)
				if !ok || dict.Namespace() != d.Namespace() || dict.Extension != ext {
					return invalid()
				}
			}
		case catalog.UnaccentInitSignature:
			if d.Name() != "unaccent_init" || d.Result != 2281 || d.Volatility != expr.Volatile || d.Strict {
				return invalid()
			}
		case catalog.UnaccentLexizeSignature:
			if d.Name() != "unaccent_lexize" || d.Result != 2281 || d.Volatility != expr.Volatile || d.Strict {
				return invalid()
			}
		default:
			return invalid()
		}
	case *catalog.TSTemplate:
		if d.Name() != "unaccent" || unaccentTemplateProviderOf(d) != stockUnaccent {
			return invalid()
		}
		ext = d.Extension
		for i, a := range [2]ids.ObjectAddress{d.Init, d.Lexize} {
			proc, ok := v.Procedure(a)
			if !ok || ext != (ids.ObjectAddress{}) && proc.Namespace() != d.Namespace() || proc.Extension != ext || proc.Signature != [2]catalog.ProcedureSignature{catalog.UnaccentInitSignature, catalog.UnaccentLexizeSignature}[i] {
				return invalid()
			}
		}
	case *catalog.TSDictionary:
		if d.Name() != "unaccent" || unaccentDictionaryProviderOf(d) != stockUnaccent {
			return invalid()
		}
		ext = d.Extension
		tmpl, ok := v.TSTemplate(d.Template)
		if !ok || ext != (ids.ObjectAddress{}) && tmpl.Namespace() != d.Namespace() || tmpl.Extension != ext {
			return invalid()
		}
	default:
		return invalid()
	}
	if ext != (ids.ObjectAddress{}) {
		parent, ok := v.Extension(ext)
		if !ok || parent.TargetNamespace != namespace {
			return invalid()
		}
		found := false
		for _, member := range parent.Members {
			found = found || member == a
		}
		if !found {
			return invalid()
		}
	}
	return nil
}
func extensionMembership(d catalog.Descriptor) ids.ObjectAddress {
	switch d := d.(type) {
	case *catalog.Type:
		return d.Extension
	case *catalog.Procedure:
		return d.Extension
	case *catalog.TSTemplate:
		return d.Extension
	case *catalog.TSDictionary:
		return d.Extension
	}
	return ids.ObjectAddress{}
}
