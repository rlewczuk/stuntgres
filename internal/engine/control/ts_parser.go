package control

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
)

func collectParserDump(ctx context.Context, c *dumpCollection, _ ids.ObjectAddress, raw catalog.Descriptor) error {
	d := raw.(*catalog.TSParser)
	if err := (catalog.TSParserProvider{}).Validate(c.CatalogView, d); err != nil {
		return err
	}
	if d.Seeded() && d.Revision() == 1 {
		return nil
	}
	event := dump.Event{OID: d.Address().ObjectID}
	c.Graph[event] = nil
	for _, address := range d.Dependencies() {
		if err := ctx.Err(); err != nil {
			return err
		}
		oid, err := predecessor(c.CatalogView, address, d.Address())
		if err != nil {
			return err
		}
		if oid != 0 {
			c.Graph[event] = append(c.Graph[event], dump.Event{OID: oid})
		}
	}
	// Renaming the seeded default may free a name now used by a user parser.
	// Replay that identity change before creating any user parser.
	if !d.Seeded() {
		seed, ok := c.CatalogView.TSParser(ids.ObjectAddress{ClassID: ids.TSParserClassID, ObjectID: 3722})
		if ok && seed.Revision() != 1 {
			c.Graph[event] = append(c.Graph[event], dump.Event{OID: 3722})
		}
	}
	c.Parsers = append(c.Parsers, d)
	return nil
}

func parserDumpStatement(view catalog.Read, d *catalog.TSParser) (dump.Statement, error) {
	if err := (catalog.TSParserProvider{}).Validate(view, d); err != nil {
		return dump.Statement{}, err
	}
	qualified := func(namespace ids.ObjectAddress, name string) string {
		return catalog.QuoteIdentifier(view.MustNamespace(namespace).Name()) + "." + catalog.QuoteIdentifier(name)
	}
	var text strings.Builder
	if d.Seeded() {
		bootstrapNamespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
		current := qualified(bootstrapNamespace, "default")
		if d.Name() != "default" {
			fmt.Fprintf(&text, "ALTER TEXT SEARCH PARSER %s RENAME TO %s;\n", current, catalog.QuoteIdentifier(d.Name()))
			current = qualified(bootstrapNamespace, d.Name())
		}
		if d.Namespace().ObjectID != ids.PGCatalogNamespaceOID {
			fmt.Fprintf(&text, "ALTER TEXT SEARCH PARSER %s SET SCHEMA %s;\n", current, catalog.QuoteIdentifier(view.MustNamespace(d.Namespace()).Name()))
		}
	} else {
		fmt.Fprintf(&text, "CREATE TEXT SEARCH PARSER %s (", qualified(d.Namespace(), d.Name()))
		first := true
		for _, method := range [...]struct {
			name string
			oid  ids.OID
		}{{"START", d.Start}, {"GETTOKEN", d.Token}, {"END", d.End}, {"LEXTYPES", d.LexType}, {"HEADLINE", d.Headline}} {
			if method.oid == 0 {
				continue
			}
			procedure, ok := regobj.RoutineIdentityByOID(view, method.oid)
			if !ok {
				return dump.Statement{}, fmt.Errorf("stuntgres: missing parser callback dump authority")
			}
			if !first {
				text.WriteString(", ")
			}
			first = false
			fmt.Fprintf(&text, "%s = %s", method.name, qualified(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: procedure.Namespace}, procedure.Name))
		}
		text.WriteString(");\n")
	}
	return dump.Statement{Owner: "postgres", SQL: text.String(), PrivilegedCreation: true}, nil
}
