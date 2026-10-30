package catalog

import (
	"context"
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
	outputPlan        proc.ResultPlan
	outputTuple       *sqltype.Descriptor
}

// SetLayout rebuilds the sole-output plan in an unpublished admission/remap.
func (h *HostRoutine) SetLayout(layout *proc.Definition) error {
	h.Layout, h.outputTuple = layout, nil
	h.outputPlan = proc.ResultPlan{}
	if layout.Procedure {
		if len(layout.OutputFormals) != 1 || !layout.MatchesOutputTuple(layout.Result.Type) {
			return fmt.Errorf("stuntgres: invalid host procedure output")
		}
		member := HostResultUsage(layout)
		plan, err := proc.PlanTuple([]sqltype.Usage{member}, layout.Result.Type)
		if err != nil { return err }
		h.outputPlan, h.outputTuple = plan, layout.Result.Type
	}
	return nil
}

func (h *HostRoutine) WrapResult(ctx context.Context, value sqltype.Value, result sqltype.Usage, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) (sqltype.Value, error) {
	if h.outputTuple == nil || h.outputTuple != result.Type || result != h.Layout.Result {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid host procedure invocation tuple")
	}
	return h.outputPlan.Apply(ctx, []sqltype.Value{value}, input, output)
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
// HostResultUsage is the scalar program output, distinct from CALL's record.
func HostResultUsage(layout *proc.Definition) sqltype.Usage {
	if layout.Procedure && len(layout.OutputFormals) == 1 {
		index := layout.OutputFormals[0]
		if index < 0 || index >= len(layout.Formals) { return sqltype.Usage{} }
		return layout.Formals[index].Usage
	}
	return layout.Result
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
	if d == nil || v.Get(d.Address()) != d || d.HostConversion != nil || d.SQL != nil || d.Builtin != nil || d.PLHandler != NoPLHandler || d.Foreign != nil || d.Origin != nil || d.Implementation != nil || d.Aggregate != nil || d.RangeConstructor != nil || d.AccessMethod != NoAccessMethod || d.Conversion.Name() != "" || len(d.Body) != 0 || d.SQLBody != nil || (d.RoutineKind != FunctionRoutine && d.RoutineKind != ProcedureRoutine) || !HostRuntimeInstalled(v, hostRuntimeOf(d)) {
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
		if d.RoutineKind != FunctionRoutine || d.Transforms != nil || d.Host != nil || d.Result != 2280 || d.Signature != "" || d.Language != (ids.ObjectAddress{}) || d.SecurityDefiner || d.Configuration != nil || d.SQLDefaults != nil {
			return invalid()
		}
		return nil
	}
	h := d.Host
	if h == nil || h.Program == nil || h.Layout == nil || h.Layout.Kind != proc.HostBody || h.Layout.Standard || h.Layout.Statements != nil || h.Layout.PL != nil || h.Layout.PLSyntax != nil || h.Layout.Invocation != proc.OrdinaryInvocation || h.Layout.ReturnsSet || h.Layout.TableResult || h.Layout.Procedure != (d.RoutineKind == ProcedureRoutine) || (!h.Layout.Procedure && len(h.Layout.OutputFormals) != 0) || (h.Layout.Procedure && len(h.Layout.OutputFormals) != 1) {
		return invalid()
	}
	language, ok := v.Get(d.Language).(*Language)
	if !ok || language.Host != h.Program.Runtime() || (LanguageProvider{}).Validate(v, language) != nil {
		return invalid()
	}
	p, layout := h.Program, h.Layout
	result := HostResultUsage(layout)
	if len(layout.Inputs) != p.InputCount() || len(layout.Names) != len(layout.Inputs) || layout.Source != p.ID() || p.Runtime().Program(p.ID()) != p || HostLogicalType(result.Type) != p.ResultType() || layout.Result.Type == nil || d.Result != ids.OID(layout.Result.Type.ID()) {
		return invalid()
	}
	validateType := func(u sqltype.Usage) bool {
		if u.Type == nil || !u.Valid() {
			return false
		}
		t, ok := v.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(u.Type.ID())})
		return ok && t.SQLType == u.Type
	}
	if !validateType(result) || len(layout.InputFormals) != len(layout.Inputs) || len(layout.FormalInputs) != len(layout.Formals) || (layout.DefaultCount() != 0) != (d.SQLDefaults != nil) || (layout.Procedure && !layout.MatchesOutputTuple(layout.Result.Type)) {
		return invalid()
	}
	inputCount, outputCount := 0, 0
	for i, f := range layout.Formals {
		if !validateType(f.Usage) || (f.Mode != "i" && !(layout.Procedure && (f.Mode == "o" || f.Mode == "b"))) {
			return invalid()
		}
		if f.Output() {
			if !layout.Procedure || layout.OutputFormals[0] != i { return invalid() }
			outputCount++
		}
		slot := layout.FormalInputs[i]
		if f.Input() {
			inputCount++
			if slot < 0 || slot >= len(layout.Inputs) || layout.InputFormals[slot] != i || f.Usage != layout.Inputs[slot] || f.Name != layout.Names[slot] { return invalid() }
		} else if slot != -1 { return invalid() }
	}
	if inputCount != len(layout.Inputs) || outputCount != len(layout.OutputFormals) { return invalid() }
	if ValidateRoutineTransforms(v, d) != nil { return invalid() }
	if (layout.Procedure && h.outputTuple != layout.Result.Type) || (!layout.Procedure && h.outputTuple != nil) { return invalid() }
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
		typ, from := result.Type, false
		if i < len(layout.Inputs) {
			typ, from = layout.Inputs[i].Type, true
		}
		converter, ok := v.Procedure(address)
		if !ok || converter.HostConversion == nil || converter.HostConversion.Type != typ || converter.HostConversion.Token != h.Tokens[i] || converter.HostConversion.From != from || ValidateHostConversion(v, converter) != nil {
			return invalid()
		}
		selected := ids.ObjectAddress{}
		pairAddress := d.Transforms.PairFor(typ)
		if pairAddress.ObjectID != 0 {
			pair, ok := v.Get(pairAddress).(*Transform)
			if !ok || h.Pairs[i] != pairAddress || pair.Language != d.Language || pair.SQLType != typ || pair.Runtime != p.Runtime() || (TransformProvider{}).Validate(v, pair) != nil {
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

func ResolveHostRoutine(v Read, layout *proc.Definition, p *host.Program, language ids.ObjectAddress, associations *RoutineTransforms) (*HostRoutine, error) {
	count := len(layout.Inputs) + 1
	h := &HostRoutine{Layout: layout, Program: p, Converters: make([]ids.ObjectAddress, count), Pairs: make([]ids.ObjectAddress, count), Tokens: make([]*host.Conversion, count)}
	if err := h.SetLayout(layout); err != nil { return nil, err }
	for i := range count {
		typ, from := HostResultUsage(layout).Type, false
		if i < len(layout.Inputs) {
			typ, from = layout.Inputs[i].Type, true
		}
		if address := associations.PairFor(typ); address.ObjectID != 0 {
			pair, ok := v.Get(address).(*Transform)
			if !ok || pair.Language != language || pair.SQLType != typ || pair.Runtime != p.Runtime() || (TransformProvider{}).Validate(v, pair) != nil {
				return nil, fmt.Errorf("stuntgres: host conversion association is unavailable")
			}
			h.Pairs[i] = pair.Address()
			h.Converters[i] = pair.To
			if from { h.Converters[i] = pair.From }
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
			if from { direction = "FROM" }
			return nil, fmt.Errorf("stuntgres: unavailable host %s SQL direction", direction)
		}
		h.Tokens[i] = converter.HostConversion.Token
	}
	return h, nil
}
