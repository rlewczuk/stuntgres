package ddl

import (
	"errors"
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
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
	d, err := LookupStatistics(h, auth, s.Name)
	if err != nil {
		var diagnostic *sqlstate.Error
		if s.IfExists && errors.As(err, &diagnostic) && diagnostic.Code == "42704" {
			Notice(h, "00000", diagnostic.Message+", skipping")
			return nil
		}
		return err
	}
	if err := Check(h, auth, access.Requirement{Object: ObjectRef(h, auth, d.Address()), OwnerOnly: true}); err != nil {
		var diagnostic *sqlstate.Error
		if errors.As(err, &diagnostic) && diagnostic.Code == "42501" {
			return sqlError("42501", "must be owner of statistics object "+catalog.ObjectNameText(s.Name))
		}
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
	return ReplaceCommand(h.Stage(), "ALTER STATISTICS", &next)
}
