package engine

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func regObjectSpelling(view CatalogRead, path []ids.ObjectAddress, class, id ids.OID) string {
	if id == 0 {
		return "-"
	}
	d := view.Get(ids.ObjectAddress{ClassID: class, ObjectID: id})
	if d == nil {
		return strconv.FormatUint(uint64(id), 10)
	}
	name := regclassIdentifier(d.Name())
	if class == ids.NamespaceClassID || visibleCollation(view, path, d) {
		return name
	}
	return regclassIdentifier(view.Get(d.Namespace()).Name()) + "." + name
}

func (s *StatementScope) FormatRegcollation(id uint32) (string, error) {
	if s == nil || s.commentCatalog.State() == nil {
		return "", fmt.Errorf("stuntgres: missing regcollation output snapshot")
	}
	return regObjectSpelling(s.commentCatalog, s.collationPath, ids.CollationClassID, ids.OID(id)), nil
}
func (s *StatementScope) FormatRegnamespace(id uint32) (string, error) {
	if s == nil || s.commentCatalog.State() == nil {
		return "", fmt.Errorf("stuntgres: missing regnamespace output snapshot")
	}
	return regObjectSpelling(s.commentCatalog, nil, ids.NamespaceClassID, ids.OID(id)), nil
}
func (r *finalRows) FormatRegcollation(id uint32) (string, error) {
	return r.regObjectText(ids.CollationClassID, id)
}
func (r *finalRows) FormatRegnamespace(id uint32) (string, error) {
	return r.regObjectText(ids.NamespaceClassID, id)
}
func (r *finalRows) regObjectText(class ids.OID, id uint32) (string, error) {
	text, exists := r.regobjectOutput[ids.ObjectAddress{ClassID: class, ObjectID: ids.OID(id)}]
	if !exists {
		return "", fmt.Errorf("stuntgres: missing collation/namespace output snapshot")
	}
	return text, nil
}

func captureRegObjectOutputValue(rows *finalRows, view CatalogRead, path []ids.ObjectAddress, value sqltype.Value) error {
	if value.IsNull() || !value.Type().ContainsRegObject() {
		return nil
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := captureRegObjectOutputValue(rows, view, path, value.CompositeField(i)); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		for i := range value.ArrayLen() {
			if err := captureRegObjectOutputValue(rows, view, path, value.ArrayElement(i)); err != nil {
				return err
			}
		}
		return nil
	}
	address, err := catalogAliasAddress(value)
	if err != nil {
		return err
	}
	if _, exists := rows.regobjectOutput[address]; exists {
		return nil
	}
	if rows.regobjectOutput == nil {
		rows.regobjectOutput = make(map[ids.ObjectAddress]string)
	}
	rows.regobjectOutput[address] = regObjectSpelling(view, path, address.ClassID, address.ObjectID)
	rows.output.Regcollation, rows.output.Regnamespace = rows, rows
	return nil
}

func (e *Engine) captureRegObjectRowsValueLocked(rows *finalRows, auth AuthorizationView, path *[]ids.ObjectAddress, value sqltype.Value) error {
	if value.IsNull() || !value.Type().ContainsRegObject() {
		return nil
	}
	if value.Type().ContainsRegcollation() && *path == nil {
		var err error
		*path, err = e.lookupPath(auth, collationLookup)
		if err != nil {
			return err
		}
	}
	return captureRegObjectOutputValue(rows, auth.catalog, *path, value)
}
