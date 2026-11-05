package core

import (
	"fmt"
	"time"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) ValidatePgStatStatementsReset(n plan.InstalledPgStatStatementsResetExpr) error {
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
	if !ok || n.Procedure == nil || n.Ref.Domain() != auth.Catalog.Owner().Domain || n.Ref.Epoch == 0 || n.Ref.Epoch > auth.Catalog.Epoch() || n.RoutineReference != n.Procedure || !exec.SameRoutineDeclaration(current, n.Procedure) {
		return fmt.Errorf("stuntgres: stale statement statistics reset declaration")
	}
	if err := extension.StockPgStatStatements.ValidateProcedure(auth.Catalog, current); err != nil {
		return err
	}
	if current.Origin.Declaration != 2 {
		return fmt.Errorf("stuntgres: mismatched statement statistics reset declaration")
	}
	return nil
}

func (h *statementHost) PgStatStatementsReset(user, database uint32, query int64, minmax bool, result *sqltype.Descriptor) (sqltype.Value, error) {
	s := h.scope()
	if err := s.Active(); err != nil {
		return sqltype.Value{}, err
	}
	if err := s.executionContext().Err(); err != nil {
		return sqltype.Value{}, err
	}
	// Shared routine entry supplies current EXECUTE admission. Capture one real
	// microsecond instant outside the collector leaf and return that same value.
	now := time.Now().UTC().Truncate(time.Microsecond)
	value, err := sqltype.TimestampValue(result, now.UnixMicro()-activityEpochUnixMicros)
	if err != nil {
		return sqltype.Value{}, err
	}
	s.Engine().Cluster.StatementStatistics.Reset(statistics.StatementFilter{UserID: user, DatabaseID: database, QueryID: query}, minmax, now)
	return value, nil
}
