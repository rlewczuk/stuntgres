package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

const eventTriggerDroppedName = "pg_event_trigger_dropped_objects"

var eventTriggerDroppedNames = [...]string{
	"classid", "objid", "objsubid", "original", "normal", "is_temporary",
	"object_type", "schema_name", "object_name", "object_identity", "address_names", "address_args",
}

var eventTriggerDroppedTypes = [...]sqltype.ID{
	sqltype.Oid, sqltype.Oid, sqltype.Int4, sqltype.Bool, sqltype.Bool, sqltype.Bool,
	sqltype.Text, sqltype.Text, sqltype.Text, sqltype.Text, 1009, 1009,
}

// EventTriggerDroppedObjects retains only the selected declaration and result
// shape. Each opening borrows the executing command's lexical sql_drop state.
type EventTriggerDroppedObjects struct {
	expr.Info
}

// EventTriggerDroppedRuntime returns detached typed fields, not catalog or
// command authority. Repeated invocations do not consume the command's records.
type EventTriggerDroppedRuntime interface {
	EventTriggerDroppedObjects() ([][]sqltype.Value, error)
}

func EventTriggerDroppedContextError() error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "39P03", Message: "pg_event_trigger_dropped_objects() can only be called in a sql_drop event trigger function"}
}

func bindEventTriggerDroppedObjects(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
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
		return nil, expr.ErrorAt("42809", eventTriggerDroppedName+"(*) specified, but "+eventTriggerDroppedName+" is not an aggregate function", raw.Pos)
	}
	if err := b.AdmitSetReturning(raw.Pos); err != nil {
		return nil, err
	}
	members := make([]sqltype.CompositeMember, len(eventTriggerDroppedNames))
	for i, name := range eventTriggerDroppedNames {
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: name, Usage: b.Type(eventTriggerDroppedTypes[i]).Usage()}
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		return nil, err
	}
	info, err := b.BuiltinRoutineInfo(b.Info(shape.Usage(), raw.Pos, expr.Properties{Volatility: expr.Stable, StageDependent: true}), CatalogRoutineProvider, eventTriggerDroppedName)
	if err != nil {
		return nil, err
	}
	return EventTriggerDroppedObjects{info}, nil
}

func (n EventTriggerDroppedObjects) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	evaluation, err := evaluationContext(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if err := n.Validate(); err != nil {
		return expr.SourceCursor{}, err
	}
	runtime, ok := ctx.(EventTriggerDroppedRuntime)
	if !ok {
		return expr.SourceCursor{}, EventTriggerDroppedContextError()
	}
	rows, err := runtime.EventTriggerDroppedObjects()
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

func (n EventTriggerDroppedObjects) Validate() error {
	if n.Usage.Type == nil || n.Usage.Type.ID() != 2249 || !n.Usage.Type.IsComposite() || n.Usage.Type.CompositeLen() != len(eventTriggerDroppedNames) {
		return fmt.Errorf("stuntgres: invalid dropped objects result shape")
	}
	for i, name := range eventTriggerDroppedNames {
		member := n.Usage.Type.CompositeMember(i)
		if member.Name != name || member.Usage.Type == nil || member.Usage.Type.ID() != eventTriggerDroppedTypes[i] {
			return fmt.Errorf("stuntgres: invalid dropped objects result member")
		}
	}
	return nil
}

func (EventTriggerDroppedObjects) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: dropped objects requires a source invocation")
}
