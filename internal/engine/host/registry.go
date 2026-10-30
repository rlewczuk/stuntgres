// Package host owns immutable application-supplied scalar routine capabilities.
// It has no catalog, session, SQL execution or mutable callback lookup authority.
package host

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

type Name struct{ Schema, Name string }
type Type string

const (
	Bool   Type = "bool"
	Int4   Type = "int4"
	Int8   Type = "int8"
	Text   Type = "text"
	Hstore Type = "hstore"
)

type HstoreEntry = sqltype.HstoreEntry
type HstoreValue struct {
	Text string
	Null bool
}
type HstoreMap map[string]HstoreValue
type SQLValue struct {
	Type   Type
	Null   bool
	Bool   bool
	Int64  int64
	Text   string
	Hstore []HstoreEntry
}
type Object struct {
	Null  bool
	Value any
}
type ProgramFunc func(context.Context, []Object) (Object, error)
type HandlerFunc func(context.Context, ProgramFunc, []Object) (Object, error)
type FromSQLFunc func(context.Context, SQLValue) (Object, error)
type ToSQLFunc func(context.Context, Object) (SQLValue, error)
type ConversionDefinition struct {
	ID               string
	Type             Type
	Bootstrap        bool
	FromName, ToName Name
	FromSQL          FromSQLFunc
	ToSQL            ToSQLFunc
}
type ProgramDefinition struct {
	ID, Version      string
	Inputs           []Type
	Result           Type
	Execute          ProgramFunc
}
type DefaultDefinition struct {
	Type         Type
	FromID, ToID string
}
type LanguageDefinition struct {
	Provider, ABIVersion, Name string
	HandlerName                Name
	Trusted                    bool
	Handler                    HandlerFunc
	Programs                   []ProgramDefinition
	Conversions                []ConversionDefinition
	Defaults                   []DefaultDefinition
}

// Runtime's definition and executable references never change after construction.
// Accessors copy declaration slices, never lend registry backing to callers.
type Runtime struct {
	definition  LanguageDefinition
	programs    map[string]*Program
	conversions map[string]*Conversion
}
type Program struct {
	runtime    *Runtime
	definition ProgramDefinition
}
type Conversion struct {
	runtime    *Runtime
	definition ConversionDefinition
}

func validType(t Type) bool         { return t == Bool || t == Int4 || t == Int8 || t == Text || t == Hstore }
func validText(s string) bool       { return s != "" && utf8.ValidString(s) && !strings.ContainsRune(s, 0) }
func validIdentifier(s string) bool { return len(s) <= 63 && validText(s) }
func validName(n Name) bool         { return validIdentifier(n.Schema) && validIdentifier(n.Name) }
func New(d LanguageDefinition) (*Runtime, error) {
	if !validText(d.Provider) || !validText(d.ABIVersion) || !validIdentifier(d.Name) || !validName(d.HandlerName) || d.Handler == nil || len(d.Programs) == 0 {
		return nil, fmt.Errorf("stuntgres: incomplete host language registration")
	}
	r := &Runtime{programs: make(map[string]*Program), conversions: make(map[string]*Conversion)}
	d.Programs = slices.Clone(d.Programs)
	d.Conversions = slices.Clone(d.Conversions)
	d.Defaults = slices.Clone(d.Defaults)
	names := map[Name]bool{d.HandlerName: true}
	pairs := make(map[Type]bool)
	for _, c := range d.Conversions {
		if !validText(c.ID) || !validType(c.Type) || r.conversions[c.ID] != nil || c.FromSQL == nil && c.ToSQL == nil {
			return nil, fmt.Errorf("stuntgres: invalid or duplicate host conversion %q", c.ID)
		}
		if c.Bootstrap {
			if pairs[c.Type] {
				return nil, fmt.Errorf("stuntgres: duplicate host bootstrap pair")
			}
			pairs[c.Type] = true
		}
		for _, direction := range []struct {
			present bool
			name    Name
		}{{c.FromSQL != nil, c.FromName}, {c.ToSQL != nil, c.ToName}} {
			if direction.present {
				if !validName(direction.name) || names[direction.name] {
					return nil, fmt.Errorf("stuntgres: invalid or duplicate host procedure name")
				}
				names[direction.name] = true
			} else if direction.name != (Name{}) {
				return nil, fmt.Errorf("stuntgres: host conversion name without implementation")
			}
		}
		r.conversions[c.ID] = &Conversion{r, c}
	}
	defaults := make(map[Type]DefaultDefinition)
	for _, v := range d.Defaults {
		if !validType(v.Type) || defaults[v.Type].Type != "" || v.FromID == "" && v.ToID == "" {
			return nil, fmt.Errorf("stuntgres: invalid host defaults")
		}
		defaults[v.Type] = v
		for _, x := range []struct {
			id   string
			from bool
		}{{v.FromID, true}, {v.ToID, false}} {
			if x.id != "" {
				c := r.conversions[x.id]
				if c == nil || c.definition.Type != v.Type || x.from && c.definition.FromSQL == nil || !x.from && c.definition.ToSQL == nil {
					return nil, fmt.Errorf("stuntgres: unavailable host default direction")
				}
			}
		}
	}
	// SQL declarations select transforms; Programs retain only logical types.
	for i := range d.Programs {
		p := d.Programs[i]
		p.Inputs = slices.Clone(p.Inputs)
		d.Programs[i] = p
		if !validText(p.ID) || !validText(p.Version) || p.Execute == nil || !validType(p.Result) || r.programs[p.ID] != nil {
			return nil, fmt.Errorf("stuntgres: invalid or duplicate host program %q", p.ID)
		}
		for _, t := range p.Inputs {
			if !validType(t) {
				return nil, fmt.Errorf("stuntgres: unsupported host input type")
			}
		}
		r.programs[p.ID] = &Program{runtime: r, definition: p}
	}
	r.definition = d
	return r, nil
}
func (r *Runtime) Definition() LanguageDefinition {
	d := r.definition
	d.Programs = slices.Clone(d.Programs)
	for i := range d.Programs {
		d.Programs[i].Inputs = slices.Clone(d.Programs[i].Inputs)
	}
	d.Conversions = slices.Clone(d.Conversions)
	d.Defaults = slices.Clone(d.Defaults)
	return d
}
func (r *Runtime) Program(id string) *Program       { return r.programs[id] }
func (r *Runtime) Conversion(id string) *Conversion { return r.conversions[id] }
func (r *Runtime) Provider() string                 { return r.definition.Provider }
func (r *Runtime) ABI() string                      { return r.definition.ABIVersion }
func (r *Runtime) Name() string                     { return r.definition.Name }
func (r *Runtime) HandlerName() Name                { return r.definition.HandlerName }
func (r *Runtime) Trusted() bool                    { return r.definition.Trusted }
func (p *Program) Runtime() *Runtime                { return p.runtime }
func (p *Program) Definition() ProgramDefinition {
	d := p.definition
	d.Inputs = slices.Clone(d.Inputs)
	return d
}
func (c *Conversion) Runtime() *Runtime                { return c.runtime }
func (c *Conversion) Definition() ConversionDefinition { return c.definition }

// Invoke runs only resolved immutable executable references. The caller must
// release every engine/store/session/replacement lock before entering it.
func (p *Program) Invoke(ctx context.Context, inputs []SQLValue, from []*Conversion, to *Conversion, admit func() error) (SQLValue, error) {
	if len(inputs) != len(p.definition.Inputs) || len(from) != len(inputs) {
		return SQLValue{}, fmt.Errorf("stuntgres: invalid host invocation arity")
	}
	if to == nil || to.runtime != p.runtime || to.definition.Type != p.definition.Result || to.definition.ToSQL == nil {
		return SQLValue{}, fmt.Errorf("stuntgres: unavailable host TO SQL direction")
	}
	for i, c := range from {
		if c == nil || c.runtime != p.runtime || c.definition.Type != p.definition.Inputs[i] || c.definition.FromSQL == nil || inputs[i].Type != p.definition.Inputs[i] {
			return SQLValue{}, fmt.Errorf("stuntgres: unavailable host FROM SQL direction")
		}
	}
	call := &invocation{context: ctx, admit: admit, program: p.definition.Execute}
	defer call.close()
	enter := call.enter
	var inline [4]Object
	args := inline[:min(len(inputs), len(inline))]
	if len(inputs) > len(inline) {
		args = make([]Object, len(inputs))
	}
	defer clear(args)
	for i, v := range inputs {
		if err := enter(); err != nil {
			return SQLValue{}, err
		}
		object, err := from[i].definition.FromSQL(ctx, v)
		if admissionErr := enter(); admissionErr != nil {
			return SQLValue{}, admissionErr
		}
		if err != nil {
			return SQLValue{}, err
		}
		if object.Null && object.Value != nil {
			return SQLValue{}, fmt.Errorf("stuntgres: contradictory host null object")
		}
		args[i] = object
	}
	// A retained handler capability captures only a holder whose private SQL
	// continuation references are detached unconditionally when Invoke unwinds.
	execute := call.run
	if err := enter(); err != nil {
		return SQLValue{}, err
	}
	result, err := p.runtime.definition.Handler(ctx, execute, args)
	if admissionErr := enter(); admissionErr != nil {
		return SQLValue{}, admissionErr
	}
	if err != nil {
		return SQLValue{}, err
	}
	if result.Null && result.Value != nil {
		return SQLValue{}, fmt.Errorf("stuntgres: contradictory host null result")
	}
	if err := enter(); err != nil {
		return SQLValue{}, err
	}
	resultSQL, err := to.definition.ToSQL(ctx, result)
	if admissionErr := enter(); admissionErr != nil {
		return SQLValue{}, admissionErr
	}
	if err != nil {
		return SQLValue{}, err
	}
	return resultSQL, nil
}

// Scalar accessors lend no mutable declaration backing on invocation hot paths.
func (p *Program) ID() string                   { return p.definition.ID }
func (p *Program) Version() string              { return p.definition.Version }
func (p *Program) InputCount() int              { return len(p.definition.Inputs) }
func (p *Program) InputType(i int) Type         { return p.definition.Inputs[i] }
func (p *Program) ResultType() Type             { return p.definition.Result }

// ConfiguredNamespace reports only immutable bootstrap name configuration,
// never catalog identity or permission to recreate a retired object.
func (r *Runtime) ConfiguredNamespace(name string) bool {
	if r.definition.HandlerName.Schema == name {
		return true
	}
	for _, c := range r.definition.Conversions {
		if c.FromSQL != nil && c.FromName.Schema == name || c.ToSQL != nil && c.ToName.Schema == name {
			return true
		}
	}
	return false
}
