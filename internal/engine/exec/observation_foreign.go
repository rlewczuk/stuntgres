package exec

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
)

// ForeignObservation contains detached presentation only, never provider plans,
// credentials or live scan ownership. Composite keys are the replaced real node.
type ForeignObservation struct {
	Composite  bool
	Details    []string
	Operation  string
	Sources    []ForeignObservedSource
	Estimates  map[string]float64
	Properties []catalog.ForeignExplainProperty
}

type ForeignObservedSource struct {
	Occurrence int    `json:"Occurrence"`
	Schema     string `json:"Schema"`
	Relation   string `json:"Relation Name"`
}

func (o *Observation) Foreign(node plan.Node) (ForeignObservation, bool) {
	if o == nil {
		return ForeignObservation{}, false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	value, ok := o.foreign[node]
	value.Details = slices.Clone(value.Details)
	value.Sources = slices.Clone(value.Sources)
	value.Properties = slices.Clone(value.Properties)
	value.Estimates = maps.Clone(value.Estimates)
	return value, ok
}

func (o *Observation) recordForeign(node plan.Node, value ForeignObservation, properties []catalog.ForeignExplainProperty) error {
	for _, property := range properties {
		if strings.TrimSpace(property.Name) == "" || strings.ContainsAny(property.Name, "\r\n:") || strings.ContainsAny(property.Value, "\r\n") {
			return fmt.Errorf("stuntgres: invalid foreign EXPLAIN property")
		}
		value.Details = append(value.Details, property.Name+": "+property.Value)
	}
	if o.foreignJSON {
		value.Properties = slices.Clone(properties)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.foreign == nil {
		o.foreign = make(map[plan.Node]ForeignObservation)
	}
	o.foreign[node] = value
	return nil
}

// Configured before stage startup; TEXT needs no structured presentation copies.
func (o *Observation) SetForeignOptions(options catalog.ForeignExplainOptions, structured bool) {
	o.foreignOptions, o.foreignJSON = options, structured
}

func observeForeignScan(ctx context.Context, o *Observation, node plan.Node, source *ForeignQuerySource, request catalog.ForeignRequest, execution *ForeignExecution) error {
	if o == nil || source.Query != nil {
		return nil
	}
	value := ForeignObservation{}
	if o.foreignOptions.Costs {
		value.Details = append(value.Details, fmt.Sprintf("Provider estimates: startup cost=%.2f total cost=%.2f rows=%.0f width=%.0f", execution.Path.StartupCost, execution.Path.TotalCost, execution.Size.Rows, execution.Size.Width))
		if o.foreignJSON {
			value.Estimates = map[string]float64{"Startup Cost": execution.Path.StartupCost, "Total Cost": execution.Path.TotalCost, "Plan Rows": execution.Size.Rows, "Plan Width": execution.Size.Width}
		}
	}
	var properties []catalog.ForeignExplainProperty
	if provider, ok := source.Wrapper.(catalog.ForeignExplainScanProvider); ok {
		path, planned := execution.Path, execution.Plan
		path.Attributes, planned.Attributes = slices.Clone(path.Attributes), slices.Clone(planned.Attributes)
		var err error
		properties, err = provider.ExplainForeignScan(ctx, catalog.ForeignExplainScanRequest{ForeignRequest: CopyForeignRequest(request), Size: execution.Size, Path: path, Plan: planned, Options: o.foreignOptions})
		if err != nil {
			return ForeignCallbackError(err)
		}
	}
	return o.recordForeign(node, value, properties)
}

func observeForeignQuery(ctx context.Context, o *Observation, node plan.Node, selected *ForeignQuerySelection) error {
	if o == nil {
		return nil
	}
	value := ForeignObservation{Composite: true, Details: []string{"Foreign operation: " + selected.Request.Stage.Kind}}
	if o.foreignJSON {
		value.Operation = selected.Request.Stage.Kind
	}
	for _, source := range selected.Request.Sources {
		value.Details = append(value.Details, fmt.Sprintf("Foreign source %d: %s.%s", source.Occurrence+1, catalog.Identifier(source.Request.Schema), catalog.Identifier(source.Request.Table)))
		if o.foreignJSON {
			value.Sources = append(value.Sources, ForeignObservedSource{Occurrence: source.Occurrence + 1, Schema: source.Request.Schema, Relation: source.Request.Table})
		}
	}
	if o.foreignOptions.Costs {
		value.Details = append(value.Details, fmt.Sprintf("Provider estimates: startup cost=%.2f total cost=%.2f", selected.Path.StartupCost, selected.Path.TotalCost))
		if o.foreignJSON {
			value.Estimates = map[string]float64{"Startup Cost": selected.Path.StartupCost, "Total Cost": selected.Path.TotalCost}
		}
	}
	var properties []catalog.ForeignExplainProperty
	if provider, ok := selected.Source.Wrapper.(catalog.ForeignExplainScanProvider); ok {
		path := selected.Path
		path.Plan.Slots = slices.Clone(path.Plan.Slots)
		var err error
		properties, err = provider.ExplainForeignScan(ctx, catalog.ForeignExplainScanRequest{ForeignRequest: CopyForeignRequest(selected.Source.Request), Query: &catalog.ForeignExplainQueryPlan{Request: CopyForeignQueryRequest(selected.Request), Path: path}, Options: o.foreignOptions})
		if err != nil {
			return ForeignCallbackError(err)
		}
	}
	return o.recordForeign(node, value, properties)
}

// ObserveForeignModification is called only after the executor has selected its
// actual modification plan. Nil is the command-local modification root key.
func ObserveForeignModification(ctx context.Context, wrapper catalog.ForeignDataWrapper, request catalog.ForeignModifyRequest) error {
	o, _ := ctx.Value(observationKey{}).(*Observation)
	if o == nil {
		return nil
	}
	var properties []catalog.ForeignExplainProperty
	if provider, ok := wrapper.(catalog.ForeignExplainModifyProvider); ok {
		var err error
		properties, err = provider.ExplainForeignModify(ctx, catalog.ForeignExplainModifyRequest{ForeignModifyRequest: request, Options: o.foreignOptions})
		if err != nil {
			return ForeignCallbackError(err)
		}
	}
	return o.recordForeign(nil, ForeignObservation{}, properties)
}
