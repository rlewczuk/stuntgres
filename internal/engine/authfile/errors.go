// Package authfile owns PostgreSQL authentication-file tokenization and includes.
package authfile

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"stuntgres/internal/sqlstate"
	"syscall"
)

func FileError(operation, path string, err error) error {
	code := "XX000"
	reason := ""
	switch {
	case errors.Is(err, os.ErrNotExist):
		code = "58P01"
		reason = "No such file or directory"
	case errors.Is(err, os.ErrPermission):
		code = "42501"
		reason = "Permission denied"
	case errors.Is(err, syscall.EISDIR):
		code = "42809"
		reason = "Is a directory"
	case errors.Is(err, syscall.ENOTDIR):
		code = "42809"
		reason = "Not a directory"
	case errors.Is(err, syscall.EMFILE), errors.Is(err, syscall.ENFILE):
		code = "53000"
	case errors.Is(err, syscall.EROFS):
		code = "42501"
	case errors.Is(err, syscall.EEXIST):
		code = "58P02"
	case errors.Is(err, syscall.ENOTEMPTY):
		code = "42809"
	case errors.Is(err, syscall.ENOSPC):
		code = "53100"
	case errors.Is(err, syscall.ENOMEM):
		code = "53200"
	case errors.Is(err, syscall.EIO):
		code = "58030"
	case errors.Is(err, syscall.ENAMETOOLONG):
		code = "58P03"
	}
	if reason == "" {
		var pe *os.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		reason = err.Error()
		if len(reason) > 0 {
			reason = strings.ToUpper(reason[:1]) + reason[1:]
		}
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: fmt.Sprintf("could not %s file \"%s\": %s", operation, path, reason)}
}
