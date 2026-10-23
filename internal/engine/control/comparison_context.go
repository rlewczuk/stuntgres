package control

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
)

const ComparisonContextBegin = "stuntgres:comparison-context begin"
const ComparisonContextEnd = "stuntgres:comparison-context end"
const ComparisonBindingEnd = "stuntgres:comparison-binding end"
const comparisonBindingPrefix = "stuntgres:comparison-binding "

func InvalidComparisonContext() error {
	return fmt.Errorf("stuntgres: invalid comparison binding context")
}

// DecodeComparisonContext accepts exactly one JSON namespace field. Portable
// text requests a binding; the importer must reconstruct current authority.
func DecodeComparisonContext(comment string) (kind, namespace string, recognized bool, err error) {
	text := strings.TrimSpace(comment)
	switch text {
	case ComparisonContextBegin:
		return "begin", "", true, nil
	case ComparisonContextEnd:
		return "finish", "", true, nil
	case ComparisonBindingEnd:
		return "close", "", true, nil
	}
	if !strings.HasPrefix(text, "stuntgres:comparison-") {
		return "", "", false, nil
	}
	if !strings.HasPrefix(text, comparisonBindingPrefix) {
		return "", "", true, InvalidComparisonContext()
	}
	encoded := strings.TrimPrefix(text, comparisonBindingPrefix)
	raw, failure := base64.StdEncoding.Strict().DecodeString(encoded)
	if failure != nil || strings.ContainsAny(encoded, "\r\n") {
		return "", "", true, InvalidComparisonContext()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, failure := d.Token()
	if failure != nil || token != json.Delim('{') {
		return "", "", true, InvalidComparisonContext()
	}
	token, failure = d.Token()
	if failure != nil || token != "namespace" {
		return "", "", true, InvalidComparisonContext()
	}
	if d.Decode(&namespace) != nil || namespace == "" || d.More() {
		return "", "", true, InvalidComparisonContext()
	}
	token, failure = d.Token()
	if failure != nil || token != json.Delim('}') {
		return "", "", true, InvalidComparisonContext()
	}
	if _, failure = d.Token(); failure != io.EOF {
		return "", "", true, InvalidComparisonContext()
	}
	return "bind", namespace, true, nil
}

func (w *dumpWriter) emitDefinition(view catalog.Read, statement dump.Statement) error {
	if statement.Binding == nil {
		if err := w.emit(statement.SQL); err != nil {
			return err
		}
		if statement.FollowingSQL != "" {
			return w.emit(statement.FollowingSQL)
		}
		return nil
	}
	namespace, ok := view.Namespace(statement.Binding.CubeNamespace)
	if !ok || namespace.Temporary != (ids.SessionToken{}) {
		return InvalidComparisonContext()
	}
	if !w.comparisonContext {
		if err := w.emit("-- " + ComparisonContextBegin + "\nSET search_path = pg_catalog;\n"); err != nil {
			return err
		}
		w.comparisonContext = true
	}
	raw, err := json.Marshal(struct {
		Namespace string `json:"namespace"`
	}{namespace.Name()})
	if err != nil {
		return err
	}
	if err := w.emit("-- " + comparisonBindingPrefix + base64.StdEncoding.EncodeToString(raw) + "\nSET search_path = pg_catalog, " + catalog.QuoteIdentifier(namespace.Name()) + ";\n"); err != nil {
		return err
	}
	if err := w.emit(statement.SQL); err != nil {
		return err
	}
	if err := w.emit("-- " + ComparisonBindingEnd + "\nSET search_path = pg_catalog;\n"); err != nil {
		return err
	}
	if statement.FollowingSQL != "" {
		return w.emit(statement.FollowingSQL)
	}
	return nil
}
