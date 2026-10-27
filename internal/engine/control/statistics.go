package control

import (
	"context"
	"slices"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
)

func collectStatisticsDump(ctx context.Context, c *dumpCollection, _ ids.ObjectAddress, raw catalog.Descriptor) error {
	d := raw.(*catalog.Statistics)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := (catalog.StatisticsProvider{}).Validate(c.CatalogView, d); err != nil {
		return err
	}
	c.Statistics = append(c.Statistics, d)
	c.Graph[dump.Event{OID: d.Address().ObjectID}] = []dump.Event{{OID: d.Relation.Address.ObjectID}}
	return nil
}
func statisticsDumpStatement(view catalog.Read, roles authorization.Read, d *catalog.Statistics) (dump.Statement, error) {
	if err := (catalog.StatisticsProvider{}).Validate(view, d); err != nil {
		return dump.Statement{}, err
	}
	owner, err := roles.Descriptor(d.Owner)
	if err != nil {
		return dump.Statement{}, err
	}
	qualified := func(d catalog.Descriptor) string {
		return catalog.QuoteIdentifier(view.MustNamespace(d.Namespace()).Name()) + "." + catalog.QuoteIdentifier(d.Name())
	}
	rel := view.MustRelation(d.Relation.Address)
	var out strings.Builder
	out.WriteString("CREATE STATISTICS " + qualified(d) + " (")
	for i, k := range d.Selection.Kinds() {
		if i > 0 {
			out.WriteString(", ")
		}
		switch k {
		case 'd':
			out.WriteString("ndistinct")
		case 'f':
			out.WriteString("dependencies")
		case 'm':
			out.WriteString("mcv")
		}
	}
	out.WriteString(") ON ")
	for i := range d.Selection.Len() {
		if i > 0 {
			out.WriteString(", ")
		}
		out.WriteString(catalog.QuoteIdentifier(rel.Columns[rel.IndexForAttribute(d.Selection.Key(i))].Name))
	}
	out.WriteString(" FROM " + qualified(rel) + ";\n")
	statement := dump.Statement{Owner: owner.Name, SQL: out.String()}
	if d.Owner.ID() != authorization.BootstrapPrincipalID {
		statement.PrivilegedCreation = true
		statement.FollowingSQL = "ALTER STATISTICS " + qualified(d) + " OWNER TO " + catalog.QuoteIdentifier(owner.Name) + ";\n"
	}
	return statement, nil
}

// Statistics follow the complete ordinary graph, including unrelated row and
// attribute-history events. Otherwise reminting OIDs during Restore can move a
// statistics declaration across a ready row event and change the next dump.
// Only statistics receive new edges; legacy object ordering is unchanged.
func (s *dumpScript) finishStatisticsGraph() {
	if len(s.Statistics) == 0 {
		return
	}
	slices.SortFunc(s.Statistics, func(a, b *catalog.Statistics) int {
		if order := strings.Compare(s.CatalogView.MustNamespace(a.Namespace()).Name(), s.CatalogView.MustNamespace(b.Namespace()).Name()); order != 0 {
			return order
		}
		return strings.Compare(a.Name(), b.Name())
	})
	first := dump.Event{OID: s.Statistics[0].Address().ObjectID}
	predecessors := make([]dump.Event, 0, len(s.Graph)-len(s.Statistics))
	for predecessor := range s.Graph {
		if _, statistics := s.CatalogView.Statistics(ids.ObjectAddress{ClassID: ids.StatisticExtClassID, ObjectID: predecessor.OID}); !statistics {
			predecessors = append(predecessors, predecessor)
		}
	}
	s.Graph[first] = predecessors
	for i := 1; i < len(s.Statistics); i++ {
		s.Graph.Add(dump.Event{OID: s.Statistics[i].Address().ObjectID}, dump.Event{OID: s.Statistics[i-1].Address().ObjectID})
	}
}
