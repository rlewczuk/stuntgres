package control

import (
	"context"
	"stuntgres/internal/engine/partition"
)

func emitDumpPendingDetaches(ctx context.Context, s *dumpScript, w *dumpWriter) error {
	for _, record := range partition.PendingDetachRecords(s.OriginalCatalog) {
		if err := ctx.Err(); err != nil {
			return err
		}
		comment, err := record.Comment()
		if err != nil {
			return err
		}
		if err := w.emit(comment); err != nil {
			return err
		}
	}
	return nil
}
