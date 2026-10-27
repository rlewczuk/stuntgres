package tablecmds

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func captureNDistinct(s *core.StatementScope, read catalog.Read, relation *catalog.Relation, columns []storage.StatisticsColumn) ([]storage.NDistinctInput, error) {
	typ, ok := read.LookupType(sqltype.PgNDistinct)
	if !ok {
		return nil, fmt.Errorf("missing pg_ndistinct type")
	}
	var inputs []storage.NDistinctInput
	for _, raw := range read.Descriptors() {
		d, ok := raw.(*catalog.Statistics)
		if !ok || d.Relation.Address != relation.Address() || !strings.Contains(d.Selection.Kinds(), "d") || d.TargetSet && d.Target == 0 {
			continue
		}
		if err := (catalog.StatisticsProvider{}).Validate(read, d); err != nil {
			return nil, err
		}
		selected := make([]storage.StatisticsColumn, 0, d.Selection.Len())
		for i := range d.Selection.Len() {
			for _, column := range columns {
				if column.Key.Attribute == d.Selection.Key(i) {
					selected = append(selected, column)
					break
				}
			}
		}
		if len(selected) != d.Selection.Len() {
			ns, _ := read.Namespace(d.Namespace())
			relns, _ := read.Namespace(relation.Namespace())
			s.Op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42P17", Message: fmt.Sprintf(`statistics object "%s.%s" could not be computed for relation "%s.%s"`, ns.Name(), d.Name(), relns.Name(), relation.Name()), Schema: relns.Name(), Table: relation.Name()})
			continue
		}
		inputs = append(inputs, storage.NDistinctInput{Key: storage.ExtendedStatisticsKey{Definition: read.Domain().Ref(read.Epoch(), d.Address())}, Definition: d, Columns: selected, Type: typ})
	}
	return inputs, nil
}
