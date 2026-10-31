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
func (r *Renderer) property(tree *Tree, name string, value any) {
	if r.options.Format != "json" {
		return
	}
	if tree.properties == nil {
		tree.properties = make(map[string]any)
	}
	tree.properties[name] = value
}

func (r *Renderer) repeated(tree *Tree, name string, value any) {
	if r.options.Format != "json" {
		return
	}
	if tree.properties == nil {
		tree.properties = make(map[string]any)
	}
	values, _ := tree.properties[name].([]any)
	tree.properties[name] = append(values, value)
}

func (r *Renderer) relationProperties(tree *Tree, relation *catalog.Relation, alias string) {
	r.property(tree, "Relation Name", relation.Name())
	if alias == "" {
		alias = relation.Name()
	}
	r.property(tree, "Alias", alias)
	if r.options.Verbose {
		if namespace, ok := r.auth.Catalog.Namespace(relation.Namespace()); ok {
			r.property(tree, "Schema", namespace.Name())
		}
	}
}

// Both encodings apply actual provider selection to the same frozen tree.
func (r *Renderer) effective(tree *Tree) *Tree {
	if r.Observation == nil || tree.node == nil && tree.Actual == nil {
		return tree
	}
	foreign, ok := r.Observation.Foreign(tree.node)
	if !ok {
		return tree
	}
	presentation := *tree
	presentation.details = append(append([]string(nil), tree.details...), foreign.Details...)
	if r.options.Format == "json" {
		presentation.properties = maps.Clone(tree.properties)
	}
	if foreign.Composite {
		presentation.label, presentation.nodeType = "Foreign Scan", "Foreign Scan"
		presentation.children, presentation.details = nil, foreign.Details
		presentation.unknownEstimate = true
		presentation.properties = nil
	}
	if r.options.Format == "json" {
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

func (r *Renderer) jsonNode(tree *Tree) map[string]any {
	tree = r.effective(tree)
	object := maps.Clone(tree.properties)
	if object == nil {
		object = make(map[string]any)
	}
	object["Node Type"] = tree.nodeType
	if r.options.Costs && !tree.unknownEstimate {
		object["Plan Rows"], object["Logical Cost"] = tree.estimate.RoundedRows(), tree.estimate.Cost
		object["Estimate Model"] = "logical heuristics; not execution measurements"
	}
	if r.Observation != nil && (r.Observation.Known(tree.node) || tree.Actual != nil) {
		actual := exec.Actual{}
		if tree.Actual != nil {
			actual = *tree.Actual
		} else {
			actual = r.Observation.Actual(tree.node)
		}
		rows := float64(0)
		if actual.Loops != 0 {
			rows = float64(actual.Rows) / float64(actual.Loops)
		}
		object["Actual Rows"], object["Actual Loops"] = rows, actual.Loops
		if r.options.Timing && actual.Loops != 0 {
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

type Summaries struct{ Planning, Execution *time.Duration }

// JSON enters the ordinary descriptor codec and result publication path.
func Output(scope *StatementScope, schema []catalog.Column, options Options, renderers []*Renderer, lines []string, summaries Summaries) ([][]sqltype.Value, error) {
	if options.Format == "json" {
		document := make([]map[string]any, 0)
		for _, renderer := range renderers {
			for _, tree := range renderer.trees {
				document = append(document, map[string]any{"Plan": renderer.jsonNode(tree)})
			}
		}
		if len(document) == 0 {
			document = append(document, map[string]any{"Plan": map[string]any{"Node Type": "Rewrite", "Action": "nothing"}})
		}
		if options.Analyze && len(renderers) != 0 {
			document[0]["Measurement Model"] = "emitted rows and active elapsed milliseconds per loop; elapsed includes child work, sums parallel worker plus leader work, excludes consumer idle time and transaction publication"
		}
		if summaries.Planning != nil {
			document[0]["Planning Time"] = float64(*summaries.Planning) / 1e6
		}
		if summaries.Execution != nil {
			document[0]["Execution Time"] = float64(*summaries.Execution) / 1e6
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
	if summaries.Planning != nil {
		lines = append(lines, fmt.Sprintf("Planning Time: %.3f ms", float64(*summaries.Planning)/1e6))
	}
	if summaries.Execution != nil {
		lines = append(lines, fmt.Sprintf("Execution Time: %.3f ms", float64(*summaries.Execution)/1e6))
	}
	rows := make([][]sqltype.Value, len(lines))
	for i, line := range lines {
		rows[i] = []sqltype.Value{sqltype.String(schema[0].Usage.Type, line)}
	}
	return rows, nil
}
