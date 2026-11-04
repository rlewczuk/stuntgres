package explain

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"stuntgres/internal/engine/exec"
)

const logicalMetricModel = "Logical Tuple Accesses: demanded base-source tuples before filtering, excluding intermediate and cached replay tuples; Logical Tuple Writes: accepted staged row-version operations, not committed changes; no physical cache, page or WAL measurements"

// LogOutput consumes the same typed trees as SQL EXPLAIN, without publishing
// SQL result rows or invoking execution. JSON logs have an object envelope.
func LogOutput(options Options, renderers []*Renderer, query, parameters string, elapsed time.Duration, observation *exec.Observation, buffers, wal, triggers bool) (string, error) {
	if err := ResolveCommands(renderers); err != nil {
		return "", err
	}
	prefix := fmt.Sprintf("duration: %.3f ms  plan:\n", float64(elapsed)/1e6)
	accesses, writes := observation.LogicalMetrics()
	if !options.Structured() {
		lines := []string{"Query Text: " + query}
		if parameters != "" {
			lines = append(lines, "Query Parameters: "+parameters)
		}
		for _, renderer := range renderers {
			lines = append(lines, renderer.Render()...)
		}
		if buffers || wal {
			lines = append(lines, "Metric Model: "+logicalMetricModel)
		}
		if buffers {
			lines = append(lines, fmt.Sprintf("Logical Tuple Accesses: %d", accesses))
		}
		if wal {
			lines = append(lines, fmt.Sprintf("Logical Tuple Writes: %d", writes))
		}
		if triggers {
			for _, trigger := range observation.Triggers() {
				line := fmt.Sprintf("Trigger %s on %s: calls=%d", trigger.Name, trigger.Relation, trigger.Calls)
				if options.Timing {
					line += fmt.Sprintf(" time=%.3f ms", float64(trigger.Elapsed)/1e6)
				}
				lines = append(lines, line)
			}
		}
		return prefix + strings.Join(lines, "\n"), nil
	}
	document := map[string]any{"Query Text": query}
	if parameters != "" {
		document["Query Parameters"] = parameters
	}
	var plans []map[string]any
	for _, renderer := range renderers {
		for _, tree := range renderer.trees {
			plans = append(plans, renderer.structuredNode(tree))
		}
	}
	if len(plans) == 0 {
		return "", fmt.Errorf("stuntgres: automatic EXPLAIN has no executed plan")
	}
	document["Plan"] = plans[0]
	if len(plans) > 1 {
		document["Rewritten Plans"] = plans[1:]
	}
	if buffers || wal {
		document["Metric Model"] = logicalMetricModel
	}
	if buffers {
		document["Logical Tuple Accesses"] = accesses
	}
	if wal {
		document["Logical Tuple Writes"] = writes
	}
	if triggers {
		var entries []map[string]any
		for _, trigger := range observation.Triggers() {
			entry := map[string]any{"Trigger Name": trigger.Name, "Relation": trigger.Relation, "Calls": trigger.Calls}
			if options.Timing {
				entry["Time"] = float64(trigger.Elapsed) / 1e6
			}
			entries = append(entries, entry)
		}
		if len(entries) != 0 {
			document["Triggers"] = entries
		}
	}
	var encoded []byte
	var err error
	if options.Format == "xml" {
		encoded, err = encodeStructured(options.Format, []map[string]any{document})
	} else {
		// JSON flow collections are valid typed YAML too.
		encoded, err = json.Marshal(document)
	}
	return prefix + string(encoded), err
}
