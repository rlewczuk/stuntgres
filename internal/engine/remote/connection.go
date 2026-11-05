package remote

import (
	"context"
	"errors"
	"sync"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
)

type connection struct {
	owner           *Owner
	provider        *catalog.ExternalSQLProviderDefinition
	raw             catalog.ExternalSQLConnection
	info            catalog.ExternalSQLConnectionInfo
	postgres        *postgresEntry // Immutable link; Owner.mu protects entry state.
	command         sync.Mutex
	mu              sync.Mutex
	active, closing bool
	activeCancel    context.CancelFunc
	cancels         sync.WaitGroup
	closeOnce       sync.Once
	closeError      error
}

func (c *connection) Info() catalog.ExternalSQLConnectionInfo { return c.info }

func (c *connection) check(ctx context.Context) error {
	if err := c.owner.check(ctx); err != nil {
		return err
	}
	if active, ok := catalog.ExternalSQLContextCapability(ctx).(contextAuthority); ok && active.ExternalSQLProviderToken() != c.provider {
		return errors.New("stuntgres: foreign external SQL provider context")
	}
	return nil
}

func (c *connection) begin(ctx context.Context) (context.Context, error) {
	if err := c.check(ctx); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing {
		return nil, errors.New("stuntgres: external SQL connection is closed")
	}
	managed, cancel := context.WithCancel(ctx)
	c.active, c.activeCancel = true, cancel
	return managed, nil
}
func (c *connection) end() {
	c.mu.Lock()
	cancel := c.activeCancel
	c.active, c.activeCancel = false, nil
	c.mu.Unlock()
	cancel()
}

func (c *connection) Execute(ctx context.Context, sql string) ([]catalog.ExternalSQLResult, error) {
	c.command.Lock()
	defer c.command.Unlock()
	ctx, err := c.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer c.end()
	results, err := c.raw.Execute(ctx, sql)
	if err != nil {
		return nil, exec.ForeignCallbackError(err)
	}
	if err := c.check(ctx); err != nil {
		return nil, err
	}
	for _, result := range results {
		if err := ValidateResult(result); err != nil {
			return nil, err
		}
	}
	return results, nil
}

func ValidateResult(r catalog.ExternalSQLResult) error {
	if r.RowCount < 0 || r.RowsAffected < 0 || !r.Tuple && (len(r.Columns) != 0 || len(r.Cells) != 0 || r.RowCount != 0) {
		return errors.New("stuntgres: malformed external SQL result")
	}
	width := len(r.Columns)
	if width == 0 {
		if len(r.Cells) != 0 {
			return errors.New("stuntgres: malformed external SQL row width")
		}
	} else if len(r.Cells)%width != 0 || len(r.Cells)/width != r.RowCount {
		return errors.New("stuntgres: malformed external SQL row width")
	}
	for _, column := range r.Columns {
		if column.TypeOID == 0 || column.Format != 0 {
			return errors.New("stuntgres: external SQL result requires typed text columns")
		}
	}
	for _, cell := range r.Cells {
		if cell.Null && cell.Text != "" {
			return errors.New("stuntgres: external SQL NULL has text")
		}
	}
	return nil
}

func (c *connection) Cancel(ctx context.Context) error {
	if err := c.check(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	if c.closing {
		c.mu.Unlock()
		return errors.New("stuntgres: external SQL connection is closed")
	}
	c.cancels.Add(1)
	c.mu.Unlock()
	defer c.cancels.Done()
	return exec.ForeignCallbackError(c.raw.Cancel(ctx))
}

func (c *connection) DrainNotifications(ctx context.Context) ([]catalog.ExternalSQLNotification, error) {
	c.command.Lock()
	defer c.command.Unlock()
	ctx, err := c.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer c.end()
	events, err := c.raw.DrainNotifications(ctx)
	if err != nil {
		return nil, exec.ForeignCallbackError(err)
	}
	if err := c.check(ctx); err != nil {
		return nil, err
	}
	return events, nil
}

func (c *connection) Close(ctx context.Context) error {
	if token, ok := ctx.Value(cleanupKey{}).(ids.SessionToken); ok && token != c.owner.session.Token() {
		return errors.New("stuntgres: foreign external SQL cleanup context")
	}
	if active, ok := catalog.ExternalSQLContextCapability(ctx).(contextAuthority); ok && (active.ExternalSQLSessionToken() != c.owner.session.Token() || active.ExternalSQLProviderToken() != c.provider) {
		return errors.New("stuntgres: foreign external SQL connection context")
	}
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closing = true
		active := c.active
		cancel := c.activeCancel
		c.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if active {
			_ = c.raw.Cancel(context.WithoutCancel(ctx))
		}
		c.command.Lock()
		defer c.command.Unlock()
		c.cancels.Wait()
		c.closeError = exec.ForeignCallbackError(c.raw.Close(context.WithoutCancel(ctx)))
		c.owner.mu.Lock()
		delete(c.owner.connections, c)
		c.owner.mu.Unlock()
	})
	return c.closeError
}
