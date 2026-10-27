package functions

import (
	"path"
	"strings"
)

// GenericFileListRuntime borrows statement-local role and settings authority.
// It must never inspect the host filesystem.
type GenericFileListRuntime interface {
	GenericFileList(GenericFileListKind, string) error
}

func canonicalFileListPath(value string) string {
	if value == "" {
		return ""
	}
	return path.Clean(value)
}

// CheckGenericFileListPath applies PostgreSQL's Linux lexical read-file policy
// to logical roots. It performs no filesystem calls or symlink resolution.
func CheckGenericFileListPath(dirname, dataDirectory, logDirectory string, privileged bool) (string, error) {
	dirname = canonicalFileListPath(dirname)
	if privileged {
		return dirname, nil
	}
	if path.IsAbs(dirname) {
		dataDirectory = canonicalFileListPath(dataDirectory)
		logDirectory = canonicalFileListPath(logDirectory)
		prefix := func(root string) bool {
			return root != "" && (dirname == root || strings.HasPrefix(dirname, root+"/"))
		}
		if !prefix(dataDirectory) && !(path.IsAbs(logDirectory) && prefix(logDirectory)) {
			return dirname, sqlError("42501", "absolute path not allowed")
		}
	} else if dirname == ".." || strings.HasPrefix(dirname, "../") {
		return dirname, sqlError("42501", "path must be in or below the data directory")
	}
	return dirname, nil
}

func GenericFileListUnsupported(kind GenericFileListKind) error {
	return sqlError("0A000", kind.Name()+" physical directory listing is unsupported in embedded mode")
}
