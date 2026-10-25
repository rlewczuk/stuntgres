package engine

import (
	"context"
	"fmt"
	"io"

	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
)

// Reset replaces e's persistent catalog with a freshly composed engine's.
func Reset(ctx context.Context, e *core.Engine) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fresh := NewWithSettings(nil, e.DatabaseName, false)
	if err := fresh.InstallForeignDefinitions(e.Foreign.Definitions()); err != nil {
		return err
	}
	_, err := e.ReplaceCatalog(ctx, fresh, fresh.CatalogRead())
	return err
}

// Dump emits replayable logical definitions and rows, including renamed,
// manual, partial and nested partitioned key/index trees. Virtual indexes have
// no physical roots; their actual ancestry and incomplete validity are retained.
//
// Dump reads engine state only in captureDumpLocked, under Engine.mu, through
// the Collect tables (dumpCollectors). It then hands the capture to
// control.Dump, which orders the captured graph and writes the script.
func Dump(ctx context.Context, e *core.Engine, dst io.Writer) error {
	if dst == nil {
		return fmt.Errorf("stuntgres: nil dump writer")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	e.Mu.Lock()
	capture, err := captureDumpLocked(e, ctx)
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	return control.Dump(ctx, capture, dst)
}

// Restore replaces the persistent logical catalog atomically from Dump SQL.
// Partitioned key/index references are reminted against retained live TEMP
// objects; failed or canceled replacement leaves the old tree usable.
// Native replacement is not a PostgreSQL control API.
func Restore(ctx context.Context, e *core.Engine, src io.Reader) error {
	if src == nil {
		return fmt.Errorf("stuntgres: nil restore reader")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	run := &restoreRun{stage: e.RestoreStage()}
	return control.Restore(ctx, &restoreHost{target: e, run: run}, restoreHooks, run, src)
}
