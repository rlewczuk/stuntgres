package engine

import (
	"context"
	"fmt"
	"io"

	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
)

type controlOrigin uint8

const (
	trustedEngineControl controlOrigin = iota + 1
	detachedPublicControl
)

func (origin controlOrigin) validate(e *core.Engine, authority *core.ControlSepgsqlAuthority) error {
	switch origin {
	case trustedEngineControl:
		if authority == nil {
			return nil
		}
	case detachedPublicControl:
		return e.ValidateControlSepgsql(authority)
	}
	return fmt.Errorf("stuntgres: invalid native control trust origin")
}

// Reset is the trusted host-internal bootstrap replacement contract.
func Reset(ctx context.Context, e *core.Engine) error {
	return resetCatalogContents(ctx, e, trustedEngineControl, nil)
}

// ResetWithAuthority is the authenticated public adapter's replacement contract.
// Active simulation requires an actual destination-session admission capture.
func ResetWithAuthority(ctx context.Context, e *core.Engine, authority *core.ControlSepgsqlAuthority) error {
	return resetCatalogContents(ctx, e, detachedPublicControl, authority)
}
func resetCatalogContents(ctx context.Context, e *core.Engine, origin controlOrigin, authority *core.ControlSepgsqlAuthority) error {
	if err := origin.validate(e, authority); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	fresh := NewWithSettings(nil, e.DatabaseName, false)
	if err := fresh.InstallForeignDefinitions(e.Foreign.Definitions()); err != nil {
		return err
	}
	if err := fresh.InstallHostDefinitions(e.Catalog.HostRuntimes); err != nil {
		return err
	}
	if err := fresh.InstallSepgsqlSimulation(e.Sepgsql, false); err != nil {
		return err
	}
	fresh.InitializeSepgsqlSeedLabels()
	fresh.TextSearchFiles = e.TextSearchFiles
	fresh.IdentFile = e.IdentFile
	if origin == detachedPublicControl {
		_, err := e.ReplaceCatalogWithControl(ctx, fresh, fresh.CatalogRead(), nil, authority)
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
	return restoreCatalogContents(ctx, e, src, trustedEngineControl, nil)
}

// RestoreWithAuthority replays using an explicit detached destination identity,
// never the source replay session or an implicitly trusted nil capture.
func RestoreWithAuthority(ctx context.Context, e *core.Engine, src io.Reader, authority *core.ControlSepgsqlAuthority) error {
	return restoreCatalogContents(ctx, e, src, detachedPublicControl, authority)
}
func restoreCatalogContents(ctx context.Context, e *core.Engine, src io.Reader, origin controlOrigin, authority *core.ControlSepgsqlAuthority) error {
	if err := origin.validate(e, authority); err != nil {
		return err
	}
	if src == nil {
		return fmt.Errorf("stuntgres: nil restore reader")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	run := &restoreRun{stage: e.RestoreStage(), hostContext: ctx, controlOrigin: origin, sepgsqlAuthority: authority}
	return control.Restore(ctx, &restoreHost{target: e, run: run}, restoreHooks, run, src)
}
