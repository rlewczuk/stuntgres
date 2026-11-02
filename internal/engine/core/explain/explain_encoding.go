package explain

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

const explainNamespace = "http://www.postgresql.org/2009/explain"

func encodeStructured(format string, document []map[string]any) ([]byte, error) {
	switch format {
	case "json", "yaml":
		// JSON's flow collections and double-quoted strings are also YAML 1.2.
		// Encode the original typed document directly: no JSON round trip, scalar
		// coercion, plain-string heuristics, or second structural representation.
		return json.Marshal(document)
	case "xml":
		var out bytes.Buffer
		e := xml.NewEncoder(&out)
		root := xml.StartElement{Name: xml.Name{Local: "explain"}, Attr: []xml.Attr{{Name: xml.Name{Local: "xmlns"}, Value: explainNamespace}}}
		if err := e.EncodeToken(root); err != nil {
			return nil, err
		}
		for _, entry := range document {
			if err := encodeExplainXML(e, "Query", entry); err != nil {
				return nil, err
			}
		}
		if err := e.EncodeToken(root.End()); err != nil {
			return nil, err
		}
		if err := e.Flush(); err != nil {
			return nil, err
		}
		return out.Bytes(), nil
	default:
		return nil, fmt.Errorf("stuntgres: invalid structured EXPLAIN format %q", format)
	}
}

// Every key is a fixed, owned structural property. Provider labels remain
// Name/Value data, never XML element names. Empty maps/lists and nil scalars
// produce empty elements; XML scalars intentionally have lexical content only.
func encodeExplainXML(e *xml.Encoder, label string, value any) error {
	start := xml.StartElement{Name: xml.Name{Local: strings.ReplaceAll(label, " ", "-")}}
	if err := e.EncodeToken(start); err != nil {
		return err
	}
	item := "Item"
	if label == "Plans" {
		item = "Plan"
	}
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			if err := encodeExplainXML(e, key, v[key]); err != nil {
				return err
			}
		}
	case map[string]float64:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			if err := encodeExplainXML(e, key, v[key]); err != nil {
				return err
			}
		}
	case []map[string]any:
		for _, child := range v {
			if err := encodeExplainXML(e, item, child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range v {
			if err := encodeExplainXML(e, item, child); err != nil {
				return err
			}
		}
	case []string:
		for _, child := range v {
			if err := encodeExplainXML(e, item, child); err != nil {
				return err
			}
		}
	default:
		text, err := explainScalar(v)
		if err != nil {
			return err
		}
		if err := e.EncodeToken(xml.CharData(text)); err != nil {
			return err
		}
	}
	return e.EncodeToken(start.End())
}

func explainScalar(value any) (string, error) {
	switch v := value.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	case bool:
		return strconv.FormatBool(v), nil
	case int:
		return strconv.Itoa(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case uint32:
		return strconv.FormatUint(uint64(v), 10), nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return "", fmt.Errorf("stuntgres: nonfinite EXPLAIN scalar")
		}
		return strconv.FormatFloat(v, 'g', -1, 64), nil
	default:
		return "", fmt.Errorf("stuntgres: invalid EXPLAIN scalar %T", value)
	}
}
