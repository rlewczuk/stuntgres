package control

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
)

func collectConfigurationDump(ctx context.Context, c *dumpCollection, _ ids.ObjectAddress, raw catalog.Descriptor) error {
	d := raw.(*catalog.TSConfiguration)
	if err := (catalog.TSConfigurationProvider{}).Validate(c.CatalogView, d); err != nil {
		return err
	}
	if d.Seeded() && d.Revision() == 1 {
		return nil
	}
	event := dump.Event{OID: d.Address().ObjectID}
	c.Graph[event] = nil
	for _, a := range d.Dependencies() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if a.ClassID == 3601 || a.ClassID == ids.NamespaceClassID {
			continue
		}
		oid, err := predecessor(c.CatalogView, a, d.Address())
		if err != nil {
			return err
		}
		if oid != 0 {
			c.Graph[event] = append(c.Graph[event], dump.Event{OID: oid})
		}
	}
	if !d.Seeded() {
		seed, ok := c.CatalogView.TSConfiguration(ids.ObjectAddress{ClassID: ids.TSConfigurationClassID, ObjectID: 3748})
		if ok && seed.Revision() != 1 {
			c.Graph[event] = append(c.Graph[event], dump.Event{OID: 3748})
		}
	}
	c.Configurations = append(c.Configurations, d)
	return nil
}
func configurationDumpStatement(view catalog.Read, roles authorization.Read, d *catalog.TSConfiguration) (dump.Statement, error) {
	if err := (catalog.TSConfigurationProvider{}).Validate(view, d); err != nil {
		return dump.Statement{}, err
	}
	owner, err := roles.Descriptor(d.Owner)
	if err != nil {
		return dump.Statement{}, err
	}
	qualified := func(d catalog.Descriptor) string {
		return catalog.QuoteIdentifier(view.MustNamespace(d.Namespace()).Name()) + "." + catalog.QuoteIdentifier(d.Name())
	}
	name := qualified(d)
	var text strings.Builder
	if d.Seeded() {
		originalNS := view.MustNamespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}).Name()
		current := catalog.QuoteIdentifier(originalNS) + ".simple"
		if d.Name() != "simple" {
			fmt.Fprintf(&text, "ALTER TEXT SEARCH CONFIGURATION %s RENAME TO %s;\n", current, catalog.QuoteIdentifier(d.Name()))
			current = catalog.QuoteIdentifier(originalNS) + "." + catalog.QuoteIdentifier(d.Name())
		}
		if d.Namespace().ObjectID != ids.PGCatalogNamespaceOID {
			fmt.Fprintf(&text, "ALTER TEXT SEARCH CONFIGURATION %s SET SCHEMA %s;\n", current, catalog.QuoteIdentifier(view.MustNamespace(d.Namespace()).Name()))
		}
		text.WriteString("ALTER TEXT SEARCH CONFIGURATION " + name + " DROP MAPPING IF EXISTS FOR ")
		for i, t := range catalog.TextParserTokens() {
			if i > 0 {
				text.WriteString(", ")
			}
			text.WriteString(catalog.QuoteIdentifier(t.Alias))
		}
		text.WriteString(";\n")
	} else {
		parser := catalog.DefaultTextParser()
		parserNS := view.MustNamespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: parser.Namespace}).Name()
		fmt.Fprintf(&text, "CREATE TEXT SEARCH CONFIGURATION %s (PARSER = %s.%s);\n", name, catalog.QuoteIdentifier(parserNS), catalog.QuoteIdentifier(parser.Name))
	}
	tokens := catalog.TextParserTokens()
	for _, m := range d.Mappings {
		fmt.Fprintf(&text, "ALTER TEXT SEARCH CONFIGURATION %s ADD MAPPING FOR %s WITH ", name, catalog.QuoteIdentifier(tokens[m.TokenID-1].Alias))
		for i, a := range m.Dictionaries {
			if i > 0 {
				text.WriteString(", ")
			}
			dict, ok := view.TSDictionary(a)
			if !ok {
				return dump.Statement{}, fmt.Errorf("stuntgres: missing configuration dictionary")
			}
			text.WriteString(qualified(dict))
		}
		text.WriteString(";\n")
	}
	return dump.Statement{Owner: owner.Name, SQL: text.String(), FollowingSQL: "ALTER TEXT SEARCH CONFIGURATION " + name + " OWNER TO " + catalog.QuoteIdentifier(owner.Name) + ";\n", PrivilegedCreation: true}, nil
}
