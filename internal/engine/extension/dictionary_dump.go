package extension

import (
	"fmt"
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
	text := "CREATE TEXT SEARCH DICTIONARY " + qualified(d) + " (TEMPLATE = " + qualified(template)
	if len(d.Options) > 0 {
		text += ", " + catalog.DictionaryOptionsText(d.Options)
	}
	return dump.Statement{
		Owner:              owner.Name,
		SQL:                text + ");\n",
		FollowingSQL:       "ALTER TEXT SEARCH DICTIONARY " + qualified(d) + " OWNER TO " + catalog.QuoteIdentifier(owner.Name) + ";\n",
		PrivilegedCreation: true,
	}, nil
}
