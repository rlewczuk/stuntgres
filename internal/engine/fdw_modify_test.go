package engine

import (
	"context"
	"errors"

	"stuntgres/internal/engine/catalog"
)

type fdwUnitScanWrapper struct{}

func (fdwUnitScanWrapper) GetForeignRelSize(context.Context, catalog.ForeignRequest) (catalog.ForeignRelSize, error) {
	return catalog.ForeignRelSize{}, nil
}

func (fdwUnitScanWrapper) GetForeignPaths(context.Context, catalog.ForeignRequest, catalog.ForeignRelSize) ([]catalog.ForeignPath, error) {
	return nil, nil
}

func (fdwUnitScanWrapper) GetForeignPlan(context.Context, catalog.ForeignRequest, catalog.ForeignPath) (catalog.ForeignPlan, error) {
	return catalog.ForeignPlan{}, nil
}

func (fdwUnitScanWrapper) BeginForeignScan(context.Context, catalog.ForeignRequest, catalog.ForeignPlan) (catalog.ForeignScan, error) {
	return nil, nil
}

type fdwUnitInsert struct {
	present bool
	calls   int
	err     error
}

func (p *fdwUnitInsert) ExecForeignInsert(context.Context, catalog.ForeignModifyRequest, any, catalog.ForeignRow) (catalog.ForeignRow, bool, error) {
	p.calls++
	return catalog.ForeignRow{}, p.present, p.err
}

type fdwUnitSingle struct {
	fdwUnitScanWrapper
	*fdwUnitInsert
}

type fdwUnitLifecycle struct {
	fdwUnitSingle
	tx                   *fdwUnitTransaction
	beginError, endError error
	begins, ends         int
	check                func()
}

func (p *fdwUnitLifecycle) ForeignTransactionCapabilities(context.Context, catalog.ForeignRequest) (catalog.ForeignTransactionCapabilities, error) {
	if p.check != nil {
		p.check()
	}
	return catalog.ForeignTransactionCapabilities{StatementRollback: true, TransactionRollback: true, Savepoints: true}, nil
}

func (p *fdwUnitLifecycle) BeginForeignTransaction(context.Context, catalog.ForeignRequest) (catalog.ForeignTransaction, error) {
	return p.tx, nil
}

func (p *fdwUnitLifecycle) BeginForeignModify(context.Context, catalog.ForeignModifyRequest) (any, error) {
	p.begins++
	if p.check != nil {
		p.check()
	}
	return "partial", p.beginError
}

func (p *fdwUnitLifecycle) EndForeignModify(ctx context.Context, _ catalog.ForeignModifyRequest, state any) error {
	p.ends++
	if p.check != nil {
		p.check()
	}
	if ctx.Err() != nil || state != "partial" {
		return errors.New("invalid cleanup context/state")
	}
	return p.endError
}
