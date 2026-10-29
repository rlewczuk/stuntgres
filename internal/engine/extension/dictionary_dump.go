package extension

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
)

func DictionaryDumpStatement(view catalog.Read, roles authorization.Read, d *catalog.TSDictionary) (dump.Statement, error) {
	if err := (CatalogProvider{Class: d.Address().ClassID}).Validate(view, d); err != nil {
		return dump.Statement{}, err
	}
	owner, err := roles.Descriptor(d.Owner)
	if err != nil {
		return dump.Statement{}, err
	}
	template, ok := view.TSTemplate(d.Template)
	if !ok {
		return dump.Statement{}, fmt.Errorf("stuntgres: missing dictionary template")
	}
	qualified := func(object catalog.Descriptor) string {
		return catalog.QuoteIdentifier(view.MustNamespace(object.Namespace()).Name()) + "." + catalog.QuoteIdentifier(object.Name())
	}
	options := d.Options
	normalizer, err := ThesaurusReplayNormalizer(view, d)
	if err != nil {
		return dump.Statement{}, err
	}
	if normalizer != nil {
		options = slices.Clone(options)
		for i := range options {
			if options[i].Name == "dictionary" {
				options[i].Value = qualified(normalizer)
			}
		}
	}
	text := "CREATE TEXT SEARCH DICTIONARY " + qualified(d) + " (TEMPLATE = " + qualified(template)
	if len(options) > 0 {
		text += ", " + catalog.DictionaryOptionsText(options)
	}
	return dump.Statement{
		Owner:              owner.Name,
		SQL:                text + ");\n",
		FollowingSQL:       "ALTER TEXT SEARCH DICTIONARY " + qualified(d) + " OWNER TO " + catalog.QuoteIdentifier(owner.Name) + ";\n",
		PrivilegedCreation: true,
	}, nil
}
