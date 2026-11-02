package explain

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

// Properties are detached at construction, never recovered from display text.
func (r *Renderer) property(tree *Tree, name string, value any) {
	if !r.options.Structured() {
		return
	}
	if tree.properties == nil {
		tree.properties = make(map[string]any)
	}
	tree.properties[name] = value
}

func (r *Renderer) repeated(tree *Tree, name string, value any) {
	if !r.options.Structured() {
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

// All formats apply actual provider selection to the same frozen tree.
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
	if r.options.Structured() {
		presentation.properties = maps.Clone(tree.properties)
	}
	if foreign.Composite {
		presentation.label, presentation.nodeType = "Foreign Scan", "Foreign Scan"
		presentation.children, presentation.details = nil, foreign.Details
		presentation.unknownEstimate = true
		presentation.properties = nil
	}
	if r.options.Structured() {
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

func (r *Renderer) structuredNode(tree *Tree) map[string]any {
	tree = r.effective(tree)
	object := maps.Clone(tree.properties)
	if object == nil {
		object = make(map[string]any)
	}
	object["Node Type"] = tree.nodeType
	if tree.debug != nil {
		object["Embedded Debug"] = tree.debug
	}
	if tree.commandDebug != nil {
		object["Embedded Command Debug"] = tree.commandDebug
	}
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
			children[i] = r.structuredNode(child)
		}
		object["Plans"] = children
	}
	return object
}

type Summaries struct{ Planning, Execution *time.Duration }

// Structured documents enter the ordinary descriptor codec and publication path.
// Output publishes the presentation as QUERY PLAN rows of schema.
func Output(ctx context.Context, schema []catalog.Column, options Options, renderers []*Renderer, lines []string, summaries Summaries) ([][]sqltype.Value, error) {
	if options.Structured() {
		document := make([]map[string]any, 0)
		for _, renderer := range renderers {
			for i, tree := range renderer.trees {
				entry := map[string]any{"Plan": renderer.structuredNode(tree)}
				if i == 0 && renderer.rangeTable != nil {
					entry["Embedded Range Table"] = renderer.rangeTable
				}
				document = append(document, entry)
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
		encoded, err := encodeStructured(options.Format, document)
		if err != nil {
			return nil, err
		}
		value, err := schema[0].Usage.Type.Input(ctx, string(encoded), -1)
		if err != nil {
			return nil, err
		}
		return [][]sqltype.Value{{value}}, nil
	}
	// Logical renderer lines can contain accepted identifier newlines. Only
	// this publication boundary turns LF-delimited physical lines into rows.
	count := len(lines)
	for _, line := range lines {
		count += strings.Count(line, "\n")
	}
	if summaries.Planning != nil {
		count++
	}
	if summaries.Execution != nil {
		count++
	}
	rows := make([][]sqltype.Value, count)
	typ := schema[0].Usage.Type
	next := 0
	for _, line := range lines {
		for {
			part, rest, more := strings.Cut(line, "\n")
			rows[next] = []sqltype.Value{sqltype.String(typ, part)}
			next++
			if !more {
				break
			}
			line = rest
		}
	}
	// Do not append summaries to the caller's potentially shared line backing.
	if summaries.Planning != nil {
		rows[next] = []sqltype.Value{sqltype.String(typ, fmt.Sprintf("Planning Time: %.3f ms", float64(*summaries.Planning)/1e6))}
		next++
	}
	if summaries.Execution != nil {
		rows[next] = []sqltype.Value{sqltype.String(typ, fmt.Sprintf("Execution Time: %.3f ms", float64(*summaries.Execution)/1e6))}
	}
	return rows, nil
}
