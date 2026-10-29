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
	// Current extension membership is restored only after ordinary creators.
	// It never replaces this configuration's parser/mapping dependency event.
	if d.Seeded() && d.Revision() == 1 {
		return nil
	}
	event := dump.Event{OID: d.Address().ObjectID}
	c.Graph[event] = nil
	for _, a := range d.Dependencies() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if a.ClassID == ids.NamespaceClassID {
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
	parser := view.MustTSParser(d.Parser)
	var tokens [23]struct{ Alias, Description string }
	if d.Seeded() || len(d.Mappings) > 0 {
		runtime, err := catalog.ResolveParserRuntime(view, parser)
		if err != nil {
			return dump.Statement{}, err
		}
		tokens, err = runtime.TokenTypes(context.Background())
		if err != nil {
			return dump.Statement{}, err
		}
	}
	var text strings.Builder
	if d.Seeded() {
		text.WriteString("ALTER TEXT SEARCH CONFIGURATION " + name + " DROP MAPPING IF EXISTS FOR ")
		for i, t := range tokens {
			if i > 0 {
				text.WriteString(", ")
			}
			text.WriteString(catalog.QuoteIdentifier(t.Alias))
		}
		text.WriteString(";\n")
	} else {
		fmt.Fprintf(&text, "CREATE TEXT SEARCH CONFIGURATION %s (PARSER = %s);\n", name, qualified(parser))
	}
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
