package engine

import (
	"context"
	"fmt"
	"io"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func (e *Engine) Reset(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fresh := NewWithSettings(nil, e.databaseName, false)
	if err := fresh.InstallForeignDefinitions(e.foreign.Definitions()); err != nil {
		return err
	}
	_, err := e.ReplaceCatalog(ctx, fresh, fresh.CatalogRead())
	return err
}

// Namespace ACL entry order and split entries do not affect reproducibility.
// Principal/grantor references do: losing them would change DROP dependencies.
func bootstrapNamespaceDumpable(d *catalog.Namespace) bool {
	if d.Address().ObjectID == ids.PGToastNamespaceOID {
		return !d.ACL.Explicit && len(d.ACL.Entries) == 0
	}
	if !d.ACL.Explicit {
		return false
	}
	publicRights := authorization.UsagePrivilege
	var publicUse, publicOptions, ownerUse, ownerOptions authorization.Privileges
	for _, entry := range d.ACL.Entries {
		if entry.Grantor != d.Owner {
			return false
		}
		if entry.Grantee.Public {
			if entry.Grantee.Role != (authorization.PrincipalRef{}) {
				return false
			}
			publicUse |= entry.Privileges
			publicOptions |= entry.GrantOptions
		} else {
			if entry.Grantee.Role != d.Owner {
				return false
			}
			ownerUse |= entry.Privileges
			ownerOptions |= entry.GrantOptions
		}
	}
	return publicUse == publicRights && publicOptions == 0 &&
		ownerUse == authorization.UsagePrivilege|authorization.CreatePrivilege && ownerOptions == 0
}

// Dump emits replayable logical definitions and rows, including renamed,
// manual, partial and nested partitioned key/index trees. Virtual indexes have
// no physical roots; their actual ancestry and incomplete validity are retained.
//
// Dump reads engine state only in captureDumpLocked, under Engine.mu, through
// the Collect tables (dumpCollectors). It then hands the capture to
// control.Dump, which orders the captured graph and writes the script.
func (e *Engine) Dump(ctx context.Context, dst io.Writer) error {
	if dst == nil {
		return fmt.Errorf("stuntgres: nil dump writer")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	capture, err := e.captureDumpLocked(ctx)
	e.mu.Unlock()
	if err != nil {
		return err
	}
	return control.Dump(ctx, capture, dst)
}

type contextReader struct {
	ctx context.Context
	src io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.src.Read(p)
	if canceled := r.ctx.Err(); canceled != nil {
		return n, canceled
	}
	return n, err
}

// Restore replaces the persistent logical catalog atomically from Dump SQL.
// Partitioned key/index references are reminted against retained live TEMP
// objects; failed or canceled replacement leaves the old tree usable.
// Native replacement is not a PostgreSQL control API.
func (e *Engine) Restore(ctx context.Context, src io.Reader) error {
	if src == nil {
		return fmt.Errorf("stuntgres: nil restore reader")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	stage := e.restoreStage()
	data, err := io.ReadAll(contextReader{ctx, src})
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	r := &restoreRun{stage: stage}
	host := &restoreHost{target: e, run: r}
	source := string(data)
	statements, err := parser.ParseWithOptions(source, parser.Options{Context: ctx, StandardConformingStrings: true, LineComment: func(comment string, position parser.Position) error {
		return r.decodeComment(restoreHooks, comment, position)
	}})
	if err != nil {
		return err
	}
	if err := host.Begin(source, statements); err != nil {
		return err
	}
	op, end, err := host.Start(ctx)
	if err != nil {
		return err
	}
	defer end()
	for statementIndex, stmt := range statements {
		host.Statement(statementIndex)
		consumed, err := r.runBefore(restoreHooks, op, stmt)
		if err != nil {
			return err
		}
		if consumed {
			continue
		}
		admission, err := r.runAdmit(restoreHooks, op, stmt)
		if err != nil {
			return err
		}
		if admission == restoreConsumed {
			continue
		}
		if err := r.runPrepare(restoreHooks, op, stmt); err != nil {
			return err
		}
		result, err := host.Execute(op, stmt)
		if err != nil {
			return err
		}
		if err := r.runAfter(restoreHooks, op, stmt, result); err != nil {
			return err
		}
	}
	if err := r.runFinish(ctx, restoreHooks, op); err != nil {
		return err
	}
	return host.Publish(ctx, op)
}
