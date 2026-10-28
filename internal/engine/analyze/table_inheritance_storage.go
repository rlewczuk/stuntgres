package analyze

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"
)

// MergeInheritedColumnStorage merges effective policies from actual parents,
// never an uninitialized destination. Storage conflicts precede independent
// compression conflicts; callers retain the result in their private binding.
func MergeInheritedColumnStorage(name string, old, next catalog.ColumnStorage) (catalog.ColumnStorage, error) {
	return mergeColumnStorage(name, old, next, true)
}

// MergeLocalColumnStorage checks specified local policies against the effective
// inherited pair. Empty local fields contribute nothing, including DEFAULT
// compression; selected type-default storage is nevertheless specified.
func MergeLocalColumnStorage(name string, inherited, next catalog.ColumnStorage) (catalog.ColumnStorage, error) {
	if next.Storage == "" {
		next.Storage = inherited.Storage
	}
	return mergeColumnStorage(name, inherited, next, false)
}

func mergeColumnStorage(name string, old, next catalog.ColumnStorage, parents bool) (catalog.ColumnStorage, error) {
	if old.Storage != next.Storage {
		prefix := "column"
		if parents {
			prefix = "inherited column"
		}
		d := sqlError("42804", fmt.Sprintf(`%s "%s" has a storage parameter conflict`, prefix, name)).(*sqlstate.Error)
		d.Detail = inheritedStorageName(old.Storage) + " versus " + inheritedStorageName(next.Storage)
		return catalog.ColumnStorage{}, d
	}
	if old.Compression == "" {
		old.Compression = next.Compression
	} else if next.Compression != "" && old.Compression != next.Compression {
		d := sqlError("42804", fmt.Sprintf(`column "%s" has a compression method conflict`, name)).(*sqlstate.Error)
		d.Detail = inheritedCompressionName(old.Compression) + " versus " + inheritedCompressionName(next.Compression)
		return catalog.ColumnStorage{}, d
	}
	return old, nil
}

func inheritedStorageName(value string) string {
	switch value {
	case "p":
		return "PLAIN"
	case "e":
		return "EXTERNAL"
	case "m":
		return "MAIN"
	default:
		return "EXTENDED"
	}
}

func inheritedCompressionName(value string) string {
	if value == "p" {
		return "pglz"
	}
	return "lz4"
}
