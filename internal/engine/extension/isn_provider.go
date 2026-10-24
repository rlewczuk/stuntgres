package extension

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type IsnProvider struct{ control catalog.ExtensionControl }

func (p *IsnProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *IsnProvider) Versions() catalog.ExtensionVersions { return catalog.ExtensionVersions{Bases: []string{"1.3"}} }

var StockIsn = &IsnProvider{control: catalog.ExtensionControl{Version: "1.3", Trusted: true, Superuser: true, Relocatable: true, Comment: new("data types for international product numbering standards")}}

type isnRecipe struct {
	slot int
	name, input, output, inputSource string
	construct func(sqltype.ID, string, string) (*sqltype.Descriptor, error)
	accept func(*sqltype.Descriptor) bool
}

var isnRecipes = [...]isnRecipe{
	{0, "ean13", "ean13_in", "ean13_out", "ean13_in", sqltype.Ean13, (*sqltype.Descriptor).IsEan13},
	{3, "isbn", "isbn_in", "isn_out", "isbn_in", sqltype.Isbn, (*sqltype.Descriptor).IsIsbn},
	{6, "isbn13", "isbn13_in", "ean13_out", "isbn_in", sqltype.Isbn13, (*sqltype.Descriptor).IsIsbn13},
}

const isnMemberCount = len(isnRecipes)*3

func IsnSignature(id sqltype.ID) catalog.ProcedureSignature { return catalog.ProcedureSignature(strconv.FormatUint(uint64(id), 10)) }

func (p *IsnProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid isn manifest") }
	if p != StockIsn || ext.Name() != "isn" || ext.Version != p.control.Version || len(ext.Members) != isnMemberCount { return invalid() }
	for i, address := range ext.Members {
		for _, earlier := range ext.Members[:i] { if address == earlier { return invalid() } }
	}
	for _, recipe := range isnRecipes {
		if ext.Members[recipe.slot].ClassID != ids.TypeClassID { return invalid() }
		base, ok := v.Type(ext.Members[recipe.slot])
		if !ok || base.SQLType == nil || base.Address() != ext.Members[recipe.slot] || !recipe.accept(base.SQLType) || base.Extension != ext.Address() || base.ObjectRevision == 0 || base.Array == 0 { return invalid() }
		if err := ValidateProviderHeader(v, base, ids.TypeClassID); err != nil { return err }
		array, ok := v.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)})
		if !ok || array.SQLType == nil || !array.SQLType.IsArray() || array.SQLType.Concrete() || array.SQLType.Element() != base.SQLType || array.Namespace() != base.Namespace() || array.Owner != base.Owner || array.Extension != (ids.ObjectAddress{}) { return invalid() }
		if err := validateRegisteredType(v, array); err != nil { return err }
		for i := range 2 {
			address := ext.Members[recipe.slot+1+i]
			proc, ok := v.Procedure(address)
			if !ok || address.ClassID != ids.ProcedureClassID || proc.Address() != address || IsnProviderOf(proc) != p || proc.Extension != ext.Address() || proc.Namespace() == (ids.ObjectAddress{}) || proc.Revision() == 0 { return invalid() }
			signature, result := catalog.ProcedureSignature("2275"), ids.OID(base.SQLType.ID())
			if i == 1 { signature, result = IsnSignature(base.SQLType.ID()), 2275 }
			source := recipe.inputSource
			if i == 1 { source = recipe.output }
			if proc.Display.Source != source { return invalid() }
			if proc.Owner == (authorization.PrincipalRef{}) || v.NameAddress(catalog.DescriptorNameKey(proc)) != address { return invalid() }
			if proc.RoutineKind != catalog.FunctionRoutine || proc.Aggregate != nil || proc.Foreign != nil || proc.Builtin != nil || proc.SQL != nil || proc.SQLBody != nil || proc.SQLDefaults != nil || proc.Language != (ids.ObjectAddress{}) || proc.RangeConstructor != nil || proc.PLHandler != catalog.NoPLHandler || proc.AccessMethod != catalog.NoAccessMethod || len(proc.Body) != 0 || len(proc.DeclarationDependencies) != 0 || proc.Display.SecurityDefiner { return invalid() }
			if proc.Signature != signature || proc.Result != result || !proc.Strict || proc.Volatility != expr.Immutable || proc.Conversion.Name() != "" || proc.Dictionary != (ids.ObjectAddress{}) || proc.Display.Language != catalog.CRoutineLanguage { return invalid() }
		}
	}
	return nil
}

// IsnCodecType resolves immutable member slots, never mutable procedure names.
func IsnCodecType(v catalog.Read, proc *catalog.Procedure) (*catalog.Type, bool, error) {
	if proc == nil || v.Get(proc.Address()) != proc { return nil, false, fmt.Errorf("stuntgres: foreign isn codec") }
	ext, ok := v.Extension(proc.Extension)
	if !ok { return nil, false, fmt.Errorf("stuntgres: missing isn installation") }
	if err := StockIsn.ValidateExtension(v, ext); err != nil { return nil, false, err }
	for _, recipe := range isnRecipes {
		for i := range 2 { if proc.Address() == ext.Members[recipe.slot+1+i] { return v.MustType(ext.Members[recipe.slot]), i == 0, nil } }
	}
	return nil, false, fmt.Errorf("stuntgres: foreign isn codec member")
}

func (p *IsnProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	if p != StockIsn { return fmt.Errorf("stuntgres: foreign isn provider") }
	_, _, err := IsnCodecType(v, proc)
	return err
}
