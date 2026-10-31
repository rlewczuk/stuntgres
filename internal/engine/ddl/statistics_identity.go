package ddl

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// LookupStatistics uses the ordinary statistics search policy, including explicit
// namespace admission and exclusion of implicit temporary namespaces.
func LookupStatistics(h interface {
	SearchNamespaces(Authority, parser.ObjectName, ids.OID) ([]ids.ObjectAddress, error)
	AccessView(Authority) access.View
}, auth Authority, name parser.ObjectName) (*catalog.Statistics, error) {
	// Namespace lookup errors in these utilities are semantic, not parser-positioned.
	if len(name.Parts) == 2 {
		parts := [2]parser.Identifier{name.Parts[0], name.Parts[1]}
		parts[0].Pos = parser.Position{}
		name.Parts = parts[:]
	}
	path, err := h.SearchNamespaces(auth, name, ids.StatisticExtClassID)
	if err != nil {
		return nil, err
	}
	view := h.AccessView(auth)
	for _, ns := range path {
		a := view.Catalog.NameAddress(ids.NameKey{ClassID: ids.StatisticExtClassID, Namespace: ns, Name: name.Parts[len(name.Parts)-1].Text})
		if d, ok := view.Catalog.Statistics(a); ok {
			return d, nil
		}
	}
	return nil, sqlError("42704", fmt.Sprintf(`statistics object "%s" does not exist`, catalog.ObjectNameText(name)))
}

func StageRenameStatistics(h StatisticsHost, auth Authority, s *parser.AlterStatisticsRename) error {
	d, err := LookupStatistics(h, auth, s.Name)
	if err != nil {
		return err
	}
	if err = Check(h, auth, access.Requirement{Object: ObjectRef(h, auth, d.Address()), OwnerOnly: true, OwnerNoun: "statistics object"}); err != nil {
		return err
	}
	if err = h.CheckCreateIn(auth, ObjectRef(h, auth, d.Namespace())); err != nil {
		return err
	}
	next := *d
	next.ObjectName = s.NewName.Text
	if h.Stage().LookupName(catalog.DescriptorNameKey(&next)) != (ids.ObjectAddress{}) {
		return sqlError("42710", fmt.Sprintf(`statistics object "%s" already exists in schema "%s"`, next.Name(), h.AccessView(auth).Catalog.MustNamespace(d.Namespace()).Name()))
	}
	h.AddDomainBase(d)
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	if err = h.RecordDependencyPath(auth); err != nil {
		return err
	}
	return ReplaceCommand(h.Stage(), "ALTER STATISTICS", &next)
}

func StageStatisticsNamespace(h StatisticsHost, auth Authority, s *parser.AlterStatisticsSchema) error {
	d, err := LookupStatistics(h, auth, s.Name)
	if err != nil {
		return err
	}
	dest, auth, err := h.StatisticsSchemaNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{s.Schema, {Text: d.Name()}}})
	if err != nil {
		return err
	}
	// LookupCreationNamespace checks destination CREATE before the generic
	// mover's same-schema shortcut, namespace transition and source-owner check.
	if err = h.CheckCreateIn(auth, dest); err != nil {
		return err
	}
	h.AddDomainBase(d)
	if _, err = StageObjectNamespace(h, auth, d, dest.Address); err != nil {
		return err
	}
	RecordCommand(h.Stage(), "ALTER STATISTICS", d.Address())
	return nil
}
