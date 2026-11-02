package reference_test

import (
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"stuntgres/reference/fixture"
)

func overexplainScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			db, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			b.DB, b.Recorder = db, recorder
			defer c.Close()
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			defer func() {
				if t.Failed() {
					c.ExecContext(p.Ctx, "ROLLBACK")
				}
			}()
			body(t, p, b, c)
			if len(notices) != 0 {
				t.Errorf("unexpected notices: %#v", notices)
			}
		})
	}
}

func overexplainExec(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
}

func overexplainDiagnostic(q, code, message, token string) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic(code, message)
	if token != "" {
		i := strings.Index(q, token)
		if i < 0 {
			panic("diagnostic token absent: " + token)
		}
		d.Position = fixture.Position(len([]rune(q[:i])) + 1)
	}
	return d
}

func overexplainError(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string, d fixture.Diagnostic, stage string, selectRows bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, d)
		s.SelectRows, s.Prepared, s.ErrorStage = selectRows, prepared, "execute"
		if prepared {
			s.ErrorStage = stage
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func overexplainUnloaded(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	for _, format := range []string{"TEXT", "XML", "JSON", "YAML"} {
		for _, option := range []string{"DEBUG", "DEBUG FALSE"} {
			q := "/*é*/ EXPLAIN (FORMAT " + format + ", " + option + ") SELECT 1"
			overexplainError(t, p, b, c, q, overexplainDiagnostic(q, "42601", `unrecognized EXPLAIN option "debug"`, "DEBUG"), "execute", true)
		}
	}
}

type overexplainResult struct {
	text     string
	document []map[string]any
}

// XML is inspected structurally through encoding/xml; unlike JSON/YAML its
// scalars intentionally have lexical, not invented xsi, types.
type overexplainXML struct {
	XMLName  xml.Name
	Text     string           `xml:",chardata"`
	Children []overexplainXML `xml:",any"`
}

func overexplainXMLValue(t *testing.T, n overexplainXML, parent string) any {
	t.Helper()
	if n.XMLName.Space != "http://www.postgresql.org/2009/explain" {
		t.Fatalf("XML property escaped EXPLAIN namespace: %v", n.XMLName)
	}
	// Plan/Output is a target list; Sort-Targets/Item/Output is a Boolean.
	outputList := n.XMLName.Local == "Output" && parent == "Plan"
	list := n.XMLName.Local == "Plans" || outputList || n.XMLName.Local == "Target-List" || n.XMLName.Local == "Returning-Target-List" || n.XMLName.Local == "Sort-Targets"
	switch n.XMLName.Local {
	case "Range-Table", "Scopes", "Entries", "Columns", "Required-Permissions", "Outer-Scope-IDs":
		list = true
	}
	if len(n.Children) == 0 {
		if list {
			if strings.TrimSpace(n.Text) != "" {
				t.Fatalf("%s list contains scalar content %q", n.XMLName.Local, n.Text)
			}
			return []any{}
		}
		return strings.TrimSpace(n.Text)
	}
	// Nested arrays (for example VALUES rows) use Item inside Item.
	if n.Children[0].XMLName.Local == "Item" {
		list = true
	}
	if list {
		if strings.TrimSpace(n.Text) != "" {
			t.Fatalf("%s list contains scalar content %q", n.XMLName.Local, n.Text)
		}
		values := make([]any, 0, len(n.Children))
		for _, child := range n.Children {
			want := "Item"
			if n.XMLName.Local == "Plans" {
				want = "Plan"
			} else if n.XMLName.Local == "Range-Table" {
				want = "Range-Table-Entry"
			}
			if child.XMLName.Local != want {
				t.Fatalf("%s has unexpected list child %s", n.XMLName.Local, child.XMLName.Local)
			}
			values = append(values, overexplainXMLValue(t, child, n.XMLName.Local))
		}
		return values
	}
	m := make(map[string]any)
	for _, child := range n.Children {
		key := strings.ReplaceAll(child.XMLName.Local, "-", " ")
		if _, exists := m[key]; exists {
			t.Fatalf("duplicate XML property %s", key)
		}
		m[key] = overexplainXMLValue(t, child, n.XMLName.Local)
	}
	return m
}

func overexplainRows(t *testing.T, rows *sql.Rows, format string) overexplainResult {
	t.Helper()
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(cols, []string{"QUERY PLAN"}) {
		t.Fatalf("columns=%v: %v", cols, err)
	}
	types, err := rows.ColumnTypes()
	wantType := "TEXT"
	if format == "XML" || format == "JSON" {
		wantType = format
	}
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != wantType {
		t.Fatalf("want %s metadata: %v, %v", wantType, types, err)
	}
	var lines []string
	for rows.Next() {
		var value sql.NullString
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		if !value.Valid {
			t.Fatal("SQL NULL plan document/line")
		}
		if format == "TEXT" && strings.Contains(value.String, "\n") {
			t.Fatal("TEXT row is not one line")
		}
		lines = append(lines, value.String)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 || (format != "TEXT" && len(lines) != 1) {
		t.Fatalf("%s row count %d", format, len(lines))
	}
	r := overexplainResult{text: strings.Join(lines, "\n")}
	if strings.TrimSpace(r.text) == "" {
		t.Fatal("empty plan")
	}
	switch format {
	case "TEXT":
		return r
	case "JSON":
		if err := json.Unmarshal([]byte(r.text), &r.document); err != nil {
			t.Fatal(err, r.text)
		}
	case "YAML":
		d := yaml.NewDecoder(strings.NewReader(r.text))
		if err := d.Decode(&r.document); err != nil {
			t.Fatal(err, r.text)
		}
		var extra any
		if err := d.Decode(&extra); err != io.EOF {
			t.Fatalf("YAML trailing document: %v %v", extra, err)
		}
	case "XML":
		d := xml.NewDecoder(strings.NewReader(r.text))
		var root overexplainXML
		if err := d.Decode(&root); err != nil {
			t.Fatal(err, r.text)
		}
		if root.XMLName.Local != "explain" || root.XMLName.Space != "http://www.postgresql.org/2009/explain" {
			t.Fatalf("XML root: %v", root.XMLName)
		}
		for {
			token, err := d.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if chars, ok := token.(xml.CharData); !ok || strings.TrimSpace(string(chars)) != "" {
				t.Fatalf("trailing XML token %v", token)
			}
		}
		for _, query := range root.Children {
			if query.XMLName.Local != "Query" || query.XMLName.Space != root.XMLName.Space {
				t.Fatalf("XML Query: %v", query.XMLName)
			}
			m, ok := overexplainXMLValue(t, query, root.XMLName.Local).(map[string]any)
			if !ok {
				t.Fatal("XML Query is not a mapping")
			}
			r.document = append(r.document, m)
		}
	default:
		t.Fatal("unknown format", format)
	}
	if len(r.document) == 0 {
		t.Fatal("empty structured document")
	}
	for _, entry := range r.document {
		n, ok := entry["Plan"].(map[string]any)
		if !ok {
			t.Fatalf("missing structural Plan: %v", entry)
		}
		explainJSONVisit(n, func(n map[string]any) {
			if typ, ok := n["Node Type"].(string); !ok || typ == "" {
				t.Fatalf("missing Node Type: %v", n)
			}
			if children, exists := n["Plans"]; exists {
				list, ok := children.([]any)
				if !ok {
					t.Fatalf("Plans not sequence: %v", children)
				}
				for _, child := range list {
					if _, ok := child.(map[string]any); !ok {
						t.Fatalf("Plan child not mapping: %v", child)
					}
				}
			}
		})
	}
	return r
}

func overexplainQuery(t *testing.T, p *fixture.Pair, c fixture.Executor, q, format string, prepared bool, args ...any) overexplainResult {
	t.Helper()
	if prepared {
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(q, err)
		}
		defer stmt.Close()
		rows, err := stmt.QueryContext(p.Ctx, args...)
		if err != nil {
			t.Fatal(q, err)
		}
		return overexplainRows(t, rows, format)
	}
	rows, err := c.QueryContext(p.Ctx, q, args...)
	if err != nil {
		t.Fatal(q, err)
	}
	return overexplainRows(t, rows, format)
}

func overexplainInteger(t *testing.T, v any, lexical bool) int64 {
	t.Helper()
	var n int64
	switch v := v.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || v != math.Trunc(v) {
			t.Fatalf("noninteger %v", v)
		}
		n = int64(v)
	case int:
		n = int64(v)
	case int64:
		n = v
	case string:
		if !lexical {
			t.Fatalf("numeric property became string %q", v)
		}
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		n = parsed
	default:
		t.Fatalf("not an integer: %T %v", v, v)
	}
	return n
}

// ANALYZE rows/loops are numeric measurements, not planner integer identities.
// PostgreSQL XML preserves the decimal lexical form (for example 3.00).
func overexplainNumber(t *testing.T, v any, lexical bool) float64 {
	t.Helper()
	var n float64
	switch v := v.(type) {
	case float64:
		n = v
	case int:
		n = float64(v)
	case int64:
		n = float64(v)
	case string:
		if !lexical {
			t.Fatalf("numeric measurement became string %q", v)
		}
		parsed, err := strconv.ParseFloat(v, 64)
		if err != nil {
			t.Fatal(err)
		}
		n = parsed
	default:
		t.Fatalf("not a numeric measurement: %T %v", v, v)
	}
	if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
		t.Fatalf("invalid numeric measurement %v", n)
	}
	return n
}

func overexplainDebug(t *testing.T, b fixture.Backend, r overexplainResult, format string, enabled bool) {
	t.Helper()
	if format == "TEXT" {
		markers := []string{"Embedded Debug:", "Embedded Command Debug:", "Node ID:", "Target List:", "Sort Targets:", "Disabled Nodes:", "Parallel Safe:", "extParam:", "allParam:", "PlannedStmt:"}
		if enabled {
			if b.PG {
				explainContains(t, []string{r.text}, "Plan Node ID:", "Disabled Nodes:", "Parallel Safe:", "PlannedStmt:", "Command Type:")
			} else if strings.Contains(r.text, "Embedded Debug:") {
				explainContains(t, []string{r.text}, "Node ID:")
			} else {
				explainContains(t, []string{r.text}, "Embedded Command Debug:", "Relation OID:")
			}
		} else {
			for _, marker := range markers {
				if strings.Contains(r.text, marker) {
					t.Errorf("disabled DEBUG leaked %s", marker)
				}
			}
		}
		return
	}
	count := 0
	for _, entry := range r.document {
		if b.PG && enabled {
			stmt, ok := entry["PlannedStmt"].(map[string]any)
			if !ok {
				t.Fatalf("missing query-level PlannedStmt: %v", entry)
			}
			for _, key := range []string{"Command Type", "Flags", "Subplans Needing Rewind", "Relation OIDs", "Executor Parameter Types", "Parse Location"} {
				if _, ok := stmt[key].(string); !ok {
					t.Fatalf("PG %s must be string: %v", key, stmt)
				}
			}
		} else if _, exists := entry["PlannedStmt"]; exists {
			t.Fatal("unexpected PG PlannedStmt", entry)
		}
		explainJSONVisit(entry["Plan"].(map[string]any), func(n map[string]any) {
			if !enabled {
				for _, key := range []string{"Embedded Debug", "Embedded Command Debug", "Disabled Nodes", "Parallel Safe", "Plan Node ID", "extParam", "allParam"} {
					if _, exists := n[key]; exists {
						t.Errorf("DEBUG FALSE/default leaked %s", key)
					}
				}
				return
			}
			if b.PG {
				if _, exists := n["Embedded Debug"]; exists {
					t.Fatal("PG emitted embedded schema")
				}
				if _, exists := n["Embedded Command Debug"]; exists {
					t.Fatal("PG emitted embedded command schema")
				}
				if overexplainInteger(t, n["Disabled Nodes"], format == "XML") < 0 || overexplainInteger(t, n["Plan Node ID"], format == "XML") < 0 {
					t.Fatal("invalid PG debug integer")
				}
				if format == "XML" {
					if n["Parallel Safe"] != "true" && n["Parallel Safe"] != "false" {
						t.Fatal("XML Boolean", n)
					}
				} else if _, ok := n["Parallel Safe"].(bool); !ok {
					t.Fatal("PG Parallel Safe is not Boolean", n)
				}
				for _, key := range []string{"extParam", "allParam"} {
					if _, ok := n[key].(string); !ok {
						t.Fatalf("PG %s is not string: %v", key, n)
					}
				}
				count++
				return
			}
			for _, key := range []string{"Disabled Nodes", "Parallel Safe", "Plan Node ID", "extParam", "allParam"} {
				if _, exists := n[key]; exists {
					t.Errorf("fabricated PG planner field %s", key)
				}
			}
			if value, exists := n["Embedded Command Debug"]; exists {
				switch n["Node Type"] {
				case "Insert", "Update", "Delete", "Merge":
				default:
					t.Fatal("non-command fabricated bound command data", n)
				}
				d, ok := value.(map[string]any)
				if !ok || overexplainInteger(t, d["Relation OID"], format == "XML") <= 0 {
					t.Fatal("invalid actual command target identity", value)
				}
				if _, exists := d["Node ID"]; exists {
					t.Fatal("command fabricated a graph node ID", d)
				}
				list, ok := d["Returning Target List"].([]any)
				if !ok {
					t.Fatal("bound RETURNING target list is not an explicit array", d)
				}
				for _, target := range list {
					if _, ok := target.(string); !ok {
						t.Fatal("RETURNING target is not SQL string", target)
					}
				}
				count++
			}
			if value, exists := n["Embedded Debug"]; exists {
				if n["Node Type"] == "SubPlan" || n["Node Type"] == "Rule Rewrite" {
					t.Fatal("synthetic wrapper invented bound-node identity", n)
				}
				d, ok := value.(map[string]any)
				if !ok || overexplainInteger(t, d["Node ID"], format == "XML") <= 0 {
					t.Fatalf("invalid Embedded Debug: %v", value)
				}
				for _, key := range []string{"Frame", "Occurrence", "Relation OID"} {
					if v, exists := d[key]; exists {
						overexplainInteger(t, v, format == "XML")
					}
				}
				if targets, exists := d["Target List"]; exists {
					list, ok := targets.([]any)
					if !ok {
						t.Fatal("Target List is not an array", targets)
					}
					for _, target := range list {
						if _, ok := target.(string); !ok {
							t.Fatal("target is not SQL string", target)
						}
					}
				}
				if targets, exists := d["Sort Targets"]; exists {
					list, ok := targets.([]any)
					if !ok {
						t.Fatal("Sort Targets is not an array", targets)
					}
					for _, target := range list {
						m, ok := target.(map[string]any)
						if !ok {
							t.Fatal("sort target is not a record", target)
						}
						for _, key := range []string{"Target Identity", "Output Ordinal", "Slot", "Cache"} {
							overexplainInteger(t, m[key], format == "XML")
						}
						if format == "XML" {
							if m["Output"] != "true" && m["Output"] != "false" {
								t.Fatal("sort Output not Boolean", m)
							}
						} else if _, ok := m["Output"].(bool); !ok {
							t.Fatal("sort Output not Boolean", m)
						}
					}
				}
				count++
			}
		})
	}
	if enabled && count == 0 {
		t.Fatal("DEBUG emitted no genuine bound node or command data")
	}
}

func overexplainSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	overexplainExec(t, p, b, c, "CREATE TABLE overexplain_data(id integer,v text)")
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO overexplain_data VALUES(1,'one'),(2,NULL),(3,'three')", 3))
	overexplainExec(t, p, b, c, "CREATE SEQUENCE overexplain_seq START 41")
}

func overexplainInvariant(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,v FROM overexplain_data ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), nil}, {int64(3), "three"}}))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT nextval('overexplain_seq')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(41)}}))
}

func overexplainSQL(format, options, child string) string {
	if options != "" {
		options = ", " + options
	}
	return fmt.Sprintf("EXPLAIN (FORMAT %s%s) %s", format, options, child)
}
