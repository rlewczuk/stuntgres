package regobj

import (
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func CaptureClassOutputValue(rows *exec.FinalRows, catalog catalog.Read, path []ids.ObjectAddress, value sqltype.Value) error {
	if err := CaptureProcOutputValue(rows, catalog, path, value); err != nil {
		return err
	}
	if err := captureRegtypeOutputValue(rows, catalog, path, value); err != nil {
		return err
	}
	if err := CaptureOperatorOutputValue(rows, catalog, path, value); err != nil {
		return err
	}
	if err := CaptureTextSearchOutputValue(rows, catalog, path, value); err != nil {
		return err
	}
	if value.IsNull() || !value.Type().ContainsRegclass() {
		return nil
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := CaptureClassOutputValue(rows, catalog, path, value.CompositeField(i)); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		for i := 0; i < value.ArrayLen(); i++ {
			if err := CaptureClassOutputValue(rows, catalog, path, value.ArrayElement(i)); err != nil {
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
	rows.RegclassOutput[key] = ClassSpelling(catalog, path, ids.OID(n))
	rows.Output.Regclass = rows
	return nil
}

// ClassSpelling uses only the statement's captured catalog and relation path.
func ClassSpelling(view catalog.Read, path []ids.ObjectAddress, id ids.OID) string {
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

type ClassOutput struct {
	View       catalog.Read
	Path       []ids.ObjectAddress
	definition catalog.DefinitionReader
}

func (r ClassOutput) FormatRegclass(id uint32) (string, error) {
	return regclassSpellingAt(r.View, r.definition, r.Path, ids.OID(id)), nil
}
