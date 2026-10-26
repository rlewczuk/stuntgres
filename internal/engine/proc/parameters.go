package proc

import (
	"fmt"
	"slices"
	"strconv"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Formal is declaration metadata, never an executable default or invocation value.
type Formal struct {
	Mode       string // i, o, b, v, t
	Name       string
	Usage      sqltype.Usage
	Position   parser.Position
	HasDefault bool
}

func (f Formal) Input() bool  { return f.Mode == "i" || f.Mode == "b" || f.Mode == "v" }
func (f Formal) Output() bool { return f.Mode == "o" || f.Mode == "b" || f.Mode == "t" }

// SetFormals installs one owned layout and derives every projection from it.
// The definition must still be unpublished.
func (d *Definition) SetFormals(formals []Formal) error {
	projections := make([]int, 3*len(formals))
	inputs, outputs := projections[:0:len(formals)], projections[len(formals):len(formals):2*len(formals)]
	slots := projections[2*len(formals):]
	variadic, defaulted := false, false
	for i, f := range formals {
		fail := func(message string) error {
			d := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P13", Message: message}
			if f.Position.Character > 0 {
				d.Position = strconv.Itoa(f.Position.Character)
			}
			return d
		}
		if f.Mode != "i" && f.Mode != "o" && f.Mode != "b" && f.Mode != "v" && f.Mode != "t" {
			return fmt.Errorf("invalid routine parameter mode")
		}
		if !DeclarationUsage(f.Usage) {
			return fmt.Errorf("invalid routine formal usage")
		}
		for _, earlier := range formals[:i] {
			if f.Name != "" && f.Name == earlier.Name && (f.Input() && earlier.Input() || f.Output() && earlier.Output()) {
				return fail(fmt.Sprintf("parameter name %q used more than once", f.Name))
			}
		}
		slots[i] = -1
		if f.Input() {
			if variadic {
				return fail("VARIADIC parameter must be the last input parameter")
			}
			if f.Mode == "v" {
				if !f.Usage.Type.IsArray() && f.Usage.Type.ID() != 2277 && f.Usage.Type.ID() != 5078 {
					return fail("VARIADIC parameter must be an array")
				}
				variadic = true
			}
			if defaulted && !f.HasDefault {
				return fail("input parameters after one with a default value must also have defaults")
			}
			defaulted = defaulted || f.HasDefault
			slots[i] = len(inputs)
			inputs = append(inputs, i)
		} else if f.HasDefault {
			return fail("only input parameters can have default values")
		}
		if f.Output() {
			outputs = append(outputs, i)
		}
	}
	d.Formals, d.InputFormals, d.OutputFormals, d.FormalInputs = slices.Clone(formals), inputs, outputs, slots
	d.Inputs, d.Names = make([]sqltype.Usage, len(inputs)), make([]string, len(inputs))
	for slot, index := range inputs {
		d.Inputs[slot], d.Names[slot] = formals[index].Usage, formals[index].Name
	}
	return nil
}

func (d *Definition) OutputName() string {
	if len(d.OutputFormals) != 1 {
		return ""
	}
	return d.Formals[d.OutputFormals[0]].Name
}

func (d *Definition) DefaultCount() int {
	n := 0
	for _, f := range d.Formals {
		if f.HasDefault {
			n++
		}
	}
	return n
}

func (d *Definition) HasDefault(slot int) bool {
	return slot >= 0 && slot < len(d.InputFormals) && d.Formals[d.InputFormals[slot]].HasDefault
}
