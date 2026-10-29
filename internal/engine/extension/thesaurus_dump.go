package extension

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// ThesaurusReplayNormalizer resolves the current logical option for the
// host-authorized committed dump snapshot. An unqualified option uses its
// accepted initialization namespace, not an arbitrary pool session's path.
func ThesaurusReplayNormalizer(view catalog.Read, d *catalog.TSDictionary) (*catalog.TSDictionary, error) {
	if !IsThesaurus(view, d) {
		return nil, nil
	}
	_, logical, err := thesaurusOptions(d.Options)
	if err != nil {
		return nil, err
	}
	name, err := parser.ParseRegclassName(logical)
	if err != nil {
		return nil, dictionaryError("42602", "invalid name syntax")
	}
	namespace := d.NormalizerNamespace
	if len(name.Parts) == 2 {
		namespace = view.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name.Parts[0].Text})
		if namespace == (ids.ObjectAddress{}) {
			return nil, dictionaryError("3F000", fmt.Sprintf(`schema "%s" does not exist`, name.Parts[0].Text))
		}
	} else if len(name.Parts) != 1 {
		return nil, dictionaryError("42602", "invalid name syntax")
	}
	address := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: namespace, Name: name.Parts[len(name.Parts)-1].Text})
	normalizer, ok := view.TSDictionary(address)
	if !ok {
		return nil, dictionaryError("42704", fmt.Sprintf(`text search dictionary "%s" does not exist`, logical))
	}
	if IsThesaurus(view, normalizer) {
		return nil, dictionaryError("XX000", "forbidden call of thesaurus or nested call")
	}
	if err := validateDictionary(view, normalizer); err != nil {
		return nil, err
	}
	return normalizer, nil
}
