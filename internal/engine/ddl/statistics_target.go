package ddl

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func StageAlterStatisticsTarget(h StatisticsHost, auth Authority, s *parser.AlterStatisticsTarget) error {
	target := s.Target
	if target < -1 {
		return sqlError("22023", fmt.Sprintf("statistics target %d is too low", target))
	}
	if target > 10000 {
		h.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "lowering statistics target to 10000"})
		target = 10000
	}
	semantic := s.Name
	if len(s.Name.Parts) == 2 {
		parts := append([]parser.Identifier(nil), s.Name.Parts...)
		parts[0].Pos = parser.Position{}
		semantic.Parts = parts
	}
	path, err := h.SearchNamespaces(auth, semantic, ids.StatisticExtClassID)
	if err != nil {
		return err
	}
	view := h.AccessView(auth)
	var d *catalog.Statistics
	for _, ns := range path {
		address := view.Catalog.NameAddress(ids.NameKey{ClassID: ids.StatisticExtClassID, Namespace: ns, Name: s.Name.Parts[len(s.Name.Parts)-1].Text})
		if found, ok := view.Catalog.Statistics(address); ok {
			d = found
			break
		}
	}
	if d == nil {
		message := fmt.Sprintf(`statistics object "%s" does not exist`, catalog.ObjectNameText(s.Name))
		if s.IfExists {
			Notice(h, "00000", message+", skipping")
			return nil
		}
		return sqlError("42704", message)
	}
	if err := Check(h, auth, access.Requirement{Object: ObjectRef(h, auth, d.Address()), OwnerOnly: true}); err != nil {
		return err
	}
	h.AddDomainBase(d)
	next := *d
	next.TargetSet = target != -1
	next.Target = 0
	if next.TargetSet {
		next.Target = int16(target)
	}
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	_ = h.RecordDependencyPath(auth)
	return h.Stage().Replace(&next)
}
