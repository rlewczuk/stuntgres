package core

import (
	"encoding/json"
	"fmt"
	"maps"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

// Properties are detached at construction, never recovered from display text.
func (r *explainRenderer) property(tree *explainTree, name string, value any) {
	if r.options.format != "json" {
		return
	}
	if tree.properties == nil {
		tree.properties = make(map[string]any)
	}
	tree.properties[name] = value
}

func (r *explainRenderer) repeated(tree *explainTree, name string, value any) {
	if r.options.format != "json" {
		return
	}
	if tree.properties == nil {
		tree.properties = make(map[string]any)
	}
	values, _ := tree.properties[name].([]any)
	tree.properties[name] = append(values, value)
}

func (r *explainRenderer) relationProperties(tree *explainTree, relation *catalog.Relation, alias string) {
	r.property(tree, "Relation Name", relation.Name())
	if alias == "" {
		alias = relation.Name()
	}
	r.property(tree, "Alias", alias)
	if r.options.verbose {
		if namespace, ok := r.auth.Catalog.Namespace(relation.Namespace()); ok {
			r.property(tree, "Schema", namespace.Name())
		}
	}
}

// Both encodings apply actual provider selection to the same frozen tree.
func (r *explainRenderer) effective(tree *explainTree) *explainTree {
	if r.observation == nil || tree.node == nil && tree.actual == nil {
		return tree
	}
	foreign, ok := r.observation.Foreign(tree.node)
	if !ok {
		return tree
	}
	presentation := *tree
	presentation.details = append(append([]string(nil), tree.details...), foreign.Details...)
	if r.options.format == "json" {
		presentation.properties = maps.Clone(tree.properties)
	}
	if foreign.Composite {
		presentation.label, presentation.nodeType = "Foreign Scan", "Foreign Scan"
		presentation.children, presentation.details = nil, foreign.Details
		presentation.unknownEstimate = true
		presentation.properties = nil
	}
	if r.options.format == "json" {
		if foreign.Operation != "" {
			r.property(&presentation, "Foreign Operation", foreign.Operation)
		}
		if len(foreign.Sources) != 0 {
			r.property(&presentation, "Foreign Sources", foreign.Sources)
		}
		if foreign.Estimates != nil {
			r.property(&presentation, "Provider Estimates", foreign.Estimates)
		}
		for _, property := range foreign.Properties {
			r.repeated(&presentation, "Provider Properties", map[string]any{"Name": property.Name, "Value": property.Value})
		}
	}
	return &presentation
}

func (r *explainRenderer) jsonNode(tree *explainTree) map[string]any {
	tree = r.effective(tree)
	object := maps.Clone(tree.properties)
	if object == nil {
		object = make(map[string]any)
	}
	object["Node Type"] = tree.nodeType
	if r.options.costs && !tree.unknownEstimate {
		object["Plan Rows"], object["Logical Cost"] = tree.estimate.RoundedRows(), tree.estimate.Cost
		object["Estimate Model"] = "logical heuristics; not execution measurements"
	}
	if r.observation != nil && (r.observation.Known(tree.node) || tree.actual != nil) {
		actual := exec.Actual{}
		if tree.actual != nil {
			actual = *tree.actual
		} else {
			actual = r.observation.Actual(tree.node)
		}
		rows := float64(0)
		if actual.Loops != 0 {
			rows = float64(actual.Rows) / float64(actual.Loops)
		}
		object["Actual Rows"], object["Actual Loops"] = rows, actual.Loops
		if r.options.timing && actual.Loops != 0 {
			object["Actual Total Time"] = float64(actual.Elapsed) / 1e6 / float64(actual.Loops)
		}
	}
	if len(tree.children) != 0 {
		children := make([]map[string]any, len(tree.children))
		for i, child := range tree.children {
			children[i] = r.jsonNode(child)
		}
		object["Plans"] = children
	}
	return object
}

type explainSummaries struct{ planning, execution *time.Duration }

// JSON enters the ordinary descriptor codec and result publication path.
func explainOutput(scope *StatementScope, schema []catalog.Column, options explainOptions, renderers []*explainRenderer, lines []string, summaries explainSummaries) ([][]sqltype.Value, error) {
	if options.format == "json" {
		document := make([]map[string]any, 0)
		for _, renderer := range renderers {
			for _, tree := range renderer.trees {
				document = append(document, map[string]any{"Plan": renderer.jsonNode(tree)})
			}
		}
		if len(document) == 0 {
			document = append(document, map[string]any{"Plan": map[string]any{"Node Type": "Rewrite", "Action": "nothing"}})
		}
		if options.analyze && len(renderers) != 0 {
			document[0]["Measurement Model"] = "emitted rows and active elapsed milliseconds per loop; elapsed includes child work, sums parallel worker plus leader work, excludes consumer idle time and transaction publication"
		}
		if summaries.planning != nil {
			document[0]["Planning Time"] = float64(*summaries.planning) / 1e6
		}
		if summaries.execution != nil {
			document[0]["Execution Time"] = float64(*summaries.execution) / 1e6
		}
		encoded, err := json.Marshal(document)
		if err != nil {
			return nil, err
		}
		value, err := schema[0].Usage.Type.Input(scope.executionContext(), string(encoded), -1)
		if err != nil {
			return nil, err
		}
		return [][]sqltype.Value{{value}}, nil
	}
	if summaries.planning != nil {
		lines = append(lines, fmt.Sprintf("Planning Time: %.3f ms", float64(*summaries.planning)/1e6))
	}
	if summaries.execution != nil {
		lines = append(lines, fmt.Sprintf("Execution Time: %.3f ms", float64(*summaries.execution)/1e6))
	}
	rows := make([][]sqltype.Value, len(lines))
	for i, line := range lines {
		rows[i] = []sqltype.Value{sqltype.String(schema[0].Usage.Type, line)}
	}
	return rows, nil
}
