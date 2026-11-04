package reference_test

import (
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"stuntgres"
	"stuntgres/reference/fixture"
)

func autoExplainMessage(t *testing.T, notices *[]fixture.Diagnostic, severity string, context ...string) string {
	t.Helper()
	if len(*notices) != 1 {
		t.Fatalf("want one %s plan: %#v", severity, *notices)
	}
	d := (*notices)[0]
	*notices = nil
	want := fixture.Diagnostic{Severity: severity, SeverityUnlocalized: severity, Code: "00000", Message: d.Message}
	if severity == "WARNING" {
		want.Code = "01000"
	}
	if len(context) != 0 {
		want.Context = context[0]
	}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("diagnostic=%#v want %#v", d, want)
	}
	prefix := autoExplainDuration.FindString(d.Message)
	if prefix == "" {
		t.Fatalf("duration envelope: %q", d.Message)
	}
	return strings.TrimPrefix(d.Message, prefix)
}

func TestReferenceAutoExplainFormats(t *testing.T) {
	t.Run("materialized_cte_current_arguments", autoExplainCTEFormats)
	autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE ae_formats(id integer,v text)", 0))
		run(fixture.Exec("INSERT INTO ae_formats VALUES(1,'one'),(2,NULL)", 2))
		autoExplainConfigure(t, p, b, c, notices)
		run(fixture.Exec("SET auto_explain.log_buffers=on", 0))
		run(fixture.Exec("SET auto_explain.log_wal=on", 0))
		const query = "SELECT id,v FROM ae_formats WHERE id=$1 ORDER BY id"
		for _, format := range []string{"text", "xml", "json", "yaml"} {
			for _, verbose := range []string{"off", "on"} {
				run(fixture.Exec("SET auto_explain.log_format = "+format, 0))
				run(fixture.Exec("SET auto_explain.log_verbose = "+verbose, 0))
				run(fixture.Prepared(fixture.Select(query, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), nil}}, int64(2))))
				body := autoExplainMessage(t, notices, "NOTICE")
				if !b.PG {
					access, writes := "Logical Tuple Accesses", "Logical Tuple Writes"
					if format == "xml" {
						access, writes = "Logical-Tuple-Accesses", "Logical-Tuple-Writes"
					}
					if !strings.Contains(body, access) || !strings.Contains(body, writes) {
						t.Fatal("named metric fields missing from format", format, body)
					}
				}
				switch format {
				case "text":
					for _, fragment := range []string{"Query Text: " + query, "Query Parameters: $1 = '2'", "actual", "rows="} {
						if !strings.Contains(body, fragment) {
							t.Fatalf("TEXT missing %q: %s", fragment, body)
						}
					}
					if verbose == "on" && !strings.Contains(body, "Output:") {
						t.Fatal("verbose output absent", body)
					}
				case "xml":
					decoder := xml.NewDecoder(strings.NewReader(body))
					var source, params string
					plans := 0
					for {
						token, err := decoder.Token()
						if err == io.EOF {
							break
						}
						if err != nil {
							t.Fatal(err, body)
						}
						if start, ok := token.(xml.StartElement); ok {
							if start.Name.Space != "http://www.postgresql.org/2009/explain" {
								t.Fatal("XML namespace", start)
							}
							switch start.Name.Local {
							case "Query-Text":
								if err := decoder.DecodeElement(&source, &start); err != nil {
									t.Fatal(err)
								}
							case "Query-Parameters":
								if err := decoder.DecodeElement(&params, &start); err != nil {
									t.Fatal(err)
								}
							case "Plan":
								plans++
							}
						}
					}
					if source != query || params != "$1 = '2'" || plans == 0 {
						t.Fatal("XML document", source, params, plans)
					}
				default:
					var document map[string]any
					if format == "json" {
						if err := json.Unmarshal([]byte(body), &document); err != nil {
							t.Fatal(err)
						}
					} else if err := yaml.Unmarshal([]byte(body), &document); err != nil {
						t.Fatal(err)
					}
					if document["Query Text"] != query || document["Query Parameters"] != "$1 = '2'" {
						t.Fatal("typed source/params", document)
					}
					root, ok := document["Plan"].(map[string]any)
					if !ok {
						t.Fatal("typed Plan absent", document)
					}
					explainJSONVisit(root, func(node map[string]any) {
						if _, ok := node["Actual Rows"]; !ok {
							t.Fatal("actual rows absent", node)
						}
						if _, ok := node["Actual Total Time"]; ok {
							t.Fatal("timing off", node)
						}
					})
				}
			}
		}
		run(fixture.Exec("SET auto_explain.log_format=json", 0))
		run(fixture.Exec("SET auto_explain.log_timing=on", 0))
		run(fixture.Select(query, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), nil}}, int64(2)))
		body := autoExplainMessage(t, notices, "NOTICE")
		var timed map[string]any
		if err := json.Unmarshal([]byte(body), &timed); err != nil {
			t.Fatal(err)
		}
		root := timed["Plan"].(map[string]any)
		elapsed, ok := root["Actual Total Time"].(float64)
		if !ok || elapsed < 0 || math.IsNaN(elapsed) || math.IsInf(elapsed, 0) {
			t.Fatal("actual timing-on measurement absent/nonfinite", root)
		}
	})
}

func TestReferenceAutoExplainLevels(t *testing.T) {
	autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		autoExplainConfigure(t, p, b, c, notices)
		const query = "SELECT 8::integer AS v"
		for _, tc := range []struct {
			level, threshold, severity string
			delivered                  bool
		}{
			{"log", "notice", "LOG", false}, {"log", "log", "LOG", true},
			{"notice", "notice", "NOTICE", true}, {"notice", "warning", "NOTICE", false},
			{"warning", "warning", "WARNING", true}, {"warning", "error", "WARNING", false},
			{"info", "error", "INFO", true},
			{"debug5", "debug5", "DEBUG", true}, {"debug5", "debug4", "DEBUG", false},
			{"debug4", "debug4", "DEBUG", true}, {"debug4", "debug3", "DEBUG", false},
			{"debug3", "debug3", "DEBUG", true}, {"debug3", "debug2", "DEBUG", false},
			{"debug2", "debug2", "DEBUG", true}, {"debug2", "debug1", "DEBUG", false},
			{"debug1", "debug1", "DEBUG", true}, {"debug1", "notice", "DEBUG", false},
			{"debug", "debug2", "DEBUG", true},
		} {
			run(fixture.Exec("SET auto_explain.log_level = "+tc.level, 0))
			run(fixture.Exec("SET client_min_messages = "+tc.threshold, 0))
			run(fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{int64(8)}}))
			// PostgreSQL DEBUG loader/planner traces are not auto_explain plans.
			var plans []fixture.Diagnostic
			for _, d := range *notices {
				if autoExplainDuration.MatchString(d.Message) {
					plans = append(plans, d)
				}
			}
			*notices = plans
			if tc.delivered {
				autoExplainMessage(t, notices, tc.severity)
			} else {
				autoExplainQuiet(t, notices)
			}
		}
		if !b.PG {
			calls := 0
			var callbackErr error
			configured := p.NativeConnector.WithNoticeHandler(func(d stuntgres.Error) {
				if !autoExplainDuration.MatchString(d.Message) {
					return
				}
				calls++
				var value int
				callbackErr = p.Native.QueryRowContext(p.Ctx, "SELECT 23").Scan(&value)
				if callbackErr == nil && value != 23 {
					callbackErr = fmt.Errorf("reentry value=%d", value)
				}
			})
			db := sql.OpenDB(configured)
			defer db.Close()
			owner, err := db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			for _, statement := range []string{"LOAD 'auto_explain'", "SET auto_explain.log_level=notice", "SET auto_explain.log_min_duration=0"} {
				if _, err := owner.ExecContext(p.Ctx, statement); err != nil {
					t.Fatal(err)
				}
			}
			var value int
			if err := owner.QueryRowContext(p.Ctx, "SELECT 12").Scan(&value); err != nil || value != 12 || callbackErr != nil || calls != 1 {
				t.Fatal("unlocked notice-handler reentry", value, err, callbackErr, calls)
			}
		}
	})
}
