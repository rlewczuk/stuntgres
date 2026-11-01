package exec

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/sqltype"
)

// PrepareIsnInput admits only an actual installed input provider from the
// occurrence's captured catalog. The borrowed host belongs to this operation.
func PrepareIsnInput(ctx context.Context, host Host, view catalog.Read, target *sqltype.Descriptor) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if target == nil || host == nil || view.State() == nil {
		return false, fmt.Errorf("stuntgres: missing ISN input authority")
	}
	if _, err := extension.IsnInputProcedure(view, target); err != nil {
		return false, err
	}
	if err := host.InitializeIsn(); err != nil {
		return false, err
	}
	input, _ := host.Environments()
	return input.IsnWeak, nil
}

func (c *Context) PrepareIsnInput(ctx context.Context, target *sqltype.Descriptor) (bool, error) {
	view := c.Catalog
	if view.State() == nil {
		view = preparation.DomainCatalog(c)
	}
	return PrepareIsnInput(ctx, c.LiveHost(), view, target)
}
