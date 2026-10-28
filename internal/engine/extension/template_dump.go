package extension

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
)

func TemplateDumpStatement(view catalog.Read, d *catalog.TSTemplate) (dump.Statement, error) {
	if err := (CatalogProvider{Class:ids.TSTemplateClassID}).Validate(view, d); err != nil { return dump.Statement{}, err }
	qualified := func(object catalog.Descriptor) string { return catalog.QuoteIdentifier(view.MustNamespace(object.Namespace()).Name())+"."+catalog.QuoteIdentifier(object.Name()) }
	text := "CREATE TEXT SEARCH TEMPLATE "+qualified(d)+" ("
	if d.Init != (ids.ObjectAddress{}) {
		p, ok := view.Procedure(d.Init)
		if !ok { return dump.Statement{}, fmt.Errorf("stuntgres: missing template init") }
		text += "INIT = "+qualified(p)+", "
	}
	p, ok := view.Procedure(d.Lexize)
	if !ok { return dump.Statement{}, fmt.Errorf("stuntgres: missing template lexize") }
	return dump.Statement{SQL:text+"LEXIZE = "+qualified(p)+");\n", PrivilegedCreation:true}, nil
}
