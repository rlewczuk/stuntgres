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
	r := &catalog.RoutineRemap{Type: analyze.RecordTypeMapper(typ, collation), Collation: collation}
	definitions := make(map[*proc.Definition]*proc.Definition)
	var programs map[*proc.PLProgram]*proc.PLProgram
	usages := &ViewQueryRemapper{Type: r.Type, Collation: r.Collation}
	bodies := make(map[*RoutineProgram]catalog.RoutineProgram)
	defaults := make(map[*RoutineDefaults]catalog.RoutineDefaults)
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
		if p.SQL == nil {
			return nil
		}
		var err error
		p.SQL, err = r.Definition(p.SQL)
		if err != nil {
			return err
		}
		p.Result = ids.OID(p.SQL.Result.Type.ID())
		parts := make([]string, len(p.SQL.Inputs))
		for i, input := range p.SQL.Inputs {
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
	r.Procedure = extension.ProcedureRemapper(mapping, originalProcedureMapper(view, nil, principal), prepare)
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
