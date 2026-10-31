package contrib

import (
	"fmt"

	"stuntgres/internal/engine/functions"
)

// GenericFileList uses the executing expression principal, not the preparing
// session. Lock order is Engine -> Settings -> Session, matching setting reads.
func GenericFileList[A Authority](h Host[A], kind functions.GenericFileListKind, dirname string) error {
	if kind.Name() == "" {
		return fmt.Errorf("stuntgres: invalid generic file listing kind")
	}
	if kind != functions.GenericFileDir {
		return functions.GenericFileListUnsupported(kind)
	}
	if err := checkGenericFilePath(h, dirname); err != nil {
		return err
	}
	return functions.GenericFileListUnsupported(kind)
}

// checkGenericFilePath borrows current authority in Engine -> Settings -> Session order.
func checkGenericFilePath[A Authority](h Host[A], dirname string) error {
	dataDirectory, logDirectory, privileged, err := h.GenericFileAuthority()
	if err != nil {
		return err
	}
	if _, err := functions.CheckGenericFileListPath(dirname, dataDirectory, logDirectory, privileged); err != nil {
		return err
	}
	return nil
}
