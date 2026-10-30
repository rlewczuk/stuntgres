package reference_test

import (
	"context"
	"database/sql/driver"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"

	"stuntgres"
)

type fdwAdvancedState struct {
	mu                sync.Mutex
	limit             int
	sample            stuntgres.ForeignSample
	requests          []stuntgres.ForeignRequest
	scanRequests      []stuntgres.ForeignRequest
	queryPlans        []stuntgres.ForeignExplainQueryPlan
	executedPlans     []stuntgres.ForeignQueryPlan
	executedRequests  []stuntgres.ForeignQueryRequest
	basePlans         []stuntgres.ForeignExplainScanRequest
	baseExecutions    []fdwExplainBaseExecution
	explainProperties []stuntgres.ForeignExplainProperty
}

type fdwExplainBaseExecution struct {
	request stuntgres.ForeignRequest
	plan    stuntgres.ForeignPlan
}

func fdwProjection(request stuntgres.ForeignRequest, attributes []int32) string {
	var columns []string
	for _, attribute := range attributes {
		for _, column := range request.Columns {
			if column.Attribute != attribute {
				continue
			}
			name := fdwOption(column.Options, "column_name")
			if name == "" {
				name = column.Name
			}
			columns = append(columns, fdwQuote(name))
		}
	}
	if len(columns) == 0 {
		return "1"
	}
	return strings.Join(columns, ", ")
}

func (r *fdwSQLReader) ExplainForeignScan(ctx context.Context, request stuntgres.ForeignExplainScanRequest) ([]stuntgres.ForeignExplainProperty, error) {
	if err := r.call(ctx, "ExplainForeignScan"); err != nil {
		return nil, err
	}
	if r.advanced == nil || !request.Options.Verbose {
		return nil, nil
	}
	r.advanced.mu.Lock()
	r.advanced.scanRequests = append(r.advanced.scanRequests, request.ForeignRequest)
	if request.Query == nil {
		r.advanced.basePlans = append(r.advanced.basePlans, request)
	}
	r.advanced.mu.Unlock()
	r.advanced.mu.Lock()
	extra := append([]stuntgres.ForeignExplainProperty(nil), r.advanced.explainProperties...)
	r.advanced.mu.Unlock()
	if request.Query != nil {
		query := request.Query
		r.advanced.mu.Lock()
		r.advanced.queryPlans = append(r.advanced.queryPlans, *query)
		r.advanced.mu.Unlock()
		if len(query.Path.Plan.Slots) != len(query.Request.Stage.Slots) {
			return nil, fmt.Errorf("SQL reader: mismatched composite layout")
		}
		sql, _, err := fdwQuerySQL(query.Request, query.Request.Stage)
		if err != nil {
			return nil, err
		}
		properties := []stuntgres.ForeignExplainProperty{{Name: "Remote SQL", Value: sql}}
		properties = append(properties, extra...)
		return properties, r.call(ctx, "ExplainForeignScanResult")
	}
	remote, ok := request.Plan.Private.(string)
	if !ok {
		return nil, fmt.Errorf("SQL reader: missing actual planned source")
	}
	properties := []stuntgres.ForeignExplainProperty{{Name: "Remote SQL", Value: "SELECT " + fdwProjection(request.ForeignRequest, request.Plan.Attributes) + " FROM " + remote}}
	properties = append(properties, extra...)
	if request.RowMark != nil {
		properties = append(properties, stuntgres.ForeignExplainProperty{Name: "Row mark", Value: fmt.Sprint(request.RowMark.Type)})
		if request.Identity != nil {
			properties = append(properties, stuntgres.ForeignExplainProperty{Name: "Row identity", Value: strings.Join(request.Identity.Names, ", ")})
		}
	}
	return properties, r.call(ctx, "ExplainForeignScanResult")
}

func (w *fdwSQLWriter) ExplainForeignModify(ctx context.Context, request stuntgres.ForeignExplainModifyRequest) ([]stuntgres.ForeignExplainProperty, error) {
	if err := w.writeCall(ctx, "ExplainForeignModify"); err != nil {
		return nil, err
	}
	if w.advanced == nil || !request.Options.Verbose {
		return nil, nil
	}
	operation, ok := request.Plan.Private.(stuntgres.ForeignModifyOperation)
	if !ok || operation != request.Operation {
		return nil, fmt.Errorf("SQL writer: missing actual modification plan")
	}
	// The plan's operation and target are the same ones used by ExecForeign*.
	verb := "INSERT INTO"
	if operation == stuntgres.ForeignUpdate {
		verb = "UPDATE"
	}
	if operation == stuntgres.ForeignDelete {
		verb = "DELETE FROM"
	}
	return []stuntgres.ForeignExplainProperty{{Name: "Remote operation", Value: verb + " " + fdwRemote(request.ForeignRequest)}}, nil
}

func (r *fdwSQLReader) AnalyzeForeignTable(ctx context.Context, request stuntgres.ForeignRequest) (stuntgres.ForeignAnalyzeInfo, bool, error) {
	if err := r.call(ctx, "AnalyzeForeignTable"); err != nil {
		return stuntgres.ForeignAnalyzeInfo{}, false, err
	}
	if r.advanced == nil {
		return stuntgres.ForeignAnalyzeInfo{}, false, nil
	}
	return stuntgres.ForeignAnalyzeInfo{AcquireSampleRows: r.acquireForeignSample}, true, nil
}

// Reservoir sampling reads genuine source tuples once and closes rows and pool
// before returning. Population is counted independently of retained sample size.
func (r *fdwSQLReader) acquireForeignSample(ctx context.Context, request stuntgres.ForeignRequest, target int) (sample stuntgres.ForeignSample, err error) {
	if err = r.call(ctx, "AcquireSampleRows"); err != nil {
		return sample, err
	}
	pool, err := r.pool(request.MappingOptions)
	if err != nil {
		return sample, err
	}
	defer pool.Close()
	rows, err := pool.QueryContext(ctx, "SELECT "+fdwProjection(request, request.RequiredAttributes)+" FROM "+fdwRemote(request))
	if err != nil {
		return sample, err
	}
	defer rows.Close()
	r.advanced.mu.Lock()
	limit := r.advanced.limit
	r.advanced.mu.Unlock()
	if limit > 0 {
		target = min(target, limit)
	}
	for rows.Next() {
		values := make([]any, len(request.RequiredAttributes))
		pointers := make([]any, len(values))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err = rows.Scan(pointers...); err != nil {
			return sample, err
		}
		row := make(stuntgres.ForeignRow, len(values))
		for i, value := range values {
			if bytes, ok := value.([]byte); ok {
				value = string(bytes)
			}
			row[i], err = driver.DefaultParameterConverter.ConvertValue(value)
			if err != nil {
				return sample, err
			}
		}
		r.mu.Lock()
		transform := r.rowTransform
		r.mu.Unlock()
		if transform != nil {
			row, err = transform(ctx, row)
			if err != nil {
				return sample, err
			}
		}
		sample.TotalRows++
		if len(sample.Rows) < target {
			sample.Rows = append(sample.Rows, row)
		} else if index := rand.Int64N(int64(sample.TotalRows)); index < int64(target) {
			sample.Rows[index] = row
		}
	}
	if err = rows.Err(); err != nil {
		return sample, err
	}
	if err = ctx.Err(); err != nil {
		return sample, err
	}
	r.advanced.mu.Lock()
	r.advanced.sample = sample
	r.advanced.requests = append(r.advanced.requests, request)
	r.advanced.mu.Unlock()
	return sample, nil
}
