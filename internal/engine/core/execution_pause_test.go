package core_test

import (
	"context"

	"stuntgres/internal/engine/settings"
)

type executionPauseTimeoutDefaults struct{ enabled bool }

func (d executionPauseTimeoutDefaults) SessionDefault(_ context.Context, definition settings.Definition) (string, bool, error) {
	if d.enabled && definition.Name == "statement_timeout" {
		return "1s", true, nil
	}
	return "", false, nil
}
