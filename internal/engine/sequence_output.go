package engine

import (
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
		text := key
		address := ObjectAddress{ClassID: RelationClassID, ObjectID: OID(n)}
		if n == 0 {
			text = "-"
		} else if d, ok := auth.catalog.state.descriptors[address].(*relationDescriptor); ok {
			text = regclassIdentifier(d.name)
			visible := false
			for _, namespace := range path {
				found, exists := auth.catalog.state.names[NameKey{ClassID: RelationClassID, Namespace: namespace, Name: d.name}]
				if exists {
					visible = found == address
					break
				}
			}
			if !visible {
				namespace := auth.catalog.state.descriptors[d.namespace]
				text = regclassIdentifier(namespace.Name()) + "." + text
			}
		}
		if rows.regclassOutput == nil {
			rows.regclassOutput = make(map[string]string)
		}
		rows.regclassOutput[key] = text
	}
	return nil
}

func regclassIdentifier(name string) string {
	if parser.IdentifierNeedsQuotes(name) {
		return quoteIdentifier(name)
	}
	return name
}
