package proc

import (
	"context"
	"fmt"
	"strconv"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// OutputTuple preserves every declared output, including a single composite OUT.
// The returned descriptor is declaration-owned, never a catalog RECORD identity.
func (d *Definition) OutputTuple() (*sqltype.Descriptor, error) {
	members := make([]sqltype.CompositeMember, len(d.OutputFormals))
	for i, index := range d.OutputFormals {
		if index < 0 || index >= len(d.Formals) || !d.Formals[index].Output() {
			return nil, fmt.Errorf("invalid routine output projection")
		}
		f := d.Formals[index]
		name := f.Name
		if name == "" {
			name = "column" + strconv.Itoa(i+1)
		}
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: name, Usage: f.Usage}
	}
	return sqltype.RecordShape(members)
}

// DerivedResult reports a function's formal-derived anonymous record policy.
func (d *Definition) DerivedResult() bool { return len(d.OutputFormals) > 1 }

// MatchesOutputTuple checks exact formal provenance, including child generations.
func (d *Definition) MatchesOutputTuple(t *sqltype.Descriptor) bool {
	if t == nil || t.ID() != 2249 || !t.IsComposite() || t.CompositeLen() != len(d.OutputFormals) {
		return false
	}
	for i, index := range d.OutputFormals {
		if index < 0 || index >= len(d.Formals) || !d.Formals[index].Output() {
			return false
		}
		f := d.Formals[index]
		name := f.Name
		if name == "" {
			name = "column" + strconv.Itoa(i+1)
		}
		m := t.CompositeMember(i)
		if m.Attribute != int32(i+1) || m.Name != name || m.Usage != f.Usage {
			return false
		}
	}
	return true
}

// ResultPlan is an immutable terminal-row conversion, shared by validation and
// execution. Tuple plans own ordinal conversions, not aliases or input slots.
type ResultPlan struct {
	result      sqltype.Usage
	whole       bool
	conversions []sqltype.CoercionPlan
}

func PlanResult(columns []sqltype.Usage, result sqltype.Usage, names ...func(sqltype.ID) string) (ResultPlan, error) {
	var name func(sqltype.ID) string
	if len(names) != 0 {
		name = names[0]
	}
	return planResult(columns, result, true, name)
}

// PlanTuple always preserves the output tuple, even for one composite member.
// Consumers retain the declaration's OutputTuple descriptor and this plan.
func PlanTuple(columns []sqltype.Usage, tuple *sqltype.Descriptor) (ResultPlan, error) {
	if tuple == nil || !tuple.IsComposite() {
		return ResultPlan{}, fmt.Errorf("invalid routine output tuple")
	}
	return planResult(columns, tuple.Usage(), false, nil)
}

func planResult(columns []sqltype.Usage, result sqltype.Usage, whole bool, name func(sqltype.ID) string) (ResultPlan, error) {
	p := ResultPlan{result: result}
	mismatch := func(detail string) (ResultPlan, error) {
		return ResultPlan{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P13", Message: "return type mismatch in function declared to return " + ResultDiagnosticName(result.Type, name), Detail: detail}
	}
	// Domains (including composite domains) require a whole value conversion.
	if whole && len(columns) == 1 {
		if conversion, err := sqltype.PlanCoercion(columns[0], result, sqltype.Assignment); err == nil {
			p.whole = true
			p.conversions = []sqltype.CoercionPlan{conversion}
			return p, nil
		}
	}
	if !result.Type.IsComposite() {
		if len(columns) != 1 {
			return mismatch("Final statement must return exactly one column.")
		}
		return mismatch("Actual return type is " + ResultDiagnosticName(columns[0].Type, name) + ".")
	}
	width := result.Type.CompositeLen()
	p.conversions = make([]sqltype.CoercionPlan, width)
	for i, column := range columns {
		if i == width {
			return mismatch("Final statement returns too many columns.")
		}
		target := result.Type.CompositeMember(i).Usage
		conversion, err := sqltype.PlanCoercion(column, target, sqltype.Assignment)
		if err != nil {
			return mismatch(fmt.Sprintf("Final statement returns %s instead of %s at column %d.", ResultDiagnosticName(column.Type, name), ResultDiagnosticName(target.Type, name), i+1))
		}
		p.conversions[i] = conversion
	}
	if len(columns) < width {
		return mismatch("Final statement returns too few columns.")
	}
	return p, nil
}

// Apply returns an owned immutable datum; absent rows are handled by the caller
// as whole typed NULL, distinct from a present tuple of NULL members.
func (p ResultPlan) Apply(ctx context.Context, row []sqltype.Value, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	if len(row) != len(p.conversions) {
		return sqltype.Value{}, fmt.Errorf("invalid routine result row width")
	}
	if p.whole {
		return p.conversions[0].Apply(ctx, row[0], input, output)
	}
	fields := make([]sqltype.Value, len(row))
	for i, value := range row {
		converted, err := p.conversions[i].Apply(ctx, value, input, output)
		if err != nil {
			return sqltype.Value{}, err
		}
		fields[i] = converted
	}
	return sqltype.OwnedCompositeValue(p.result.Type, fields)
}

// ResultDiagnosticName uses the admitted search-path snapshot when available.
// Anonymous shapes keep their SQL identity instead of acquiring a catalog name.
func ResultDiagnosticName(typ *sqltype.Descriptor, name func(sqltype.ID) string) string {
	if typ.ID() != 2249 && typ.IsUserDefined() && name != nil {
		if visible := name(typ.ID()); visible != "" {
			return visible
		}
	}
	return typ.DiagnosticName()
}
