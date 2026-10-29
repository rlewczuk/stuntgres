package control

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
)

// prepareLanguageReplay parks changed bootstrap identities before installations
// occupy their released names. Installed languages are parked by their existing
// recipe placement, never recreated as independent extension members.
func (s *dumpScript) prepareLanguageReplay() error {
	s.languageReplayNames = make(map[ids.ObjectAddress]string)
	reserved := map[string]bool{"internal": true, "c": true, "sql": true, "plpgsql": true}
	var languages []*catalog.Language
	for _, descriptor := range s.CatalogView.Descriptors() {
		if d, ok := descriptor.(*catalog.Language); ok {
			languages = append(languages, d)
			reserved[d.Name()] = true
		}
	}
	slices.SortFunc(languages, func(a, b *catalog.Language) int {
		return strings.Compare(a.Name(), b.Name())
	})
	serial := 0
	for _, d := range languages {
		original := ""
		switch d.Address().ObjectID {
		case 12:
			original = "internal"
		case 13:
			original = "c"
		case 14:
			original = "sql"
		}
		if d.Origin != nil {
			if !d.Origin.Valid(d.Address()) {
				return fmt.Errorf("stuntgres: invalid installed language replay origin")
			}
			original = d.Origin.Installation.Declarations[d.Origin.Declaration].Name()
		}
		if d.HostRegistration != nil {
			original = d.HostRegistration.Name()
		}
		if original == "" || original == d.Name() {
			continue
		}
		name := ""
		for {
			serial++
			name = "__stuntgres_language_" + strconv.Itoa(serial)
			if !reserved[name] {
				break
			}
		}
		reserved[name] = true
		s.languageReplayNames[d.Address()] = name
		if d.Origin == nil {
			s.providerStatements = append(s.providerStatements, "ALTER LANGUAGE "+catalog.QuoteIdentifier(original)+" RENAME TO "+catalog.QuoteIdentifier(name)+";\n")
		}
		s.languageStatements = append(s.languageStatements, "ALTER LANGUAGE "+catalog.QuoteIdentifier(name)+" RENAME TO "+catalog.QuoteIdentifier(d.Name())+";\n")
	}
	// Every released name has been freed before final names and aliases are
	// installed. Handler names resolve in the destination installation domain.
	for _, d := range languages {
		if !d.Procedural {
			continue
		}
		baseline := d.HostRegistration != nil || d.Origin != nil || d.Address().ObjectID >= 12 && d.Address().ObjectID <= 14
		if d.Origin != nil {
			released, ok := d.Origin.Installation.Declarations[d.Origin.Declaration].(*catalog.Language)
			if !ok {
				return fmt.Errorf("stuntgres: invalid language declaration recipe")
			}
			if d.Trusted == released.Trusted && d.Call == released.Call && d.Inline == released.Inline && d.Validator == released.Validator {
				continue
			}
		}
		statement, err := routine.LanguageDumpStatement(s.CatalogView, d, baseline)
		if err != nil {
			return err
		}
		s.languageStatements = append(s.languageStatements, statement)
	}
	return nil
}
