package core

import (
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/statistics"
)

type statsFunctionFrame struct {
	parent *statsFunctionFrame
	started time.Time
	usage statistics.FunctionUsage
}

// Native dispatch is not a language: stock internal routines are never tracked,
// whereas applicable extension C routines are tracked under all.
func statsFunctionLanguage(d *catalog.Procedure) string {
	if d.Display.Language == catalog.InternalRoutineLanguage {
		return "internal"
	}
	if d.Display.Language == catalog.CRoutineLanguage || d.Host != nil {
		return "c"
	}
	if d.RoutineDefinition().Kind == proc.PLpgSQLBody {
		return "pl"
	}
	return "sql"
}

// beginStatsFunction returns nil before clock sampling or frame allocation when
// collection is off. Callers have installed the admitted body's environment.
func (s *StatementScope) beginStatsFunction(ref ids.ObjectRef, language string, planning bool) *statsFunctionFrame {
	if planning || language == "internal" {
		return nil
	}
	_, _, policy := s.Op.EffectiveSettings().StatsConfiguration()
	if policy.Functions != "all" && (policy.Functions != "pl" || language != "pl") {
		return nil
	}
	usage, ok := PayloadOf(s.Op.Session).statsWriter.BeginFunction(&s.Engine().Statistics, statistics.Key{Object: ref, Category: statistics.Function})
	if !ok {
		return nil
	}
	f := &statsFunctionFrame{parent: s.statsFunction, started: time.Now(), usage: usage}
	s.statsFunction = f
	return f
}

func (s *StatementScope) endStatsFunction(f *statsFunctionFrame, completed bool) {
	if f == nil {
		return
	}
	s.statsFunction = f.parent
	if completed {
		PayloadOf(s.Op.Session).statsWriter.EndFunction(&s.Engine().Statistics, f.usage, time.Since(f.started))
	}
}
