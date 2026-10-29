package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

const eventTriggerDDLCommandsName = "pg_event_trigger_ddl_commands"

var eventTriggerDDLCommandsNames = [...]string{
	"classid", "objid", "objsubid", "command_tag", "object_type", "schema_name", "object_identity", "in_extension", "command",
}

var eventTriggerDDLCommandsTypes = [...]sqltype.ID{
	sqltype.Oid, sqltype.Oid, sqltype.Int4, sqltype.Text, sqltype.Text, sqltype.Text, sqltype.Text, sqltype.Bool, 32,
}

// EventTriggerDDLCommands retains only the selected declaration and result
// shape. Each opening borrows the executing command's lexical collection state.
type EventTriggerDDLCommands struct {
	expr.Info
}

// EventTriggerDDLCommandsRuntime returns detached typed fields, not catalog or
// command authority. Repeated invocations do not consume the command's records.
type EventTriggerDDLCommandsRuntime interface {
	EventTriggerDDLCommands() ([][]sqltype.Value, error)
}

func EventTriggerDDLCommandsContextError() error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "39P03", Message: "pg_event_trigger_ddl_commands() can only be called in an event trigger function"}
}

func bindEventTriggerDDLCommands(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	names := make([]string, len(raw.Args))
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		name := "unknown"
		if arg.Usage.Type != nil {
			name = b.DiagnosticTypeName(arg.Usage.Type)
		}
		names[i] = NamedArgumentType(raw, i, name)
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	if !visible || len(raw.Args) != 0 || len(raw.ArgNames) != 0 || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", eventTriggerDDLCommandsName+"(*) specified, but "+eventTriggerDDLCommandsName+" is not an aggregate function", raw.Pos)
	}
	if err := b.AdmitSetReturning(raw.Pos); err != nil {
		return nil, err
	}
	members := make([]sqltype.CompositeMember, len(eventTriggerDDLCommandsNames))
	for i, name := range eventTriggerDDLCommandsNames {
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: name, Usage: b.Type(eventTriggerDDLCommandsTypes[i]).Usage()}
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		return nil, err
	}
	info, err := b.BuiltinRoutineInfo(b.Info(shape.Usage(), raw.Pos, expr.Properties{Volatility: expr.Stable, StageDependent: true}), CatalogRoutineProvider, eventTriggerDDLCommandsName)
	if err != nil {
		return nil, err
	}
	return EventTriggerDDLCommands{info}, nil
}

func (n EventTriggerDDLCommands) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	evaluation, err := evaluationContext(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if err := n.Validate(); err != nil {
		return expr.SourceCursor{}, err
	}
	runtime, ok := ctx.(EventTriggerDDLCommandsRuntime)
	if !ok {
		return expr.SourceCursor{}, EventTriggerDDLCommandsContextError()
	}
	rows, err := runtime.EventTriggerDDLCommands()
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if err := evaluation.Err(); err != nil {
		return expr.SourceCursor{}, err
	}
	values := make([]sqltype.Value, len(rows))
	for i, fields := range rows {
		if err := evaluation.Err(); err != nil {
			return expr.SourceCursor{}, err
		}
		values[i], err = sqltype.OwnedCompositeValue(n.Usage.Type, fields)
		if err != nil {
			return expr.SourceCursor{}, err
		}
	}
	if err := evaluation.Err(); err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.OwnedSource(n.Usage.Type, values), nil
}

func (n EventTriggerDDLCommands) Validate() error {
	if n.RoutineReference == nil || n.RoutineReference.RoutineOID() != 4568 {
		return fmt.Errorf("stuntgres: invalid DDL commands routine declaration")
	}
	if n.Usage.Type == nil || n.Usage.Type.ID() != 2249 || !n.Usage.Type.IsComposite() || n.Usage.Type.CompositeLen() != len(eventTriggerDDLCommandsNames) {
		return fmt.Errorf("stuntgres: invalid DDL commands result shape")
	}
	for i, name := range eventTriggerDDLCommandsNames {
		member := n.Usage.Type.CompositeMember(i)
		if member.Attribute != int32(i+1) || member.Name != name || member.Usage.Type == nil || member.Usage.Type.ID() != eventTriggerDDLCommandsTypes[i] {
			return fmt.Errorf("stuntgres: invalid DDL commands result member")
		}
	}
	return nil
}

func (EventTriggerDDLCommands) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: DDL commands requires a source invocation")
}
