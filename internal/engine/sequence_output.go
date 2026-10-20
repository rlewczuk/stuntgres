package engine

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
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
	var collationPath []ids.ObjectAddress
	aclOutput := capturedACLItemOutput{owner: e.catalog.owner(), roles: auth.Roles}
	roleOutput := capturedRegroleOutput{owner: e.catalog.owner(), roles: auth.Roles, quoteAll: rows.output.QuoteAllIdentifiers}
	for _, value := range rows.values {
		if err := e.captureRegObjectRowsValueLocked(rows, auth, &collationPath, value); err != nil {
			return err
		}
		if err := captureRegroleOutputValue(rows, roleOutput, value); err != nil {
			return err
		}
		if err := captureACLItemOutputValue(rows, aclOutput, value); err != nil {
			return err
		}
		if err := captureRegclassOutputValue(rows, auth.Catalog, path, value); err != nil {
			return err
		}
	}
	for _, row := range rows.anonymous {
		for _, value := range row {
			if err := e.captureRegObjectRowsValueLocked(rows, auth, &collationPath, value); err != nil {
				return err
			}
			if err := captureRegroleOutputValue(rows, roleOutput, value); err != nil {
				return err
			}
			if err := captureACLItemOutputValue(rows, aclOutput, value); err != nil {
				return err
			}
			if err := captureRegclassOutputValue(rows, auth.Catalog, path, value); err != nil {
				return err
			}
		}
	}
	if rows.source != nil {
		for i := 0; i < rows.count; i++ {
			index := i
			if rows.selection != nil {
				index = rows.selection[i]
			}
			row := rows.source.row(index)
			for _, slot := range rows.slots {
				if err := e.captureRegObjectRowsValueLocked(rows, auth, &collationPath, row[slot]); err != nil {
					return err
				}
				if err := captureRegroleOutputValue(rows, roleOutput, row[slot]); err != nil {
					return err
				}
				if err := captureACLItemOutputValue(rows, aclOutput, row[slot]); err != nil {
					return err
				}
				if err := captureRegclassOutputValue(rows, auth.Catalog, path, row[slot]); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func captureRegclassOutputValue(rows *finalRows, catalog catalog.Read, path []ids.ObjectAddress, value sqltype.Value) error {
	if err := captureRegprocOutputValue(rows, catalog, path, value); err != nil {
		return err
	}
	if err := captureRegtypeOutputValue(rows, catalog, path, value); err != nil {
		return err
	}
	if err := captureOperatorOutputValue(rows, catalog, path, value); err != nil {
		return err
	}
	if err := captureRegTSOutputValue(rows, catalog, path, value); err != nil {
		return err
	}
	if value.IsNull() || !value.Type().ContainsRegclass() {
		return nil
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := captureRegclassOutputValue(rows, catalog, path, value.CompositeField(i)); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		for i := 0; i < value.ArrayLen(); i++ {
			if err := captureRegclassOutputValue(rows, catalog, path, value.ArrayElement(i)); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().IsDomain() {
		var err error
		value, err = value.BaseValue()
		if err != nil {
			return err
		}
	}
	key, err := value.Text()
	if err != nil {
		return err
	}
	if _, exists := rows.regclassOutput[key]; exists {
		return nil
	}
	n, err := strconv.ParseUint(key, 10, 32)
	if err != nil {
		return err
	}
	if rows.regclassOutput == nil {
		rows.regclassOutput = make(map[string]string)
	}
	rows.regclassOutput[key] = regclassSpelling(catalog, path, ids.OID(n))
	rows.output.Regclass = rows
	return nil
}

// String output uses the same captured namespace presentation as finalRows,
// without changing the canonical OID datum or consulting a later live catalog.
func (s *StatementScope) FormatRegclass(id uint32) (string, error) {
	if s == nil || s.inquiry.commentCatalog.State() == nil {
		return "", fmt.Errorf("stuntgres: missing regclass output snapshot")
	}
	return regclassSpelling(s.inquiry.commentCatalog, s.inquiry.deparsePath, ids.OID(id)), nil
}

// regclassSpelling uses only the statement's captured catalog and relation path.
func regclassSpelling(view catalog.Read, path []ids.ObjectAddress, id ids.OID) string {
	return regclassSpellingAt(view, nil, path, id)
}

func regclassSpellingAt(view catalog.Read, reader definitionReader, path []ids.ObjectAddress, id ids.OID) string {
	if id == 0 {
		return "-"
	}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}
	descriptor, _ := definitionDescriptorAt(view, reader, address)
	if d, ok := descriptor.(*catalog.Relation); ok {
		text := regclassIdentifier(d.Name())
		if namedObjectVisibleAt(view, reader, path, d) {
			return text
		}
		namespace, _ := definitionDescriptorAt(view, reader, d.Namespace())
		return regclassIdentifier(namespace.Name()) + "." + text
	}
	return strconv.FormatUint(uint64(id), 10)
}

func regclassIdentifier(name string) string {
	if parser.IdentifierNeedsQuotes(name) {
		return catalog.QuoteIdentifier(name)
	}
	return name
}
