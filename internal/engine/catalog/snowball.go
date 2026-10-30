package catalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// SnowballHelper is bootstrap C helper authority, not a callable scalar ABI.
type SnowballHelper uint8

const (
	NoSnowballHelper SnowballHelper = iota
	SnowballInit
	SnowballLexize
)

func (h SnowballHelper) Declaration() (string, ProcedureSignature) {
	switch h {
	case SnowballInit:
		return "dsnowball_init", "2281"
	case SnowballLexize:
		return "dsnowball_lexize", "2281,2281,2281,2281"
	}
	return "", ""
}

func (d *TSTemplate) Snowball() bool { return d != nil && d.snowball }

// snowballSeed is finite bootstrap creation identity, never inferred from labels
// or configured provider options.
type snowballSeed uint8

const (
	noSnowballSeed snowballSeed = iota
	englishSnowballSeed
	arabicSnowballSeed
	armenianSnowballSeed
	basqueSnowballSeed
)

func (s snowballSeed) language() string {
	switch s {
	case englishSnowballSeed:
		return "english"
	case arabicSnowballSeed:
		return "arabic"
	case armenianSnowballSeed:
		return "armenian"
	case basqueSnowballSeed:
		return "basque"
	}
	return ""
}

// SeedName is creation identity, independent of a seed's mutable public label.
func (d *TSDictionary) SeedName() string {
	if name := d.seed.language(); name != "" {
		return name + "_stem"
	}
	if d.Seeded() {
		return "simple"
	}
	return ""
}

func (d *TSConfiguration) SeedName() string {
	if name := d.seed.language(); name != "" {
		return name
	}
	if d.Seeded() {
		return "simple"
	}
	return ""
}

func (d *TSDictionary) SeedOptions() []DictionaryOption {
	if name := d.seed.language(); name != "" {
		options := []DictionaryOption{{Name: "language", Value: name, Supplied: true}}
		if d.seed == englishSnowballSeed {
			options = append(options, DictionaryOption{Name: "stopwords", Value: "english", Supplied: true})
		}
		return options
	}
	return nil
}

func ValidateSnowballHelper(v Read, d *Procedure) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid Snowball helper authority") }
	if d == nil || v.Owner() == nil || v.Owner().Principals == nil || d.Owner.ID() == 0 || d.Owner != v.Owner().Principals.Principal(d.Owner.ID()) {
		return invalid()
	}
	name, signature := d.SnowballHelper.Declaration()
	if name == "" || v.Get(d.Address()) != d || d.Address().ClassID != ids.ProcedureClassID || d.Address().SubID != 0 || d.Revision() == 0 || v.NameAddress(DescriptorNameKey(d)) != d.Address() {
		return invalid()
	}
	if d.Signature != signature || d.Result != 2281 || d.RoutineKind != FunctionRoutine || !d.Strict || d.Volatility != expr.Volatile || d.Installed.Parallel != 'u' {
		return invalid()
	}
	if d.Display.Language != CRoutineLanguage || d.Display.Source != name || !d.Display.HasBinary || d.Display.Binary != "$libdir/dict_snowball" {
		return invalid()
	}
	if d.Builtin != nil || d.SQL != nil || d.SQLBody != nil || d.SQLDefaults != nil || d.Foreign != nil || d.Implementation != nil || d.Aggregate != nil || d.AccessMethod != NoAccessMethod || d.PLHandler != NoPLHandler || d.BrinSupport != 0 || d.RangeConstructor != nil || d.Conversion.Name() != "" || len(d.Body) != 0 || len(d.DeclarationDependencies) != 0 || d.Dictionary != (ids.ObjectAddress{}) || d.Language != (ids.ObjectAddress{}) {
		return invalid()
	}
	return nil
}

func ValidateSnowballTemplate(v Read, d *TSTemplate) error {
	if d == nil || !d.snowball || d.Implementation != nil {
		return fmt.Errorf("stuntgres: invalid Snowball template authority")
	}
	for _, helper := range []struct {
		address ids.ObjectAddress
		kind    SnowballHelper
	}{{d.Init, SnowballInit}, {d.Lexize, SnowballLexize}} {
		p, ok := v.Procedure(helper.address)
		if !ok || p.SnowballHelper != helper.kind {
			return fmt.Errorf("stuntgres: invalid Snowball template helper")
		}
		if err := ValidateSnowballHelper(v, p); err != nil {
			return err
		}
	}
	return nil
}

// InstallSnowballLocked runs only inside the private bulk seed. The caller
// supplies compiled execution state to keep catalog independent of functions.
func (s *Store) InstallSnowballLocked(owner authorization.PrincipalRef, englishRuntime, arabicRuntime, armenianRuntime, basqueRuntime DictionaryRuntime) error {
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	allocate := func(class ids.OID) (ids.ObjectAddress, error) {
		oid, err := s.AllocateOIDLocked()
		return ids.ObjectAddress{ClassID: class, ObjectID: oid}, err
	}
	template := &TSTemplate{ObjectNamespace: ns, ObjectRevision: 1, ObjectName: "snowball", snowball: true}
	for _, kind := range []SnowballHelper{SnowballInit, SnowballLexize} {
		address, err := allocate(ids.ProcedureClassID)
		if err != nil {
			return err
		}
		name, signature := kind.Declaration()
		helper := &Procedure{
			ObjectAddress: address, ObjectNamespace: ns, ObjectRevision: 1, ObjectName: name,
			Signature: signature, Result: 2281, Owner: owner, RoutineKind: FunctionRoutine,
			Volatility: expr.Volatile, Strict: true, SnowballHelper: kind,
			Display: RoutineDisplay{Language: CRoutineLanguage, Source: name, Binary: "$libdir/dict_snowball", HasBinary: true},
		}
		helper.Installed.Parallel = 'u'
		installSeedDescriptor(s.State, helper)
		if kind == SnowballInit {
			template.Init = address
		} else {
			template.Lexize = address
		}
	}
	var err error
	template.ObjectAddress, err = allocate(ids.TSTemplateClassID)
	if err != nil {
		return err
	}
	installSeedDescriptor(s.State, template)
	for _, entry := range []struct {
		seed    snowballSeed
		runtime DictionaryRuntime
	}{{englishSnowballSeed, englishRuntime}, {arabicSnowballSeed, arabicRuntime}, {armenianSnowballSeed, armenianRuntime}, {basqueSnowballSeed, basqueRuntime}} {
		address, err := allocate(ids.TSDictionaryClassID)
		if err != nil {
			return err
		}
		name := entry.seed.language()
		dictionary := &TSDictionary{ObjectAddress: address, ObjectNamespace: ns, ObjectRevision: 1, ObjectName: name + "_stem", Owner: owner, Template: template.Address(), Runtime: entry.runtime, seed: entry.seed}
		dictionary.Options = dictionary.SeedOptions()
		installSeedDescriptor(s.State, dictionary)
		address, err = allocate(ids.TSConfigurationClassID)
		if err != nil {
			return err
		}
		configuration := &TSConfiguration{ObjectAddress: address, ObjectNamespace: ns, ObjectRevision: 1, ObjectName: name, Owner: owner, Parser: ids.ObjectAddress{ClassID: 3601, ObjectID: 3722}, seed: entry.seed}
		configuration.Mappings = make([]TSMapping, 0, 19)
		for _, token := range []int32{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 15, 16, 17, 18, 19, 20, 21, 22} {
			dict := ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 3765}
			switch token {
			case 1, 2, 10, 11, 16, 17:
				dict = dictionary.Address()
			}
			configuration.Mappings = append(configuration.Mappings, TSMapping{TokenID: token, Dictionaries: []ids.ObjectAddress{dict}})
		}
		installSeedDescriptor(s.State, configuration)
	}
	return nil
}
