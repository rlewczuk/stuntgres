package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Completion follows ordinary call/signature/SRF analysis, but precedes source
// schema expansion. Explicit definitions are occurrence authorities, not aliases.
func (b *expressionBinder) completeFunctionDefinitions(raw parser.FunctionExpr, call expr.Expr, definitions []parser.FunctionColumnDefinition) (expr.Expr, error) {
	record, isRecord := call.(functions.JSONRecord)
	jsonb, isJSONB := call.(functions.JSONBRecord)
	anonymous := (isRecord || isJSONB) && call.ResultUsage().Type.ID() == 2249
	if len(definitions) == 0 {
		if isRecord && anonymous || jsonbRecordUnresolved(call) {
			return nil, expr.ErrorAt("42601", `a column definition list is required for functions returning "record"`, raw.Pos)
		}
		return call, nil
	}
	if !anonymous {
		return nil, expr.ErrorAt("42601", analyze.FunctionColumnDefinitionMessage(call), definitions[0].Name.Pos)
	}
	if isJSONB {
		usage, err := b.bindJSONBRecordDefinition(definitions)
		if err != nil {
			return nil, err
		}
		info := jsonb.Info
		info.Usage = usage
		info.Property.DomainDependent = info.Property.DomainDependent || sqltype.ContainsDomainConstraints(usage.Type)
		jsonb.Definitions, jsonb.Unresolved = true, false
		return jsonb.Reconstruct(info, jsonb.Args)
	}
	members := make([]sqltype.CompositeMember, len(definitions))
	seen := make(map[string]bool, len(definitions))
	for i, definition := range definitions {
		if seen[definition.Name.Text] {
			return nil, sqlError("42701", `column name "`+definition.Name.Text+`" specified more than once`)
		}
		seen[definition.Name.Text] = true
		u, err := b.resolveFunctionColumnUsage(definition, true)
		if err != nil {
			return nil, err
		}
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: definition.Name.Text, Usage: u}
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		return nil, err
	}
	info := record.Info
	info.Usage = shape.Usage()
	info.Property.DomainDependent = info.Property.DomainDependent || sqltype.ContainsDomainConstraints(shape)
	info.Property.RuntimeDomainConversion = info.Property.RuntimeDomainConversion || sqltype.ContainsDomainConstraints(shape)
	record.Defined = true
	return record.Reconstruct(info, record.Args)
}

// Both record providers resolve members through the same captured input
// authority. Duplicate-name ordering remains with each provider's member loop.
func (b *expressionBinder) resolveFunctionColumnUsage(definition parser.FunctionColumnDefinition, unknownNull bool) (sqltype.Usage, error) {
	u, err := b.host.ResolveAnnotationType(definition.Type, unknownNull)
	if err != nil {
		return sqltype.Usage{}, err
	}
	u, err = b.host.ResolveColumnCollation(u, definition.Collation)
	if err != nil {
		return sqltype.Usage{}, err
	}
	if err := b.CaptureTypeInput(u.Type); err != nil {
		return sqltype.Usage{}, err
	}
	return u, nil
}

func functionDefinitionError(message, hint string, pos parser.Position) error {
	err := expr.ErrorAt("42601", message, pos).(*sqlstate.Error)
	err.Hint = hint
	return err
}

func (b *expressionBinder) bindJSONBRecordDefinition(raw []parser.FunctionColumnDefinition) (sqltype.Usage, error) {
	if len(raw) > 1600 {
		return sqltype.Usage{}, expr.ErrorAt("54011", "column definition lists can have at most 1600 entries", raw[0].Name.Pos)
	}
	members := make([]sqltype.CompositeMember, len(raw))
	names := make(map[string]bool, len(raw))
	duplicate := ""
	for i, definition := range raw {
		if names[definition.Name.Text] && duplicate == "" {
			duplicate = definition.Name.Text
		}
		names[definition.Name.Text] = true
		u, err := b.resolveFunctionColumnUsage(definition, false)
		if err != nil {
			return sqltype.Usage{}, err
		}
		// Definition-only members remain dependencies even for empty/NULL calls
		// and when their output is not projected by the surrounding query.
		b.info(u, definition.Name.Pos, expr.Properties{})
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: definition.Name.Text, Usage: u}
	}
	if duplicate != "" {
		return sqltype.Usage{}, sqlError("42701", "column name \""+duplicate+"\" specified more than once")
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		return sqltype.Usage{}, err
	}
	return shape.Usage(), nil
}

func jsonbRecordUnresolved(expr expr.Expr) bool {
	call, ok := expr.(functions.JSONBRecord)
	return ok && call.Unresolved && call.ResultUsage().Type.ID() == 2249
}
