package reference_test

import (
	"context"
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"

	"stuntgres"
)

func (r *fdwSQLReader) queryPaths(ctx context.Context, request stuntgres.ForeignQueryRequest, method string) ([]stuntgres.ForeignQueryPath, error) {
	if err := r.call(ctx, method); err != nil {
		return nil, err
	}
	r.mu.Lock()
	enabled := r.queryPushdown
	incompatible := r.queryIncompatible
	r.mu.Unlock()
	if !enabled {
		return nil, nil
	}
	return []stuntgres.ForeignQueryPath{{TotalCost: 1, Compatibility: stuntgres.ForeignQueryCompatibility{Expressions: !incompatible, Collations: true, StatementVisibility: true}, Plan: stuntgres.ForeignQueryPlan{Slots: append([]stuntgres.ForeignQuerySlot{}, request.Stage.Slots...)}}}, nil
}

func (r *fdwSQLReader) GetForeignJoinPaths(ctx context.Context, request stuntgres.ForeignQueryRequest) ([]stuntgres.ForeignQueryPath, error) {
	return r.queryPaths(ctx, request, "GetForeignJoinPaths")
}
func (r *fdwSQLReader) GetForeignUpperPaths(ctx context.Context, request stuntgres.ForeignQueryRequest) ([]stuntgres.ForeignQueryPath, error) {
	return r.queryPaths(ctx, request, "GetForeignUpperPaths")
}

func (r *fdwSQLReader) BeginForeignQueryScan(ctx context.Context, request stuntgres.ForeignQueryRequest, plan stuntgres.ForeignQueryPlan) (stuntgres.ForeignScan, error) {
	if r.advanced != nil {
		r.advanced.mu.Lock()
		r.advanced.executedPlans = append(r.advanced.executedPlans, plan)
		r.advanced.mu.Unlock()
	}
	query, _, err := fdwQuerySQL(request, request.Stage)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	observe := r.queryRequestHook
	r.mu.Unlock()
	if observe != nil {
		observe(request)
	}
	var witness func(stuntgres.ForeignQueryStage) error
	witness = func(stage stuntgres.ForeignQueryStage) error {
		if err := r.call(ctx, "ExecuteForeignQueryStage:"+stage.Kind); err != nil {
			return err
		}
		for _, child := range stage.Children {
			if err := witness(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := witness(request.Stage); err != nil {
		return nil, err
	}
	pool, err := r.pool(request.Sources[0].Request.MappingOptions)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.begins++
	r.parameters = append(r.parameters, request.Parameters)
	r.mu.Unlock()
	scan := &fdwSQLScan{reader: r, pool: pool, query: query, width: len(plan.Slots)}
	scan.queryForParameters = func(parameters stuntgres.ForeignScanParameters) (string, error) {
		request.Parameters = parameters
		query, _, err := fdwQuerySQL(request, request.Stage)
		return query, err
	}
	return scan, r.call(ctx, "BeginForeignQueryScan")
}

type fdwQueryOrdering struct {
	slot        int
	desc, first bool
}

func fdwQuerySQL(request stuntgres.ForeignQueryRequest, stage stuntgres.ForeignQueryStage) (string, []fdwQueryOrdering, error) {
	var refs []string
	var from string
	var inherited []fdwQueryOrdering
	if stage.Kind == "source" {
		var source *stuntgres.ForeignRequest
		for i := range request.Sources {
			if request.Sources[i].Occurrence == stage.Occurrence {
				source = &request.Sources[i].Request
				break
			}
		}
		if source == nil {
			return "", nil, fmt.Errorf("SQL reader: missing source occurrence")
		}
		from = fdwRemote(*source)
		for _, column := range source.Columns {
			name := fdwOption(column.Options, "column_name")
			if name == "" {
				name = column.Name
			}
			refs = append(refs, fdwQuote(name))
		}
	} else {
		var parts []string
		for i, child := range stage.Children {
			query, order, err := fdwQuerySQL(request, child)
			if err != nil {
				return "", nil, err
			}
			alias := "c" + strconv.Itoa(i)
			parts = append(parts, "("+query+") AS "+alias)
			for j := range child.Slots {
				refs = append(refs, alias+"."+fdwQuote("s"+strconv.Itoa(j)))
			}
			if i == 0 {
				inherited = order
			}
		}
		if stage.Kind == "join" {
			predicate := "TRUE"
			if stage.Predicate != nil {
				var err error
				predicate, err = fdwQueryExpression(request, *stage.Predicate, refs)
				if err != nil {
					return "", nil, err
				}
			}
			from = parts[0] + " " + stage.Join + " JOIN " + parts[1] + " ON " + predicate
			inherited = nil
		} else {
			from = parts[0]
		}
	}
	var outputs []string
	if stage.Kind == "aggregate" {
		for _, key := range stage.Keys {
			value, err := fdwQueryExpression(request, key, refs)
			if err != nil {
				return "", nil, err
			}
			outputs = append(outputs, value)
		}
		for _, call := range stage.Aggregates {
			var args []string
			for _, arg := range call.Arguments {
				value, err := fdwQueryExpression(request, arg, refs)
				if err != nil {
					return "", nil, err
				}
				args = append(args, value)
			}
			if len(args) == 0 {
				args = []string{"*"}
			}
			outputs = append(outputs, fdwQuote(call.Namespace)+"."+fdwQuote(call.Name)+"("+strings.Join(args, ",")+")")
		}
		inherited = nil
		// Aggregate output expressions address the key/call result frame,
		// not the child frame, and include hidden occurrence cells.
		refs, outputs = outputs, nil
	}
	for _, output := range stage.Outputs {
		value, err := fdwQueryExpression(request, output, refs)
		if err != nil {
			return "", nil, err
		}
		outputs = append(outputs, value)
	}
	if len(outputs) != len(stage.Slots) {
		return "", nil, fmt.Errorf("SQL reader: aggregate output layout mismatch")
	}
	for i := range outputs {
		outputs[i] += " AS " + fdwQuote("s"+strconv.Itoa(i))
	}
	projection := strings.Join(outputs, ",")
	if projection == "" {
		projection = "1"
	}
	query := "SELECT " + projection + " FROM " + from
	if stage.Kind == "filter" {
		predicate, err := fdwQueryExpression(request, *stage.Predicate, refs)
		if err != nil {
			return "", nil, err
		}
		query += " WHERE " + predicate
	}
	if stage.Kind == "aggregate" && len(stage.Keys) != 0 {
		var keys []string
		for i := range stage.Keys {
			keys = append(keys, strconv.Itoa(i+1))
		}
		query += " GROUP BY " + strings.Join(keys, ",")
	}
	var order []string
	if stage.Kind == "order" {
		inherited = nil
		for _, key := range stage.Order {
			value, err := fdwQueryExpression(request, key.Expression, refs)
			if err != nil {
				return "", nil, err
			}
			order = append(order, value+fdwQueryDirection(key.Descending, key.NullsFirst))
			inherited = append(inherited, fdwQueryOrdering{key.OutputSlot, key.Descending, key.NullsFirst})
		}
	} else if stage.Kind != "source" && stage.Kind != "join" && stage.Kind != "aggregate" {
		for _, key := range inherited {
			order = append(order, refs[key.slot]+fdwQueryDirection(key.desc, key.first))
		}
	}
	if len(order) != 0 {
		query += " ORDER BY " + strings.Join(order, ",")
	}
	if stage.Kind == "limit" {
		if stage.Count != nil {
			query += " LIMIT " + strconv.FormatInt(*stage.Count, 10)
		}
		if stage.Offset != 0 {
			query += " OFFSET " + strconv.FormatInt(stage.Offset, 10)
		}
	}
	return query, inherited, nil
}

func fdwQueryDirection(desc, first bool) string {
	direction := " ASC"
	if desc {
		direction = " DESC"
	}
	if first {
		return direction + " NULLS FIRST"
	}
	return direction + " NULLS LAST"
}

func fdwQueryLiteral(value driver.Value) (string, error) {
	switch v := value.(type) {
	case nil:
		return "NULL", nil
	case bool:
		if v {
			return "TRUE", nil
		}
		return "FALSE", nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case string:
		return "'" + strings.ReplaceAll(v, "'", "''") + "'", nil
	case []byte:
		return "'" + strings.ReplaceAll(string(v), "'", "''") + "'", nil
	}
	return "", fmt.Errorf("SQL reader: unsupported typed literal %T", value)
}

func fdwQueryExpression(request stuntgres.ForeignQueryRequest, e stuntgres.ForeignQueryExpression, refs []string) (string, error) {
	var args []string
	for _, arg := range e.Arguments {
		value, err := fdwQueryExpression(request, arg, refs)
		if err != nil {
			return "", err
		}
		args = append(args, value)
	}
	switch e.Kind {
	case "slot":
		if e.Slot < 0 || e.Slot >= len(refs) {
			return "", fmt.Errorf("SQL reader: invalid slot")
		}
		return refs[e.Slot], nil
	case "present":
		return "COALESCE(" + refs[e.Slot] + ",FALSE)", nil
	case "literal", "parameter":
		value := e.Literal
		if e.Kind == "parameter" {
			if e.Parameter < 1 || e.Parameter > len(request.Parameters.Values) {
				return "", fmt.Errorf("SQL reader: invalid parameter")
			}
			value = request.Parameters.Values[e.Parameter-1]
		}
		literal, err := fdwQueryLiteral(value)
		if err != nil {
			return "", err
		}
		return "(" + literal + ")::pg_catalog." + fdwQuote(e.Type.TypeName), nil
	case "comparison":
		return "(" + args[0] + " " + e.Operator + " " + args[1] + ")", nil
	case "and", "or":
		return "(" + args[0] + " " + strings.ToUpper(e.Kind) + " " + args[1] + ")", nil
	case "not":
		return "(NOT " + args[0] + ")", nil
	case "isnull":
		return "(" + args[0] + " IS NULL)", nil
	case "isnotnull":
		return "(" + args[0] + " IS NOT NULL)", nil
	case "cast":
		return "(" + args[0] + ")::pg_catalog." + fdwQuote(e.Type.TypeName), nil
	case "collate":
		return "(" + args[0] + " COLLATE \"C\")", nil
	}
	return "", fmt.Errorf("SQL reader: unsupported expression %q", e.Kind)
}
