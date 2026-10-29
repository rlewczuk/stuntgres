package pgcatalog

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

// A declaration formal is independent of invocation expansion and result shape.
// Usage is present for checked SQL/PL formals; other types are catalog identities.
type ProcedureFormal struct {
	Type         ids.OID
	Usage        sqltype.Usage
	Mode         string
	Index, Input int
	HasDefault   bool
}

func VisitProcedureFormals(ctx context.Context, d *catalog.Procedure, inputTypes sqltype.Value, visit func(ProcedureFormal) error) error {
	if d.RoutineDefinition() != nil {
		if len(d.RoutineDefinition().Formals) != len(d.RoutineDefinition().FormalInputs) || len(d.RoutineDefinition().Inputs) != len(d.RoutineDefinition().InputFormals) {
			return fmt.Errorf("stuntgres: invalid routine formal layout")
		}
		input := 0
		for i, f := range d.RoutineDefinition().Formals {
			if err := ctx.Err(); err != nil {
				return err
			}
			if f.Usage.Type == nil {
				return fmt.Errorf("stuntgres: missing routine formal type")
			}
			slot := -1
			switch f.Mode {
			case "i", "b", "v":
				slot = input
				input++
			case "o", "t":
				if f.HasDefault {
					return fmt.Errorf("stuntgres: output formal has default")
				}
			default:
				return fmt.Errorf("stuntgres: invalid routine formal mode")
			}
			if d.RoutineDefinition().FormalInputs[i] != slot || slot >= 0 && (slot >= len(d.RoutineDefinition().InputFormals) || d.RoutineDefinition().InputFormals[slot] != i || d.RoutineDefinition().Inputs[slot] != f.Usage) {
				return fmt.Errorf("stuntgres: invalid routine input projection")
			}
			if err := visit(ProcedureFormal{ids.OID(f.Usage.Type.ID()), f.Usage, f.Mode, i, slot, f.HasDefault}); err != nil {
				return err
			}
		}
		if input != len(d.RoutineDefinition().Inputs) {
			return fmt.Errorf("stuntgres: invalid routine input count")
		}
		return nil
	}
	variadic, err := ProcedureVariadic(d)
	if err != nil {
		return err
	}
	inputs := int(ProcedureArity(d))
	outputs := d.OutputDeclaration()
	if outputs != nil && outputs.Inputs() != inputs {
		return fmt.Errorf("stuntgres: invalid fixed output declaration")
	}
	defaults := int(d.Installed.Defaults)
	if d.Builtin != nil {
		defaults = int(d.Builtin.Defaults)
	}
	if defaults > inputs {
		return fmt.Errorf("stuntgres: invalid routine default count")
	}
	remaining := string(d.Signature)
	if inputTypes.Type() != nil {
		if !inputTypes.Type().IsCatalogVector() || inputTypes.IsNull() || inputTypes.ArrayLen() != inputs {
			return fmt.Errorf("stuntgres: invalid routine input vector")
		}
		remaining = ""
	}
	for i := range inputs {
		if err := ctx.Err(); err != nil {
			return err
		}
		var n uint32
		if inputTypes.Type() != nil {
			var err error
			n, err = inputTypes.ArrayElement(i).OID()
			if err != nil {
				return err
			}
		} else {
			token, rest, _ := strings.Cut(remaining, ",")
			remaining = rest
			parsed, err := strconv.ParseUint(token, 10, 32)
			if err != nil || parsed == 0 {
				return fmt.Errorf("stuntgres: invalid routine signature")
			}
			n = uint32(parsed)
		}
		mode := "i"
		if variadic != 0 && i == inputs-1 {
			mode = "v"
		}
		if err := visit(ProcedureFormal{Type: ids.OID(n), Mode: mode, Index: i, Input: i, HasDefault: i >= inputs-defaults}); err != nil {
			return err
		}
	}
	if remaining != "" || variadic != 0 && inputs == 0 {
		return fmt.Errorf("stuntgres: invalid routine signature layout")
	}
	if outputs != nil {
		for i := range outputs.Len() {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := visit(ProcedureFormal{Type: ids.OID(outputs.Type(i)), Mode: "o", Index: inputs + i, Input: -1}); err != nil {
				return err
			}
		}
	}
	return nil
}

// One name projection serves pg_proc arrays and parameter rows. It borrows only
// immutable declaration data, never a provider binding or inferred output.
type ProcedureNames struct {
	sql        *proc.Definition
	names      []string
	inputNames *catalog.RoutineInputNames
	outputs    *catalog.RoutineOutputs
	installed  [4]string
	uuid       [2]string
	count      int
	present    bool
}

func (n ProcedureNames) At(i int) string {
	if n.sql != nil {
		return n.sql.Formals[i].Name
	}
	if n.names != nil {
		return n.names[i]
	}
	if n.outputs != nil {
		return n.outputs.Name(i)
	}
	if n.installed[0] != "" {
		return n.installed[i]
	}
	if n.inputNames != nil {
		return n.inputNames.At(i)
	}
	if n.uuid[0] != "" {
		return n.uuid[i]
	}
	return ""
}
func ProcedureNameProjection(view catalog.Read, d *catalog.Procedure) (ProcedureNames, error) {
	n := ProcedureNames{count: int(ProcedureArity(d))}
	if d.RoutineDefinition() != nil {
		n.sql = d.RoutineDefinition()
		n.count = len(d.RoutineDefinition().Formals)
		for _, f := range d.RoutineDefinition().Formals {
			n.present = n.present || f.Name != ""
		}
	} else if d.Aggregate != nil && len(d.Aggregate.ArgumentNames) > 0 {
		n.names = d.Aggregate.ArgumentNames
		n.present = true
		if len(n.names) != n.count {
			return n, fmt.Errorf("stuntgres: invalid aggregate argument-name declaration")
		}
	} else if outputs := d.OutputDeclaration(); outputs != nil {
		if outputs.Inputs() != n.count {
			return n, fmt.Errorf("stuntgres: invalid fixed output names")
		}
		n.outputs = outputs
		n.count += outputs.Len()
		n.present = true
	} else if d.InputNames != nil {
		if d.InputNames.Len() != n.count {
			return n, fmt.Errorf("stuntgres: invalid builtin input names")
		}
		n.inputNames = d.InputNames
		n.present = true
	} else if installation, ok := catalog.OriginInstallation(d); ok && installation.Provider == "uuid-ossp" && d.Implementation != nil {
		if err := validateInstalledProcedureOwner(view, d); err != nil {
			return n, err
		}
		slot := d.Origin.Declaration
		if slot >= len(functions.UUIDOsspMembers) {
			return n, fmt.Errorf("stuntgres: invalid UUID routine manifest slot")
		}
		member := functions.UUIDOsspMembers[slot]
		if member.Signature == string(d.Signature) && member.Signature != "" {
			n.uuid = member.ArgumentNames
			n.present = true
			if n.count != 2 {
				return n, fmt.Errorf("stuntgres: invalid UUID routine names")
			}
		}
	}
	if d.Installed.Outputs == nil && d.Installed.ArgumentNames[0] != "" {
		if n.count > len(n.installed) {
			return n, fmt.Errorf("stuntgres: invalid installed routine names")
		}
		n.sql = nil
		n.names = nil
		n.outputs = nil
		n.installed = d.Installed.ArgumentNames
		n.present = true
	}
	return n, nil
}

// The registered procedure class owner validates the concrete installed
// declaration. ExtensionImplementation alone promises only extension metadata;
// it is not a callable validation capability.
func validateInstalledProcedureOwner(view catalog.Read, d *catalog.Procedure) error {
	if d == nil || d.Implementation == nil || view.Owner() == nil {
		return fmt.Errorf("stuntgres: missing installed procedure declaration owner")
	}
	provider := view.Owner().Providers[ids.ProcedureClassID]
	if provider == nil {
		return fmt.Errorf("stuntgres: missing registered procedure provider")
	}
	return provider.Validate(view, d)
}
