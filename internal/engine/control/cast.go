package control

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func isDumpCast(d catalog.Descriptor) bool { _, ok := d.(*catalog.Cast); return ok }
func collectCastDump(_ context.Context, c *dumpCollection, _ ids.ObjectAddress, d catalog.Descriptor) error {
	return (catalog.CastProvider{}).Validate(c.CatalogView, d)
}
func (s *dumpScript) castEvents() error {
	for _, descriptor := range s.CatalogView.Descriptors() {
		d, ok := descriptor.(*catalog.Cast)
		if !ok || d.GeneratedRange != (ids.ObjectAddress{}) || d.Extension != (ids.ObjectAddress{}) {
			continue
		}
		event := dump.Event{OID: d.OID}
		s.Graph[event] = nil
		for _, a := range catalog.CastDependencies(d) {
			oid, err := predecessor(s.CatalogView, a, d.Address())
			if err != nil {
				return err
			}
			if oid != 0 {
				s.Graph.Add(event, dump.Event{OID: oid})
			}
		}
	}
	return nil
}
func (s *dumpScript) castDeclarations() error {
	for _, descriptor := range s.CatalogView.Descriptors() {
		d, ok := descriptor.(*catalog.Cast)
		if !ok || d.GeneratedRange != (ids.ObjectAddress{}) || d.Extension != (ids.ObjectAddress{}) {
			continue
		}
		statement, err := castDumpStatement(s.CatalogView, d)
		if err != nil {
			return err
		}
		s.declarations[d.OID] = statement
	}
	return nil
}
func castDumpStatement(view catalog.Read, d *catalog.Cast) (dump.Statement, error) {
	source, sourceOK := view.LookupType(d.Source)
	target, targetOK := view.LookupType(d.Target)
	if !sourceOK || !targetOK {
		return dump.Statement{}, fmt.Errorf("missing cast replay endpoint")
	}
	x, err := (Presenter{}).ColumnDeclaration(view, catalog.Column{Usage: source.Usage()})
	if err != nil {
		return dump.Statement{}, err
	}
	y, err := (Presenter{}).ColumnDeclaration(view, catalog.Column{Usage: target.Usage()})
	if err != nil {
		return dump.Statement{}, err
	}
	text := "CREATE CAST (" + x + " AS " + y + ") "
	switch d.Method {
	case "b":
		text += "WITHOUT FUNCTION"
	case "i":
		text += "WITH INOUT"
	case "f":
		p, ok := view.Procedure(d.Function)
		if !ok {
			return dump.Statement{}, fmt.Errorf("missing cast replay routine")
		}
		text += "WITH FUNCTION " + catalog.QuoteIdentifier(view.Get(p.Namespace()).Name()) + "." + catalog.QuoteIdentifier(p.Name()) + "("
		for i, word := range strings.Split(string(p.Signature), ",") {
			if i != 0 {
				text += ", "
			}
			id, err := strconv.ParseUint(word, 10, 32)
			if err != nil {
				return dump.Statement{}, err
			}
			typ, ok := view.LookupType(sqltype.ID(id))
			if !ok {
				return dump.Statement{}, fmt.Errorf("missing cast replay formal")
			}
			name, err := (Presenter{}).ColumnDeclaration(view, catalog.Column{Usage: typ.Usage()})
			if err != nil {
				return dump.Statement{}, err
			}
			text += name
		}
		text += ")"
	default:
		return dump.Statement{}, fmt.Errorf("invalid cast replay method")
	}
	if d.Context == "a" {
		text += " AS ASSIGNMENT"
	} else if d.Context == "i" {
		text += " AS IMPLICIT"
	}
	return dump.Statement{Owner: "postgres", SQL: text + ";\n"}, nil
}
