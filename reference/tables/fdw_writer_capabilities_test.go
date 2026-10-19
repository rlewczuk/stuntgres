package reference_test

import (
	"context"

	"stuntgres"
)

type fdwWriterBase struct {
	stuntgres.ForeignDataWrapper
	stuntgres.ForeignTransactionProvider
}
type fdwSingleWriter struct {
	fdwWriterBase
	stuntgres.ForeignInserter
}
type fdwBatchWithoutSize struct {
	fdwSingleWriter
	stuntgres.ForeignBatchInserter
}
type fdwSizeWithoutBatch struct {
	fdwSingleWriter
	stuntgres.ForeignBatchSizer
}
type fdwUpdateOnlyWriter struct {
	fdwWriterBase
	stuntgres.ForeignUpdater
	stuntgres.ForeignUpdateTargetProvider
}
type fdwUncontractedWriter struct {
	stuntgres.ForeignDataWrapper
	stuntgres.ForeignInserter
}
type fdwNoSavepointProvider struct {
	stuntgres.ForeignTransactionProvider
}

func (p fdwNoSavepointProvider) ForeignTransactionCapabilities(ctx context.Context, r stuntgres.ForeignRequest) (stuntgres.ForeignTransactionCapabilities, error) {
	return stuntgres.ForeignTransactionCapabilities{StatementRollback: true, TransactionRollback: true}, nil
}
func (w *fdwSQLWriter) registerVariants(d *stuntgres.Driver) error {
	base := fdwWriterBase{ForeignDataWrapper: w, ForeignTransactionProvider: w}
	single := fdwSingleWriter{fdwWriterBase: base, ForeignInserter: w}
	variants := []struct {
		name    string
		wrapper stuntgres.ForeignDataWrapper
	}{
		{"fdw_single_writer_handler", single},
		{"fdw_batch_without_size_handler", fdwBatchWithoutSize{fdwSingleWriter: single, ForeignBatchInserter: w}},
		{"fdw_size_without_batch_handler", fdwSizeWithoutBatch{fdwSingleWriter: single, ForeignBatchSizer: w}},
		{"fdw_update_only_handler", fdwUpdateOnlyWriter{fdwWriterBase: base, ForeignUpdater: w, ForeignUpdateTargetProvider: w}},
		{"fdw_uncontracted_writer_handler", fdwUncontractedWriter{ForeignDataWrapper: w, ForeignInserter: w}},
		{"fdw_no_savepoint_handler", fdwSingleWriter{fdwWriterBase: fdwWriterBase{ForeignDataWrapper: w, ForeignTransactionProvider: fdwNoSavepointProvider{w}}, ForeignInserter: w}},
	}
	for _, v := range variants {
		if err := d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Schema: "public", Name: v.name}, Wrapper: v.wrapper}); err != nil {
			return err
		}
	}
	return nil
}
