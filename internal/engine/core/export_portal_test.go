package core

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/copyfmt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

// ProtocolPortal names protocolPortal.
type ProtocolPortal = protocolPortal

// RetireProtocolPortalsAfter calls retireProtocolPortalsAfter.
func RetireProtocolPortalsAfter(t *session.TransactionOwner, frontier uint64) {
	retireProtocolPortalsAfter(t, frontier)
}

// ScopeBeginProtocolSimplePortal calls x.beginProtocolSimplePortal.
func ScopeBeginProtocolSimplePortal(x *StatementScope) error {
	return x.beginProtocolSimplePortal()
}

// ScopeCaptureProtocolOutput calls x.captureProtocolOutput.
func ScopeCaptureProtocolOutput(x *StatementScope, rows *exec.FinalRows, values []sqltype.Value, catalog catalog.Read) error {
	return x.captureProtocolOutput(rows, values, catalog)
}

// ScopeProtocol calls x.protocol.
func ScopeProtocol(x *StatementScope) *protocolScope {
	return x.protocol()
}

// ScopeProtocolOrZero calls x.protocolOrZero.
func ScopeProtocolOrZero(x *StatementScope) protocolScope {
	return x.protocolOrZero()
}

// ScopeProtocolSimpleIO calls x.protocolSimpleIO.
func ScopeProtocolSimpleIO(x *StatementScope, call func() error) error {
	return x.protocolSimpleIO(call)
}

// ScopeStartProtocolCopy calls x.startProtocolCopy.
func ScopeStartProtocolCopy(x *StatementScope, hooks CopyProtocolHooks, from bool, options copyfmt.Options, columns int) error {
	return x.startProtocolCopy(hooks, from, options, columns)
}

// PortalFields holds the protocolPortal fields tests set.
type PortalFields struct {
	Tx       ids.TxRef
	Born     uint64
	Entry    PreparedDefinition
	Context  session.ProtocolContext
	Columns  []catalog.Column
	Producer *sqlCursor
	Rows     exec.RowResult
	Started  bool
}

// NewPortal builds a protocolPortal from f.
func NewPortal(f PortalFields) *protocolPortal {
	p := newProtocolPortal(f.Entry, f.Tx)
	p.Birth, p.context, p.columns, p.producer, p.rows, p.started = f.Born, f.Context, f.Columns, f.Producer, f.Rows, f.Started
	if f.Producer != nil {
		p.Resource = &f.Producer.PreparedResource
		p.Resource.Closer = p
	}
	return p
}

// PortalBornRef returns &x.Birth.
func PortalBornRef(x *protocolPortal) *uint64 { return &x.Birth }

// PortalClose calls x.close.
func PortalClose(x *protocolPortal, ctx context.Context) error {
	return x.close(ctx)
}

// PortalColumns returns x.columns.
func PortalColumns(x *protocolPortal) []catalog.Column { return x.columns }

// PortalContext returns x.context.
func PortalContext(x *protocolPortal) session.ProtocolContext { return x.context }

// PortalEntryRef returns &x.Entry.
func PortalEntryRef(x *protocolPortal) *PreparedDefinition { return &x.Entry }

// PortalNext calls x.next.
func PortalNext(x *protocolPortal, ctx context.Context, values []sqltype.Value) (bool, error) {
	return x.next(ctx, values)
}

// PortalProducer returns x.producer.
func PortalProducer(x *protocolPortal) *sqlCursor { return x.producer }

// PortalRetired returns x.Retired.
func PortalRetired(x *protocolPortal) bool { return x.Retired }

// PortalRow returns x.row.
func PortalRow(x *protocolPortal) []sqltype.Value { return x.row }

// PortalRows returns x.rows.
func PortalRows(x *protocolPortal) exec.RowResult { return x.rows }

// PortalScratch returns x.scratch.
func PortalScratch(x *protocolPortal) []byte { return x.scratch }

// PortalTx returns x.tx.
func PortalTx(x *protocolPortal) ids.TxRef { return x.Transaction }

// PortalTxRef returns &x.tx.
func PortalTxRef(x *protocolPortal) *ids.TxRef { return &x.Transaction }

// SetPortalRows sets x.rows.
func SetPortalRows(x *protocolPortal, v exec.RowResult) { x.rows = v }

// SetPortalTx sets x.tx.
func SetPortalTx(x *protocolPortal, v ids.TxRef) { x.Transaction = v }

// ProtocolScopeBind returns x.bind.
func ProtocolScopeBind(x protocolScope) *protocolPortal { return x.bind }

// ProtocolScopeCommandRef returns &x.command.
func ProtocolScopeCommandRef(x *protocolScope) *bool { return &x.command }

// ProtocolScopeSimpleRef returns &x.simple.
func ProtocolScopeSimpleRef(x *protocolScope) *bool { return &x.simple }

// SetProtocolScopeCommand sets x.command.
func SetProtocolScopeCommand(x *protocolScope, v bool) { x.command = v }

// SetProtocolScopeBind sets x.bind.
func SetProtocolScopeBind(x *protocolScope, v *protocolPortal) { x.bind = v }

func NewProtocolPortal(entry PreparedDefinition, tx ids.TxRef) *protocolPortal {
	return newProtocolPortal(entry, tx)
}

func ProtocolPortalOf(binding *PreparedBinding) *protocolPortal {
	return protocolPortalOf(binding)
}

func PortalProducerRef(p *protocolPortal) **sqlCursor             { return &p.producer }
func PortalRowsRef(p *protocolPortal) *exec.RowResult             { return &p.rows }
func PortalColumnsRef(p *protocolPortal) *[]catalog.Column        { return &p.columns }
func PortalStartedRef(p *protocolPortal) *bool                    { return &p.started }
func PortalContextRef(p *protocolPortal) *session.ProtocolContext { return &p.context }

func ScopeInstallProtocolProducer(s *StatementScope, root plan.Node, source *exec.Source, values []sqltype.Value) (Result, error) {
	return s.installProtocolProducer(root, source, values)
}
