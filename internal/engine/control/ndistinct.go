package control

import (
	"context"
	"stuntgres/internal/engine/storage"
)

func emitDumpExtendedStatistics(ctx context.Context, s *dumpScript, w *dumpWriter) error {
	for _, record := range s.ExtendedStatistics {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := storage.EncodeExtendedStatisticsSnapshot(record)
		if err != nil {
			return err
		}
		if err := w.emit(line); err != nil {
			return err
		}
	}
	return ctx.Err()
}
