package engine

import (
	"stuntgres/internal/engine/core"
)

// publishCatalog publishes p outside any operation or statement.
func publishCatalog(e *core.Engine, p *core.PreparedCatalogPublication, token core.PublicationToken) (core.ChangeSet, error) {
	return e.PublishPreparedCatalog(nil, nil, p, token)
}
