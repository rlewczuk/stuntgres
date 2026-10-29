package control

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/host"
	"stuntgres/internal/engine/ids"
)

// prepareHostReplay diffs genuine source declarations against the immutable
// destination bootstrap inventory. No callback or source numeric OID is emitted.
func (s *dumpScript) prepareHostReplay() error {
	view := s.CatalogView
	if len(view.Owner().HostRuntimes) == 0 {
		return nil
	}
	var prefix []string
	reserved := make(map[host.Name]bool)
	for _, d := range view.Descriptors() {
		if p, ok := d.(*catalog.Procedure); ok {
			if ns, ok := view.Namespace(p.Namespace()); ok {
				reserved[host.Name{Schema: ns.Name(), Name: p.Name()}] = true
			}
		}
	}
	serial := 0
	absences := host.PairAbsences{}
	namespaces := make(map[string]bool)
	for _, runtime := range view.Owner().HostRuntimes {
		definition := runtime.Definition()
		namespaces[runtime.HandlerName().Schema] = true
		for _, c := range definition.Conversions {
			if c.FromSQL != nil {
				namespaces[c.FromName.Schema] = true
			}
			if c.ToSQL != nil {
				namespaces[c.ToName.Schema] = true
			}
		}
		baseline, handler := false, false
		directions := make(map[*host.Conversion][2]bool)
		for _, d := range view.Descriptors() {
			switch d := d.(type) {
			case *catalog.Language:
				baseline = baseline || d.HostRegistration == runtime
			case *catalog.Procedure:
				handler = handler || d.HostHandler == runtime
				if c := d.HostConversion; c != nil && c.Token.Runtime() == runtime {
					found := directions[c.Token]
					index := 1
					if c.From {
						index = 0
					}
					found[index] = true
					directions[c.Token] = found
				}
			}
		}
		if !baseline {
			prefix = append(prefix, "DROP LANGUAGE IF EXISTS "+catalog.QuoteIdentifier(runtime.Name())+" CASCADE;\n")
		}
		if !handler {
			original := runtime.HandlerName()
			parked := original
			for {
				serial++
				parked.Name = "__stuntgres_host_retired_" + strconv.Itoa(serial)
				if !reserved[parked] {
					break
				}
			}
			reserved[parked] = true
			prefix = append(prefix, "ALTER FUNCTION "+hostQualified(original)+"() RENAME TO "+catalog.QuoteIdentifier(parked.Name)+";\n")
			s.hostHandlerDrops = append(s.hostHandlerDrops, "DROP FUNCTION "+hostQualified(parked)+"() CASCADE;\n")
		}
		for _, conversion := range definition.Conversions {
			present := directions[runtime.Conversion(conversion.ID)]
			for _, d := range []struct {
				name                host.Name
				registered, present bool
			}{{conversion.FromName, conversion.FromSQL != nil, present[0]}, {conversion.ToName, conversion.ToSQL != nil, present[1]}} {
				if d.registered && !d.present {
					s.hostConverterDrops = append(s.hostConverterDrops, "DROP FUNCTION IF EXISTS "+hostQualified(d.name)+"(pg_catalog.internal) CASCADE;\n")
				}
			}
			if !conversion.Bootstrap {
				continue
			}
			for _, d := range view.Descriptors() {
				language, ok := d.(*catalog.Language)
				if !ok || language.Host != runtime {
					continue
				}
				for _, d := range view.Descriptors() {
					typ, ok := d.(*catalog.Type)
					if !ok || catalog.HostLogicalType(typ.SQLType) != conversion.Type {
						continue
					}
					pair := view.Get(view.NameAddress(ids.NameKey{ClassID: ids.TransformClassID, Name: fmt.Sprintf("%d,%d", typ.Address().ObjectID, language.Address().ObjectID)}))
					if pair != nil {
						continue
					}
					ns, ok := view.Namespace(typ.Namespace())
					if !ok {
						return fmt.Errorf("stuntgres: missing host pair type namespace")
					}
					absences.Pairs = append(absences.Pairs, host.PairAbsence{Provider: runtime.Provider(), ABI: runtime.ABI(), Language: language.Name(), Type: conversion.Type, SQLName: host.Name{Schema: ns.Name(), Name: typ.Name()}})
				}
			}
		}
	}
	// Configured namespaces can themselves be renamed or dropped. Retire only
	// actual source absences after every surviving provider has moved away.
	var namespaceDrops []string
	for name := range namespaces {
		if view.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name}).ObjectID == 0 {
			namespaceDrops = append(namespaceDrops, "DROP SCHEMA IF EXISTS "+catalog.QuoteIdentifier(name)+" CASCADE;\n")
		}
	}
	slices.Sort(namespaceDrops)
	s.hostHandlerDrops = append(s.hostHandlerDrops, namespaceDrops...)
	slices.SortFunc(absences.Pairs, func(a, b host.PairAbsence) int {
		for _, pair := range [][2]string{{a.Provider, b.Provider}, {a.Language, b.Language}, {a.SQLName.Schema, b.SQLName.Schema}, {a.SQLName.Name, b.SQLName.Name}} {
			if n := strings.Compare(pair[0], pair[1]); n != 0 {
				return n
			}
		}
		return 0
	})
	var err error
	s.hostAbsenceComment, err = absences.Comment()
	if err != nil {
		return err
	}
	s.providerStatements = append(prefix, s.providerStatements...)
	return nil
}
func hostQualified(name host.Name) string {
	return catalog.QuoteIdentifier(name.Schema) + "." + catalog.QuoteIdentifier(name.Name)
}
