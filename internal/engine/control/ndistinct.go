package control

import (
	"context"
	"stuntgres/internal/engine/storage"
)

func emitDumpNDistinct(ctx context.Context, s *dumpScript, w *dumpWriter) error {
	for _, record := range s.NDistinct {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := storage.EncodeNDistinctSnapshot(record)
		if err != nil {
			return err
		}
		if err := w.emit(line); err != nil {
			return err
		}
	}
	return ctx.Err()
}
