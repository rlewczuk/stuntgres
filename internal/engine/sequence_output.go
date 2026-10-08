package engine

import (
	"fmt"
	"strconv"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Output presentation is captured once before returning Rows. The internal
// datum stays an OID, and retained Rows never consult later catalog authority.
func (e *Engine) captureRegclassOutput(op *Operation, rows *finalRows) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return err
	}
	path, err := e.referencePath(auth)
	if err != nil {
		return err
	}
	for _, value := range rows.values {
		if value.IsNull() || value.Type().ID() != sqltype.Regclass {
			continue
		}
		key, err := value.Text()
		if err != nil {
			return err
		}
		if _, exists := rows.regclassOutput[key]; exists {
			continue
		}
		n, err := strconv.ParseUint(key, 10, 32)
		if err != nil {
			return err
		}
		text := regclassSpelling(auth.catalog, path, OID(n))
		if rows.regclassOutput == nil {
			rows.regclassOutput = make(map[string]string)
		}
		rows.regclassOutput[key] = text
	}
	return nil
}

// String output uses the same captured namespace presentation as finalRows,
// without changing the canonical OID datum or consulting a later live catalog.
func (s *StatementScope) FormatRegclass(id uint32) (string, error) {
	if s == nil || s.commentCatalog.state == nil {
		return "", fmt.Errorf("stuntgres: missing regclass output snapshot")
	}
	return regclassSpelling(s.commentCatalog, s.deparsePath, OID(id)), nil
}

// regclassSpelling uses only the statement's captured catalog and relation path.
func regclassSpelling(view CatalogRead, path []ObjectAddress, id OID) string {
	if id == 0 {
		return "-"
	}
	address := ObjectAddress{ClassID: RelationClassID, ObjectID: id}
	if d, ok := view.state.descriptors[address].(*relationDescriptor); ok {
		text := regclassIdentifier(d.name)
		if namedObjectVisible(view, path, d) {
			return text
		}
		return regclassIdentifier(view.state.descriptors[d.namespace].Name()) + "." + text
	}
	return strconv.FormatUint(uint64(id), 10)
}

func regclassIdentifier(name string) string {
	if parser.IdentifierNeedsQuotes(name) {
		return quoteIdentifier(name)
	}
	return name
}
