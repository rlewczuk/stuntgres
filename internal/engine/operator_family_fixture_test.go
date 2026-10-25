package engine

import (
	"stuntgres/internal/engine/catalog"
	"sync"
)

var operatorFamilyUnitSeed = sync.OnceValue(func() catalog.Read { return New().CatalogRead() })

// Reduced storage fixtures still need real family members and genuine callable
// declarations. Share an immutable ordinary engine bootstrap, never a provider
// fallback or a test callback.
func operatorFamilyTestRead() catalog.Read { return operatorFamilyUnitSeed() }
