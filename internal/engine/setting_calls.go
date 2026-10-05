package engine

import (
	"fmt"
	"strconv"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type parameterInference struct {
	used  map[int]sqltype.Usage
	count int
}

func (p *parameterInference) bind(e *Engine, v parser.Value, c ColumnDescriptor, mode sqltype.CoercionContext) (boundValue, error) {
	if v.Kind != parser.ParameterValue {
		return e.bindValue(v, c.Usage, mode)
	}
	if v.Parameter <= 0 {
		return boundValue{}, sqlError("42P02", "undefined parameter")
	}
	if t, ok := p.used[v.Parameter]; ok && t.Type != c.Usage.Type {
		return boundValue{}, sqlError("0A000", "parameter reused with different destination types")
	}
	p.used[v.Parameter] = c.Usage
	if v.Parameter > p.count {
		p.count = v.Parameter
	}
	return boundValue{parameter: v.Parameter}, nil
}
func (p *parameterInference) finish() (ParameterInfo, error) {
	if len(p.used) != p.count {
		return ParameterInfo{}, sqlError("42P18", "could not determine unused parameter type")
	}
	info := ParameterInfo{Count: p.count, Types: make([]sqltype.Usage, p.count)}
	for n, t := range p.used {
		info.Types[n-1] = t
	}
	return info, nil
}
func settingColumns(typ *sqltype.Descriptor, names ...string) []ColumnDescriptor {
	columns := make([]ColumnDescriptor, len(names))
	for i, name := range names {
		columns[i] = ColumnDescriptor{Name: name, Usage: typ.Usage(), NullabilityKnown: true}
	}
	return columns
}

// describeSettingCall uses the same inference and literal binding as relations.
// The caller holds e.mu over descriptor lookup and binding.
func (e *Engine) describeSettingCall(s *parser.SettingCall) (ParameterInfo, []boundValue, error) {
	if !(s.Name == "current_setting" && len(s.Args) == 1 || s.Name == "set_config" && len(s.Args) == 3) {
		return ParameterInfo{}, nil, sqlError("0A000", "unsupported setting function signature")
	}
	inference := parameterInference{used: make(map[int]sqltype.Usage)}
	bound := make([]boundValue, len(s.Args))
	view := CatalogRead{owner: e, state: e.state}
	for i, v := range s.Args {
		if v.Kind != parser.NullValue && v.Kind != parser.ParameterValue && v.Kind != parser.StringValue && !(i == 2 && v.Kind == parser.BooleanValue) {
			return ParameterInfo{}, nil, sqlError("0A000", "unsupported setting function signature")
		}
		id := sqltype.Text
		if i == 2 {
			id = sqltype.Bool
		}
		typ, ok := view.LookupType(id)
		if !ok {
			return ParameterInfo{}, nil, fmt.Errorf("stuntgres: missing setting argument type")
		}
		var err error
		bound[i], err = inference.bind(e, v, ColumnDescriptor{Usage: typ.Usage()}, sqltype.Assignment)
		if err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && v.Kind == parser.StringValue && diagnostic.Code == "22P02" {
				copy := *diagnostic
				copy.Position = strconv.Itoa(v.Pos.Character)
				err = &copy
			}
			return ParameterInfo{}, nil, err
		}
	}
	info, err := inference.finish()
	return info, bound, err
}
func (e *Engine) executeSettingCall(op *Operation, s *parser.SettingCall, count int, importFrame FrameImporter) (Result, error) {
	e.mu.Lock()
	info, bound, err := e.describeSettingCall(s)
	typ, typeOK := (CatalogRead{owner: e, state: e.state}).LookupType(sqltype.Text)
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if !typeOK {
		return Result{}, fmt.Errorf("stuntgres: missing setting result type")
	}
	if count != info.Count {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want %d", count, info.Count)
	}
	frame := make([]sqltype.Value, count)
	if count != 0 {
		if importFrame == nil {
			return Result{}, fmt.Errorf("stuntgres: missing parameter importer")
		}
		if err := importFrame(op.ctx, info, frame); err != nil {
			return Result{}, err
		}
		for i, v := range frame {
			if v.Type() != info.Types[i].Type {
				return Result{}, fmt.Errorf("stuntgres: invalid parameter frame")
			}
		}
	}
	var inputs [3]sqltype.Value
	for i, v := range bound {
		inputs[i], err = v.evaluate(op.ctx, frame)
		if err != nil {
			return Result{}, err
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	owner := op.session.settings
	owner.mu.Lock()
	defer owner.mu.Unlock()
	op.session.mu.Lock()
	defer op.session.mu.Unlock()
	if err := publicationOperationLocked(e, op); err != nil {
		return Result{}, err
	}
	output := sqltype.Null(typ)
	if s.Name == "current_setting" {
		if !inputs[0].IsNull() {
			name, err := inputs[0].Text()
			if err != nil {
				return Result{}, err
			}
			d, err := e.sessionSetting(owner, name, false)
			if err != nil {
				return Result{}, err
			}
			output = sqltype.String(typ, owner.value(d.Name))
		}
		if err := op.ctx.Err(); err != nil {
			return Result{}, err
		}
	} else {
		if inputs[0].IsNull() {
			return Result{}, sqlError("22004", "SET requires parameter name")
		}
		if !inputs[2].IsNull() {
			local, err := inputs[2].Boolean()
			if err != nil {
				return Result{}, err
			}
			if local {
				return Result{}, sqlError("0A000", "transaction-local settings are unsupported")
			}
		}
		name, err := inputs[0].Text()
		if err != nil {
			return Result{}, err
		}
		d, err := e.sessionSetting(owner, name, true)
		if err != nil {
			return Result{}, err
		}
		var input *string
		if !inputs[1].IsNull() {
			text, err := inputs[1].Text()
			if err != nil {
				return Result{}, err
			}
			input = &text
		}
		value, err := e.applySetting(op, owner, d, input)
		if err != nil {
			return Result{}, err
		}
		output = sqltype.String(typ, value)
	}
	columns := settingColumns(typ, s.Name)
	columns[0].Nullable = true
	return Result{Count: 1, Columns: columns, Rows: anonymousSnapshot([][]sqltype.Value{{output}}, []int{0})}, nil
}
