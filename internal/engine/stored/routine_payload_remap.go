package stored

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

// NewRoutineRemap shares mappings by original immutable payload identity, not
// routine address. Declarations are stabilized before any body is rebound.
func NewRoutineRemap(view catalog.Read, mapping map[ids.ObjectAddress]ids.ObjectAddress, typ func(*sqltype.Descriptor) *sqltype.Descriptor, principal catalog.ProcedureRemapper, typesOnly bool) *catalog.RoutineRemap {
	collation := typedef.RemappedCollationCapabilities(view.State(), mapping)
	if typ == nil {
		typ = func(old *sqltype.Descriptor) *sqltype.Descriptor {
			if old == nil {
				return nil
			}
			address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.ID())}
			if target, ok := mapping[address]; ok {
				address = target
			}
			current, _ := view.LookupType(sqltype.ID(address.ObjectID))
			return current
		}
	}
	// Occurrence identity belongs to the entire operation, not to an individual
	// declaration, expression, binding or frozen presentation.
	r := &catalog.RoutineRemap{Type: analyze.RecordTypeMapper(typ, collation), Collation: collation, Descriptor: extension.DescriptorRemapper(mapping)}
	definitions := make(map[*proc.Definition]*proc.Definition)
	var programs map[*proc.PLProgram]*proc.PLProgram
	usages := &ViewQueryRemapper{Type: r.Type, Collation: r.Collation}
	bodies := make(map[*RoutineProgram]catalog.RoutineProgram)
	defaults := make(map[*RoutineDefaults]catalog.RoutineDefaults)
	var constructors map[*catalog.RangeConstructor]*catalog.RangeConstructor
	hostDeclarations := make(map[*catalog.HostRoutine]*catalog.HostRoutine)
	hostConversions := make(map[*catalog.HostConversion]*catalog.HostConversion)
	bodyUsers := make(map[*RoutineProgram][]*catalog.Procedure)
	defaultUsers := make(map[*RoutineDefaults][]*catalog.Procedure)
	r.Definition = func(original *proc.Definition) (*proc.Definition, error) {
		if original == nil {
			return nil, nil
		}
		if mapped := definitions[original]; mapped != nil {
			return mapped, nil
		}
		if original.PL != nil && programs == nil {
			programs = make(map[*proc.PLProgram]*proc.PLProgram)
		}
		mapped, err := reownRoutineDefinition(original, usages.usage, programs)
		if err == nil {
			definitions[original] = mapped
		}
		return mapped, err
	}
	r.Body = func(original catalog.RoutineProgram) (catalog.RoutineProgram, error) {
		p, ok := original.(*RoutineProgram)
		if !ok || p == nil {
			return nil, fmt.Errorf("stuntgres: invalid SQL routine body payload")
		}
		if mapped := bodies[p]; mapped != nil {
			return mapped, nil
		}
		mapped, err := p.Rebind(view, mapping, r)
		if err != nil {
			return nil, err
		}
		bodies[p] = mapped
		for _, user := range bodyUsers[p] {
			user.SQLBody = mapped
		}
		return mapped, nil
	}
	r.Defaults = func(original catalog.RoutineDefaults) (catalog.RoutineDefaults, error) {
		p, ok := original.(*RoutineDefaults)
		if !ok || p == nil {
			return nil, fmt.Errorf("stuntgres: invalid SQL routine default payload")
		}
		if mapped := defaults[p]; mapped != nil {
			return mapped, nil
		}
		mapped, err := p.Rebind(view, mapping, r)
		if err != nil {
			return nil, err
		}
		defaults[p] = mapped
		for _, user := range defaultUsers[p] {
			user.SQLDefaults = mapped
		}
		return mapped, nil
	}
	attach := func(p *catalog.Procedure) error {
		if c := p.HostConversion; c != nil {
			mapped := hostConversions[c]
			if mapped == nil {
				copy := *c
				copy.Type = r.Type(c.Type)
				if copy.Type == nil {
					return fmt.Errorf("stuntgres: missing host conversion destination type")
				}
				if address, ok := mapping[c.Handler]; ok {
					copy.Handler = address
				}
				mapped = &copy
				hostConversions[c], hostConversions[mapped] = mapped, mapped
			}
			p.HostConversion = mapped
		}
		if original := p.RangeConstructor; original != nil {
			mapped := r.Type(original.Type)
			if mapped == nil {
				return fmt.Errorf("stuntgres: missing generated constructor destination type")
			}
			if mapped != original.Type {
				provider := constructors[original]
				if provider == nil {
					copy := *original
					copy.Type = mapped
					provider = &copy
					if constructors == nil {
						constructors = make(map[*catalog.RangeConstructor]*catalog.RangeConstructor)
					}
					constructors[original], constructors[provider] = provider, provider
				}
				p.RangeConstructor = provider
			}
			signature, err := p.RangeConstructor.Signature(view)
			if err != nil {
				return err
			}
			p.Signature, p.Result = signature, ids.OID(mapped.ID())
		}
		if p.RoutineDefinition() == nil {
			return nil
		}
		definition, err := r.Definition(p.RoutineDefinition())
		if err != nil {
			return err
		}
		if h := p.Host; h != nil {
			mapped := hostDeclarations[h]
			if mapped == nil {
				copy := *h
				copy.Layout = definition
				copy.Converters = append([]ids.ObjectAddress(nil), h.Converters...)
				copy.Pairs = append([]ids.ObjectAddress(nil), h.Pairs...)
				for i, address := range copy.Converters {
					if next, ok := mapping[address]; ok {
						copy.Converters[i] = next
					}
				}
				for i, address := range copy.Pairs {
					if next, ok := mapping[address]; ok {
						copy.Pairs[i] = next
					}
				}
				mapped = &copy
				hostDeclarations[h], hostDeclarations[mapped] = mapped, mapped
			}
			p.Host = mapped
		} else {
			p.SQL = definition
		}
		p.Result = ids.OID(definition.Result.Type.ID())
		parts := make([]string, len(definition.Inputs))
		for i, input := range definition.Inputs {
			parts[i] = strconv.FormatUint(uint64(input.Type.ID()), 10)
		}
		p.Signature = catalog.ProcedureSignature(strings.Join(parts, ","))
		if p.SQLBody != nil {
			original, ok := p.SQLBody.(*RoutineProgram)
			if !ok || original == nil {
				return fmt.Errorf("stuntgres: invalid SQL routine body payload")
			}
			bodyUsers[original] = append(bodyUsers[original], p)
			if mapped := bodies[original]; mapped != nil {
				p.SQLBody = mapped
			}
		}
		if p.SQLDefaults != nil {
			original, ok := p.SQLDefaults.(*RoutineDefaults)
			if !ok || original == nil {
				return fmt.Errorf("stuntgres: invalid SQL routine default payload")
			}
			defaultUsers[original] = append(defaultUsers[original], p)
			if mapped := defaults[original]; mapped != nil {
				p.SQLDefaults = mapped
			}
		}
		return nil
	}
	var ready bool
	var declarations []*catalog.Procedure
	prepare := func(p *catalog.Procedure) error {
		if ready {
			return attach(p)
		}
		declarations = append(declarations, p)
		return nil
	}
	r.Declarations = func() error {
		if ready {
			return nil
		}
		ready = true
		for _, p := range declarations {
			if err := attach(p); err != nil {
				return err
			}
		}
		declarations = nil
		return nil
	}
	r.Procedure = extension.ProcedureRemapper(r.Descriptor, originalProcedureMapper(view, nil, principal), prepare)
	r.Finish = func() error {
		if err := r.Declarations(); err != nil {
			return err
		}
		// Type evolution explicitly rebinds only affected payloads before
		// visiting consumers. Every other original payload stays immutable.
		for !typesOnly {
			pending := false
			for original := range defaultUsers {
				if defaults[original] == nil {
					pending = true
					if _, err := r.Defaults(original); err != nil {
						return err
					}
				}
			}
			for original := range bodyUsers {
				if bodies[original] == nil {
					pending = true
					if _, err := r.Body(original); err != nil {
						return err
					}
				}
			}
			if !pending {
				break
			}
		}
		for _, mapped := range defaults {
			if err := mapped.Validate(view); err != nil {
				return err
			}
		}
		for _, mapped := range bodies {
			if err := mapped.Validate(view); err != nil {
				return err
			}
		}
		return nil
	}
	return r
}
