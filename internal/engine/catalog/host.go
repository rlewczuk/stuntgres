package catalog

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/host"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

// HostRoutine keeps the common typed layout separate from compiled execution.
// Tokens are resolved against genuine current directional catalog declarations.
type HostRoutine struct {
	Layout            *proc.Definition
	Program           *host.Program
	Converters, Pairs []ids.ObjectAddress
	Tokens            []*host.Conversion
}

func (d *Procedure) RoutineDefinition() *proc.Definition {
	if d == nil {
		return nil
	}
	if d.Host != nil {
		return d.Host.Layout
	}
	return d.SQL
}
func HostRuntimeInstalled(v Read, r *host.Runtime) bool {
	if v.Owner() == nil || r == nil {
		return false
	}
	for _, installed := range v.Owner().HostRuntimes {
		if installed == r {
			return true
		}
	}
	return false
}
func HostLogicalType(t *sqltype.Descriptor) host.Type {
	if t == nil {
		return ""
	}
	if t.IsHstore() {
		return host.Hstore
	}
	switch t.ID() {
	case sqltype.Bool:
		return host.Bool
	case sqltype.Int4:
		return host.Int4
	case sqltype.Int8:
		return host.Int8
	case sqltype.Text:
		return host.Text
	}
	return ""
}
func hostRuntimeOf(d *Procedure) *host.Runtime {
	if d.HostHandler != nil {
		return d.HostHandler
	}
	if d.Host != nil && d.Host.Program != nil {
		return d.Host.Program.Runtime()
	}
	return nil
}
func ValidateHostProcedure(v Read, d *Procedure) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid host procedure authority") }
	if d == nil || v.Get(d.Address()) != d || d.HostConversion != nil || d.SQL != nil || d.Builtin != nil || d.PLHandler != NoPLHandler || d.Foreign != nil || d.Origin != nil || d.Implementation != nil || d.Aggregate != nil || d.RangeConstructor != nil || d.AccessMethod != NoAccessMethod || d.Conversion.Name() != "" || len(d.Body) != 0 || d.SQLBody != nil || d.RoutineKind != FunctionRoutine || !HostRuntimeInstalled(v, hostRuntimeOf(d)) {
		return invalid()
	}
	if d.Display.Language != GoRoutineLanguage {
		return invalid()
	}
	if d.Address().ClassID != ids.ProcedureClassID || d.Address().ObjectID == 0 || d.Address().SubID != 0 || d.Revision() == 0 || v.NameAddress(DescriptorNameKey(d)) != d.Address() || d.Dictionary != (ids.ObjectAddress{}) {
		return invalid()
	}
	if d.BrinSupport != 0 || d.SnowballHelper != NoSnowballHelper || d.InputNames != nil || d.LiteralDefaults != nil || d.DeclarationBody != nil || len(d.DeclarationDependencies) != 0 {
		return invalid()
	}
	if d.HostHandler != nil {
		if d.Host != nil || d.Result != 2280 || d.Signature != "" || d.Language != (ids.ObjectAddress{}) || d.SecurityDefiner || d.Configuration != nil || d.SQLDefaults != nil {
			return invalid()
		}
		return nil
	}
	h := d.Host
	if h == nil || h.Program == nil || h.Layout == nil || h.Layout.Kind != proc.HostBody || h.Layout.Standard || h.Layout.Statements != nil || h.Layout.PL != nil || h.Layout.PLSyntax != nil || h.Layout.Invocation != proc.OrdinaryInvocation || h.Layout.Procedure || h.Layout.ReturnsSet || h.Layout.TableResult || len(h.Layout.OutputFormals) != 0 {
		return invalid()
	}
	language, ok := v.Get(d.Language).(*Language)
	if !ok || language.Host != h.Program.Runtime() || (LanguageProvider{}).Validate(v, language) != nil {
		return invalid()
	}
	p, layout := h.Program, h.Layout
	if len(layout.Inputs) != p.InputCount() || len(layout.Names) != len(layout.Inputs) || layout.Source != p.ID() || p.Runtime().Program(p.ID()) != p || HostLogicalType(layout.Result.Type) != p.ResultType() || layout.Result.Type == nil || d.Result != ids.OID(layout.Result.Type.ID()) {
		return invalid()
	}
	validateType := func(u sqltype.Usage) bool {
		if u.Type == nil || !u.Valid() {
			return false
		}
		t, ok := v.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(u.Type.ID())})
		return ok && t.SQLType == u.Type
	}
	if !validateType(layout.Result) || len(layout.Formals) != len(layout.Inputs) || len(layout.InputFormals) != len(layout.Inputs) || len(layout.FormalInputs) != len(layout.Inputs) || (layout.DefaultCount() != 0) != (d.SQLDefaults != nil) {
		return invalid()
	}
	for i, f := range layout.Formals {
		if f.Mode != "i" || f.Usage != layout.Inputs[i] || f.Name != layout.Names[i] || layout.InputFormals[i] != i || layout.FormalInputs[i] != i {
			return invalid()
		}
	}
	signature := string(d.Signature)
	for i, u := range layout.Inputs {
		if HostLogicalType(u.Type) != p.InputType(i) || !validateType(u) {
			return invalid()
		}
		part, rest, more := strings.Cut(signature, ",")
		id, err := strconv.ParseUint(part, 10, 32)
		if err != nil || sqltype.ID(id) != u.Type.ID() || more != (i < len(layout.Inputs)-1) {
			return invalid()
		}
		signature = rest
	}
	if signature != "" || len(h.Converters) != len(layout.Inputs)+1 || len(h.Pairs) != len(h.Converters) || len(h.Tokens) != len(h.Converters) {
		return invalid()
	}
	for i, address := range h.Converters {
		typ, from, selector := layout.Result.Type, false, p.ResultConversion()
		if i < len(layout.Inputs) {
			typ, from, selector = layout.Inputs[i].Type, true, p.InputConversion(i)
		}
		converter, ok := v.Procedure(address)
		if !ok || converter.HostConversion == nil || converter.HostConversion.Type != typ || converter.HostConversion.Token != h.Tokens[i] || converter.HostConversion.From != from || ValidateHostConversion(v, converter) != nil {
			return invalid()
		}
		selected := ids.ObjectAddress{}
		if selector != "" {
			pair, ok := v.Get(h.Pairs[i]).(*Transform)
			if !ok || pair.Language != d.Language || pair.SQLType != typ || pair.Runtime != p.Runtime() || pair.Conversion.Definition().ID != selector || (TransformProvider{}).Validate(v, pair) != nil {
				return invalid()
			}
			selected = pair.To
			if from {
				selected = pair.From
			}
		} else if h.Pairs[i].ObjectID != 0 {
			return invalid()
		}
		if selected.ObjectID != 0 {
			if selected != address {
				return invalid()
			}
		} else if p.Runtime().DefaultConversion(HostLogicalType(typ), from) != h.Tokens[i] {
			return invalid()
		}
	}
	return nil
}

func ResolveHostRoutine(v Read, layout *proc.Definition, p *host.Program, language ids.ObjectAddress) (*HostRoutine, error) {
	count := len(layout.Inputs) + 1
	h := &HostRoutine{Layout: layout, Program: p, Converters: make([]ids.ObjectAddress, count), Pairs: make([]ids.ObjectAddress, count), Tokens: make([]*host.Conversion, count)}
	for i := range count {
		typ, from, selector := layout.Result.Type, false, p.ResultConversion()
		if i < len(layout.Inputs) {
			typ, from, selector = layout.Inputs[i].Type, true, p.InputConversion(i)
		}
		if selector != "" {
			for _, d := range v.Descriptors() {
				pair, ok := d.(*Transform)
				if ok && pair.Language == language && pair.SQLType == typ && pair.Runtime == p.Runtime() && pair.Conversion.Definition().ID == selector {
					h.Pairs[i] = pair.Address()
					h.Converters[i] = pair.To
					if from {
						h.Converters[i] = pair.From
					}
					break
				}
			}
			if h.Pairs[i].ObjectID == 0 {
				return nil, fmt.Errorf("stuntgres: host conversion association is unavailable")
			}
		}
		if h.Converters[i].ObjectID == 0 {
			token := p.Runtime().DefaultConversion(HostLogicalType(typ), from)
			for _, d := range v.Descriptors() {
				converter, ok := d.(*Procedure)
				if ok && converter.HostConversion != nil && converter.HostConversion.Token == token && converter.HostConversion.Type == typ && converter.HostConversion.From == from {
					h.Converters[i] = converter.Address()
					break
				}
			}
		}
		converter, ok := v.Procedure(h.Converters[i])
		if !ok || converter.HostConversion == nil {
			direction := "TO"
			if from {
				direction = "FROM"
			}
			return nil, fmt.Errorf("stuntgres: unavailable host %s SQL direction", direction)
		}
		h.Tokens[i] = converter.HostConversion.Token
	}
	return h, nil
}
