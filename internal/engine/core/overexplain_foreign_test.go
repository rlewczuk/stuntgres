package core_test

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strconv"
	"testing"

	"go.yaml.in/yaml/v3"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

type explainForeignXMLNode struct {
	Kind      string `xml:"Node-Type"`
	Operation string `xml:"Foreign-Operation"`
	Sources   []struct {
		Occurrence int
		Schema     string
		Relation   string `xml:"Relation-Name"`
	} `xml:"Foreign-Sources>Item"`
	Properties []struct{ Name, Value string } `xml:"Provider-Properties>Item"`
	Estimates  *struct {
		Cost string `xml:"Total-Cost"`
	} `xml:"Provider-Estimates"`
	ActualRows float64 `xml:"Actual-Rows"`
	Debug      struct {
		ID int `xml:"Node-ID"`
	} `xml:"Embedded-Debug"`
	Children []explainForeignXMLNode `xml:"Plans>Plan"`
}

func TestExplainStructuredPresentation(t *testing.T) {
	for _, format := range []string{"json", "xml", "yaml"} {
		for _, composite := range []bool{false, true} {
			t.Run(fmt.Sprintf("foreign-%s/composite=%t", format, composite), func(t *testing.T) {
				e, s, w := explainExecutionFixture(t)
				w.declineExecution = !composite
				w.extraProperties = []catalog.ForeignExplainProperty{{Name: "same", Value: "first"}, {Name: "same", Value: "second"}}
				if err := s.Settings.LoadOverexplain(context.Background()); err != nil {
					t.Fatal(err)
				}
				query := "SELECT k FROM executed_explain_data WHERE k=$1"
				if composite {
					query = "SELECT a.k FROM executed_explain_data a JOIN executed_explain_data b ON a.k=b.k WHERE a.k=$1"
				}
				ast, description := enginetest.PreparedDescription(t, e, s, "EXPLAIN (DEBUG, ANALYZE, VERBOSE, FORMAT "+format+") "+query)
				defer e.RetirePreparedDescription(&description)
				owner, analysis, generation := description.Folds, description.Analysis, description.PlanGeneration
				op := enginetest.TransactionOperation(t, s)
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				explainExecutionCleanup(t, scope)
				scope.Expected = &description
				w.check = func() error {
					if !e.Mu.TryLock() {
						return fmt.Errorf("provider called under engine lock")
					}
					e.Mu.Unlock()
					w.observation, w.nodes = core.ExplainObservationForTest(scope)
					return nil
				}
				r, err := scope.Execute(ast, 1, func(_ context.Context, p analyze.ParameterInfo, frame []sqltype.Value, _ sqltype.InputEnvironment) error {
					var err error
					frame[0], err = sqltype.Integer(p.Types[0].Type, 1)
					return err
				})
				if err != nil {
					scope.Finish(err)
					t.Fatal(err)
				}
				defer r.Rows.Close()
				if err := scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
				if scope.View != nil || scope.LockTicket != nil || core.ExplainActiveForTest(scope) || w.begins != w.ends || w.begins == 0 {
					t.Fatal("structured observation retained live execution owners")
				}
				backing := r.Rows.(*exec.FinalRows).Anonymous[0]
				cells := make([]sqltype.Value, 1)
				if ok, err := r.Rows.Next(context.Background(), cells); err != nil || !ok || cells[0].IsNull() {
					t.Fatal("missing structured output", err)
				}
				retained := cells[0]
				text, err := retained.Type().Output(context.Background(), retained)
				if err != nil {
					t.Fatal(err)
				}
				if ok, err := r.Rows.Next(context.Background(), cells); err != nil || ok {
					t.Fatal("multiple structured documents", err)
				}
				r.Rows.Close()
				selected := false
				for _, node := range w.nodes {
					if observed, ok := w.observation.Foreign(node); ok {
						if observed.Composite {
							selected = true
							if len(observed.Sources) != 2 {
								t.Fatal("actual composite source capture missing", observed)
							}
						}
						if len(observed.Properties) != 3 || observed.Properties[1].Value != "first" || observed.Properties[2].Value != "second" {
							t.Fatal("actual selected provider duplicates lost", observed)
						}
					}
				}
				if selected != composite {
					t.Fatal("test missed actual late selection", selected, composite)
				}
				explainProtocolDocument(t, format, []byte(text))
				found := false
				if format == "xml" {
					var root struct {
						Queries []struct {
							Plan explainForeignXMLNode `xml:"Plan"`
						} `xml:"Query"`
					}
					if err := xml.Unmarshal([]byte(text), &root); err != nil {
						t.Fatal(err)
					}
					var visit func(explainForeignXMLNode)
					visit = func(n explainForeignXMLNode) {
						if len(n.Properties) != 0 {
							found = true
							if n.Kind != "Foreign Scan" || n.Debug.ID <= 0 || n.Estimates == nil || n.Estimates.Cost == "" || n.Properties[0].Name != "Execution token" || n.Properties[1].Name != "same" || n.Properties[1].Value != "first" || n.Properties[2].Value != "second" {
								t.Fatal("XML lost selected structural properties", text)
							}
							if composite && (n.Operation != "join" || len(n.Sources) != 2 || len(n.Children) != 0) {
								t.Fatal("XML did not substitute actual composite", text)
							}
							for i, source := range n.Sources {
								if source.Occurrence != i+1 || source.Schema != "public" || source.Relation != "executed_explain_data" {
									t.Fatal("XML source record changed", source)
								}
							}
						}
						for _, c := range n.Children {
							visit(c)
						}
					}
					visit(root.Queries[0].Plan)
					if root.Queries[0].Plan.ActualRows != 1 {
						t.Fatal("XML actual output changed", text)
					}
				} else {
					var document []map[string]any
					if format == "json" {
						err = json.Unmarshal([]byte(text), &document)
					} else {
						err = yaml.Unmarshal([]byte(text), &document)
					}
					if err != nil {
						t.Fatal(err)
					}
					var visit func(map[string]any)
					visit = func(n map[string]any) {
						if props, exists := n["Provider Properties"]; exists {
							found = true
							list := props.([]any)
							if len(list) != 3 || list[0].(map[string]any)["Name"] != "Execution token" || list[1].(map[string]any)["Value"] != "first" || list[2].(map[string]any)["Value"] != "second" || n["Provider Estimates"] == nil || n["Embedded Debug"] == nil {
								t.Fatal("typed selected provider properties missing", n)
							}
							if _, err := strconv.Atoi(list[0].(map[string]any)["Value"].(string)); err != nil {
								t.Fatal("not an actual execution token", n)
							}
							if composite {
								if n["Node Type"] != "Foreign Scan" || n["Foreign Operation"] != "join" || n["Plans"] != nil {
									t.Fatal("actual composite did not replace original children", n)
								}
								sources := n["Foreign Sources"].([]any)
								if len(sources) != 2 {
									t.Fatal("source records lost", n)
								}
								for i, source := range sources {
									m := source.(map[string]any)
									if fmt.Sprint(m["Occurrence"]) != fmt.Sprint(i+1) || m["Schema"] != "public" || m["Relation Name"] != "executed_explain_data" {
										t.Fatal("source record changed", m)
									}
									if _, stringNumber := m["Occurrence"].(string); stringNumber {
										t.Fatal("source numeric scalar became string", m)
									}
								}
							}
						}
						children, _ := n["Plans"].([]any)
						for _, c := range children {
							visit(c.(map[string]any))
						}
					}
					visit(document[0]["Plan"].(map[string]any))
				}
				if !found {
					t.Fatal("no real selected provider properties in accepted document")
				}
				for i := range w.properties {
					w.properties[i].Value = "overwritten provider memory"
				}
				w.nodes, w.observation = nil, nil
				after, err := retained.Type().Output(context.Background(), retained)
				if err != nil || after != text || backing[0] != retained || description.Folds != owner || owner.Live() || owner.Current().Generation != 0 || description.Analysis != analysis || description.PlanGeneration != generation {
					t.Fatal("accepted output/definition retained mutable execution/provider state", err)
				}
				enginetest.LockEmpty(t, e)
			})
		}
	}
}
