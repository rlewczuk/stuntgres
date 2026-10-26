package role

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

func TestCanonicalGrantParameterTargets(t *testing.T) {
	registry := settings.NewRegistry(nil, false)
	for _, name := range []string{"lc_messages", "LC_MESSAGES", "application_name", "server_encoding", "work_mem", "max_stack_depth", "temp_file_limit", "OG_CUSTOM.Value"} {
		got, err := CanonicalGrantParameter(registry, name)
		if err != nil || got != settings.FoldName(name) {
			t.Fatalf("canonical target %q: %q, %v", name, got, err)
		}
	}
	for _, name := range []string{"sort_mem", "missing_resource_setting", "missing", "bad..name", ".bad", "bad.1name"} {
		_, err := CanonicalGrantParameter(registry, name)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42704" || diagnostic.Message != `unrecognized configuration parameter "`+name+`"` {
			t.Fatalf("unsupported target %q: %v", name, err)
		}
	}
}
