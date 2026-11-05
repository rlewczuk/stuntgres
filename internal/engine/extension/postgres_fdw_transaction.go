package extension

import (
	"context"
	"errors"

	"stuntgres/internal/engine/catalog"
)

func (*PostgresFDWProvider) ForeignTransactionCapabilities(ctx context.Context, _ catalog.ForeignRequest) (catalog.ForeignTransactionCapabilities, error) {
	return catalog.ForeignTransactionCapabilities{StatementRollback: true, TransactionRollback: true, Savepoints: true}, ctx.Err()
}
func (*PostgresFDWProvider) BeginForeignTransaction(ctx context.Context, request catalog.ForeignRequest) (catalog.ForeignTransaction, error) {
	capability, ok := request.ExternalSQL.(catalog.PostgresFDWSession)
	if !ok {
		return nil, &catalog.ExternalSQLUnavailableError{Capability: "postgres_fdw execution lease"}
	}
	lease, err := capability.AcquirePostgresFDW(ctx)
	if err != nil {
		return nil, err
	}
	tx := &postgresFDWTransaction{lease: lease}
	isolation := "REPEATABLE READ"
	if request.Characteristics != nil && request.Characteristics.Isolation == "serializable" {
		isolation = "SERIALIZABLE"
	}
	if err := tx.command(ctx, "START TRANSACTION ISOLATION LEVEL "+isolation); err != nil {
		_ = lease.Release(context.WithoutCancel(ctx), false)
		return nil, err
	}
	return tx, nil
}

// There is one instance per existing FDW participant. The stock core key is the
// actual mapping, so aliases/PUBLIC callers cannot double-BEGIN/early-COMMIT.
type postgresFDWTransaction struct {
	lease  catalog.PostgresFDWLease
	cursor uint64
}

func (t *postgresFDWTransaction) connection() catalog.ExternalSQLConnection {
	return t.lease.Connection()
}
func (t *postgresFDWTransaction) command(ctx context.Context, query string) error {
	if t.lease == nil {
		return errors.New("stuntgres: postgres_fdw transaction is settled")
	}
	_, err := t.connection().Execute(ctx, query)
	return err
}
func (t *postgresFDWTransaction) BeginStatement(ctx context.Context) error {
	return t.command(ctx, "SAVEPOINT pgfdw_statement")
}
func (t *postgresFDWTransaction) EndStatement(ctx context.Context, success bool) error {
	if !success {
		if err := t.command(ctx, "ROLLBACK TO SAVEPOINT pgfdw_statement"); err != nil {
			return err
		}
	}
	return t.command(ctx, "RELEASE SAVEPOINT pgfdw_statement")
}
func (t *postgresFDWTransaction) finish(ctx context.Context, query string) error {
	if t.lease == nil {
		return nil
	}
	err := t.command(ctx, query)
	lease := t.lease
	t.lease = nil
	cleanup := lease.Release(context.WithoutCancel(ctx), err == nil)
	return errors.Join(err, cleanup)
}
func (t *postgresFDWTransaction) Commit(ctx context.Context) error { return t.finish(ctx, "COMMIT") }
func (t *postgresFDWTransaction) Rollback(ctx context.Context) error {
	return t.finish(context.WithoutCancel(ctx), "ROLLBACK")
}
func (t *postgresFDWTransaction) Savepoint(ctx context.Context, name string) error {
	return t.command(ctx, "SAVEPOINT "+catalog.QuoteIdentifier(name))
}
func (t *postgresFDWTransaction) RollbackTo(ctx context.Context, name string) error {
	return t.command(ctx, "ROLLBACK TO SAVEPOINT "+catalog.QuoteIdentifier(name))
}
func (t *postgresFDWTransaction) Release(ctx context.Context, name string) error {
	return t.command(ctx, "RELEASE SAVEPOINT "+catalog.QuoteIdentifier(name))
}
