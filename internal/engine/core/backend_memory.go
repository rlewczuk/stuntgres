package core

import (
	"context"
	"fmt"
	"unsafe"

	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
)

// Physical-session statement admission serializes the registry. No session
// mutex is acquired here; callers validate the executing operation before entry.
// Only these actual owners' layouts are selected, never their referenced graphs.
func captureBackendMemory(ctx context.Context, s *session.Session) (pgcatalog.BackendMemorySnapshot, error) {
	var snapshot pgcatalog.BackendMemorySnapshot
	if err := ctx.Err(); err != nil {
		return snapshot, err
	}
	if s == nil {
		return snapshot, fmt.Errorf("stuntgres: backend memory lacks physical session")
	}
	payload := PayloadOf(s)
	// The immutable startup backlink proves actual enclosing ownership, not
	// adjacency, a guessed retained graph or a plausible size for a literal.
	if payload == nil || payload.allocation == nil || &payload.allocation.session != s || &payload.allocation.payload != payload {
		return snapshot, fmt.Errorf("stuntgres: backend memory lacks session allocation")
	}
	snapshot.Count, snapshot.Own[0] = 1, int64(unsafe.Sizeof(*payload.allocation))
	if registry := payload.PreparedSQL; registry != nil {
		if registry.token != s.SessionToken {
			return pgcatalog.BackendMemorySnapshot{}, fmt.Errorf("stuntgres: foreign backend prepared owner")
		}
		snapshot.Count, snapshot.Own[1] = 3, int64(unsafe.Sizeof(PreparedRegistry{}))
		// Install accepts pointer aliases internally. Public SQL/wire installation
		// creates distinct objects; still charge each backing owner only once.
		seen := make(map[*PreparedDefinition]struct{}, len(registry.definitions))
		for _, definition := range registry.definitions {
			if err := ctx.Err(); err != nil {
				return pgcatalog.BackendMemorySnapshot{}, err
			}
			if definition == nil {
				return pgcatalog.BackendMemorySnapshot{}, fmt.Errorf("stuntgres: missing backend prepared definition")
			}
			if _, duplicate := seen[definition]; !duplicate {
				seen[definition] = struct{}{}
				snapshot.Own[2] += int64(unsafe.Sizeof(*definition))
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return pgcatalog.BackendMemorySnapshot{}, err
	}
	return snapshot, nil
}
