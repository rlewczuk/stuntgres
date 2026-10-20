package engine

import (
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Output presentation is captured once before returning Rows. The internal
// datum stays an OID, and retained Rows never consult later catalog authority.
func (e *Engine) captureRegclassOutput(op *session.Operation, rows *exec.FinalRows) error {
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
	roleOutput := capturedRegroleOutput{owner: e.catalog.owner(), roles: auth.Roles, quoteAll: rows.Output.QuoteAllIdentifiers}
	for _, value := range rows.Values {
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
	for _, row := range rows.Anonymous {
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
	if rows.Source != nil {
		for i := 0; i < rows.Count; i++ {
			index := i
			if rows.Selection != nil {
				index = rows.Selection[i]
			}
			row := rows.Source.Row(index)
			for _, slot := range rows.Slots {
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

func captureRegclassOutputValue(rows *exec.FinalRows, catalog catalog.Read, path []ids.ObjectAddress, value sqltype.Value) error {
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
	if _, exists := rows.RegclassOutput[key]; exists {
		return nil
	}
	n, err := strconv.ParseUint(key, 10, 32)
	if err != nil {
		return err
	}
	if rows.RegclassOutput == nil {
		rows.RegclassOutput = make(map[string]string)
	}
	rows.RegclassOutput[key] = regclassSpelling(catalog, path, ids.OID(n))
	rows.Output.Regclass = rows
	return nil
}

// String output uses the same captured namespace presentation as exec.FinalRows,
// without changing the canonical OID datum or consulting a later live catalog.
func (s *StatementScope) FormatRegclass(id uint32) (string, error) {
	if s == nil || s.inquiry.commentCatalog.State() == nil {
		return exec.DetachedPresentation{}.FormatRegclass(id)
	}
	return regclassSpelling(s.inquiry.commentCatalog, s.inquiry.deparsePath, ids.OID(id)), nil
}

// regclassSpelling uses only the statement's captured catalog and relation path.
func regclassSpelling(view catalog.Read, path []ids.ObjectAddress, id ids.OID) string {
	return regclassSpellingAt(view, nil, path, id)
}

func regclassSpellingAt(view catalog.Read, reader catalog.DefinitionReader, path []ids.ObjectAddress, id ids.OID) string {
	if id == 0 {
		return "-"
	}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}
	descriptor, _ := catalog.DescriptorAt(view, reader, address)
	if d, ok := descriptor.(*catalog.Relation); ok {
		text := regclassIdentifier(d.Name())
		if namedObjectVisibleAt(view, reader, path, d) {
			return text
		}
		namespace, _ := catalog.DescriptorAt(view, reader, d.Namespace())
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
