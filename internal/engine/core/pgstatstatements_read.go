package core

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) ValidatePgStatStatements(n plan.InstalledPgStatStatementsExpr) error {
	s := h.scope()
	if err := s.Active(); err != nil {
		return err
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		return err
	}
	current, ok := auth.Catalog.Procedure(n.Ref.Address)
	if !ok || n.RoutineReference != n.Procedure || !exec.SameRoutineDeclaration(current, n.Procedure) {
		return fmt.Errorf("stuntgres: stale statement statistics declaration")
	}
	return extension.StockPgStatStatements.ValidateProcedure(auth.Catalog, current)
}

// Capture authorization coherently at read time, outside the collector leaf.
// Prepared readers therefore observe revocation without retaining privileges.
func (h *statementHost) PgStatStatements(show, info bool, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	s := h.scope()
	e := s.Engine()
	ctx := s.executionContext()
	e.Mu.Lock()
	auth, err := s.RoutineAuthorizationLocked()
	var allowed bool
	user := uint32(0)
	if err == nil {
		user = uint32(auth.State.CurrentUser.ID())
		stats, found := auth.Roles.Lookup("pg_read_all_stats")
		if !found {
			err = fmt.Errorf("stuntgres: missing pg_read_all_stats role")
		} else {
			allowed, err = authorization.HasRole(ctx, auth.Roles, &e.RoleStore.Domain, auth.State.CurrentUser.ID(), stats.Ref.ID(), authorization.RoleUsage)
		}
	}
	e.Mu.Unlock()
	if err != nil {
		return nil, err
	}
	store := e.Cluster.StatementStatistics
	if info {
		capture := store.Info()
		fields := make([]sqltype.Value, 2)
		fields[0], err = sqltype.Integer(result.CompositeMember(0).Usage.Type, capture.Dealloc)
		if err != nil {
			return nil, err
		}
		fields[1], err = sqltype.TimestampValue(result.CompositeMember(1).Usage.Type, capture.StatsReset.UnixMicro()-activityEpochUnixMicros)
		if err != nil {
			return nil, err
		}
		row, err := sqltype.OwnedCompositeValue(result, fields)
		if err != nil {
			return nil, err
		}
		return []sqltype.Value{row}, nil
	}
	snapshot := store.Snapshot()
	rows := make([]sqltype.Value, len(snapshot))
	for i, entry := range snapshot {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fields := make([]sqltype.Value, 52)
		for j := range fields {
			typ := result.CompositeMember(j).Usage.Type
			switch typ.ID() {
			case sqltype.Int8:
				fields[j], err = sqltype.Integer(typ, 0)
			case sqltype.Float8:
				fields[j], err = sqltype.Float(typ, 0)
			case sqltype.Numeric:
				fields[j], err = typ.Input(ctx, "0", -1)
			default:
				fields[j] = sqltype.Null(typ)
			}
			if err != nil {
				return nil, err
			}
		}
		fields[0], err = sqltype.OIDValue(result.CompositeMember(0).Usage.Type, entry.Key.UserID)
		if err != nil {
			return nil, err
		}
		fields[1], err = sqltype.OIDValue(result.CompositeMember(1).Usage.Type, entry.Key.DatabaseID)
		if err != nil {
			return nil, err
		}
		fields[2] = sqltype.Boolean(result.CompositeMember(2).Usage.Type, entry.Key.TopLevel)
		visible := allowed || entry.Key.UserID == user
		fields[3] = sqltype.Null(result.CompositeMember(3).Usage.Type)
		if visible {
			fields[3], err = sqltype.Integer(result.CompositeMember(3).Usage.Type, entry.Key.QueryID)
			if err != nil {
				return nil, err
			}
		}
		if show {
			text := "<insufficient privilege>"
			if visible {
				text = entry.Query
			}
			fields[4] = sqltype.String(result.CompositeMember(4).Usage.Type, text)
		}
		for _, phase := range []struct {
			base                          int
			count                         int64
			total, min, max, mean, stddev float64
		}{
			{5, entry.Planning.Count, entry.Planning.Total, entry.Planning.Min, entry.Planning.Max, entry.Planning.Mean, entry.Planning.Stddev()},
			{11, entry.Execution.Count, entry.Execution.Total, entry.Execution.Min, entry.Execution.Max, entry.Execution.Mean, entry.Execution.Stddev()},
		} {
			fields[phase.base], err = sqltype.Integer(result.CompositeMember(phase.base).Usage.Type, phase.count)
			if err != nil {
				return nil, err
			}
			for k, value := range [...]float64{phase.total, phase.min, phase.max, phase.mean, phase.stddev} {
				index := phase.base + k + 1
				fields[index], err = sqltype.Float(result.CompositeMember(index).Usage.Type, value)
				if err != nil {
					return nil, err
				}
			}
		}
		fields[17], err = sqltype.Integer(result.CompositeMember(17).Usage.Type, entry.Rows)
		if err != nil {
			return nil, err
		}
		fields[50], err = sqltype.TimestampValue(result.CompositeMember(50).Usage.Type, entry.StatsSince.UnixMicro()-activityEpochUnixMicros)
		if err != nil {
			return nil, err
		}
		fields[51], err = sqltype.TimestampValue(result.CompositeMember(51).Usage.Type, entry.MinMaxStatsSince.UnixMicro()-activityEpochUnixMicros)
		if err != nil {
			return nil, err
		}
		rows[i], err = sqltype.OwnedCompositeValue(result, fields)
		if err != nil {
			return nil, err
		}
	}
	return rows, nil
}
