package xml

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

type serializeWitness struct {
	Group       string `json:"group"`
	ID          int    `json:"id"`
	Mode        string `json:"mode"`
	InitialMode string `json:"initial_mode"`
	Input       string `json:"input"`
	Indent      bool   `json:"indent"`
	Output      string `json:"output"`
	Result      struct {
		Output  string `json:"output"`
		State   string `json:"state"`
		Message string `json:"message"`
		Detail  string `json:"detail"`
		Hint    string `json:"hint"`
	} `json:"result"`
}

func serializationCorpus(t *testing.T) []serializeWitness {
	t.Helper()
	data, err := os.ReadFile("data/serialize-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []serializeWitness
	if err = json.Unmarshal(data, &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

// Only the independently observed randomized notation section is unordered.
// Preserve every exact record/count and its boundary before all ordinary nodes.
func canonicalNotations(s string) string {
	lines := strings.Split(s, "\n")
	start := -1
	end := 0
	for i, line := range lines {
		if strings.HasPrefix(line, "<!NOTATION ") {
			if start < 0 {
				start = i
			}
			end = i + 1
		} else if start >= 0 {
			break
		}
	}
	if start >= 0 {
		sort.Strings(lines[start:end])
	}
	return strings.Join(lines, "\n")
}
func checkSerializeWitness(t *testing.T, row serializeWitness) {
	t.Helper()
	ctx := context.Background()
	initial := Content
	if row.InitialMode == "DOCUMENT" {
		initial = Document
	}
	value, err := Parse(ctx, row.Input, Options{Mode: initial})
	if err != nil {
		var pe *ParseError
		if row.Result.State != "2200N" || !errors.As(err, &pe) || pe.Error() != row.Result.Message || pe.Detail() != row.Result.Detail || value.valid {
			t.Fatalf("initial parse: result %+v error %v detail %q; want %+v", value, err, func() string {
				if pe != nil {
					return pe.Detail()
				}
				return ""
			}(), row.Result)
		}
		return
	}
	if row.Result.State == "2200N" {
		t.Fatalf("initial parse unexpectedly accepted %q", row.Input)
	}
	mode := Content
	if row.Mode == "DOCUMENT" {
		mode = Document
	}
	got, _, err := Serialize(ctx, value, SerializeOptions{Mode: mode, Indent: row.Indent})
	if row.Result.State != "" {
		var nd *NotDocumentError
		if row.Result.State != "2200L" || !errors.As(err, &nd) || got != "" || err.Error() != row.Result.Message || row.Result.Detail != "" || row.Result.Hint != "" {
			t.Fatalf("serialization %q %v; want %+v", got, err, row.Result)
		}
		return
	}
	if err != nil || canonicalNotations(got) != canonicalNotations(row.Result.Output) {
		t.Fatalf("serialization %q error %v; want %q", got, err, row.Result.Output)
	}
	if value.Original() != row.Input {
		t.Fatal("serialization mutated original")
	}
}
func runSerializeCorpus(t *testing.T, selectRow func(serializeWitness) bool) {
	t.Helper()
	count := 0
	for _, row := range serializationCorpus(t) {
		if !selectRow(row) {
			continue
		}
		count++
		t.Run(fmt.Sprintf("%s-%d-%s-%t", row.Group, row.ID, row.Mode, row.Indent), func(t *testing.T) { checkSerializeWitness(t, row) })
	}
	if count == 0 {
		t.Fatal("empty oracle selection")
	}
}
func TestXMLSerializeLayout(t *testing.T) {
	runSerializeCorpus(t, func(r serializeWitness) bool {
		return r.Group == "initial" && r.ID <= 4 || r.Group == "edge" && r.ID == 0
	})
}
func TestXMLSerializeWhitespace(t *testing.T) {
	runSerializeCorpus(t, func(r serializeWitness) bool {
		return r.Group == "further" && (r.ID >= 6 && r.ID <= 9 || r.ID >= 19) || r.Group == "branches" && r.ID <= 5
	})
}
func TestXMLSerializeNodes(t *testing.T) {
	runSerializeCorpus(t, func(r serializeWitness) bool {
		return r.Group == "further" && (r.ID == 0 || r.ID >= 10 && r.ID <= 12) || r.Group == "initial" && r.ID >= 7 && r.ID <= 11
	})
}
func TestXMLSerializeDeclarations(t *testing.T) {
	runSerializeCorpus(t, func(r serializeWitness) bool {
		return r.Group == "document-entry" || r.Group == "initial" && (r.ID == 5 || r.ID == 6 || r.ID >= 13) || r.Group == "further" && r.ID >= 1 && r.ID <= 5 || r.Group == "edge" && r.ID == 3
	})
}
func TestXMLSerializeDTD(t *testing.T) {
	runSerializeCorpus(t, func(r serializeWitness) bool {
		return r.Group == "dtd-edges" || r.Group == "initial" && r.ID == 12 || r.Group == "further" && r.ID >= 13 && r.ID <= 18 || r.Group == "edge" && r.ID == 2 || r.Group == "composed" && r.ID <= 3 || r.Group == "branches" && r.ID == 6
	})
	// The actual 20 captures cover all six permutations. Exact records and their
	// position are compared, rather than treating the whole document as a bag.
	const input = `<!DOCTYPE a [<!NOTATION z SYSTEM "z"><!NOTATION a SYSTEM "a"><!NOTATION m SYSTEM "m"><!ENTITY e SYSTEM "u" NDATA z>]><a/>`
	value, err := Parse(context.Background(), input, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := Serialize(context.Background(), value, SerializeOptions{Mode: Document, Indent: true})
	if err != nil {
		t.Fatal(err)
	}
	const deterministic = "<!DOCTYPE a [\n<!NOTATION z SYSTEM \"z\" >\n<!NOTATION a SYSTEM \"a\" >\n<!NOTATION m SYSTEM \"m\" >\n<!ENTITY e SYSTEM \"u\" NDATA z>\n]>\n<a/>"
	if got != deterministic {
		t.Fatalf("insertion order %q", got)
	}
	orders := map[string]bool{}
	for _, row := range serializationCorpus(t) {
		if row.Group == "notation-order" {
			orders[row.Output] = true
			if canonicalNotations(got) != canonicalNotations(row.Output) {
				t.Fatalf("notation records %q", row.Output)
			}
		}
	}
	if len(orders) != 6 {
		t.Fatalf("captured permutations %d", len(orders))
	}
}
func TestXMLSerializeXHTML(t *testing.T) {
	runSerializeCorpus(t, func(r serializeWitness) bool {
		return r.Group == "xhtml" || r.Group == "edge" && r.ID == 1 || r.Group == "branches" && r.ID >= 7
	})
}
func TestXMLSerializeQualification(t *testing.T) {
	runSerializeCorpus(t, func(r serializeWitness) bool { return r.Group == "qualification" || r.Group == "composed" && r.ID >= 4 })
	// Genuine binary decoding is inherited from CORE's independently captured
	// receive contract. Serialization is compared to the equivalent Unicode case.
	raw := "<?xml version=\"1.0\" encoding=\"LATIN1\"?><a>\xe9</a>"
	value, err := Parse(context.Background(), raw, Options{InputKind: Binary})
	if err != nil {
		t.Fatal(err)
	}
	const decoded = "<?xml version=\"1.0\" encoding=\"LATIN1\"?><a>é</a>"
	if value.Original() != decoded {
		t.Fatalf("decoded %q", value.Original())
	}
	got, _, err := Serialize(context.Background(), value, SerializeOptions{Indent: true})
	if err != nil || got != "<a>&#xE9;</a>" {
		t.Fatal(got, err)
	}
	got, _, err = Serialize(context.Background(), value, SerializeOptions{})
	if err != nil || got != decoded {
		t.Fatal(got, err)
	}
	text, warning, err := OutputText(context.Background(), value)
	if err != nil || warning.Message != "" || text != "<a>é</a>" {
		t.Fatal(text, warning, err)
	}
	binary, warning, err := OutputBinary(context.Background(), value, 8)
	if err != nil || warning.Message != "" || binary != "<?xml version=\"1.0\" encoding=\"LATIN1\"?><a>\xe9</a>" {
		t.Fatal(binary, warning, err)
	}
}
