package core

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

// GenericFileRead checks a pg_read_file or pg_stat_file request, then rejects
// its physical mechanism.
func GenericFileRead[A Authority](h Host[A], kind functions.GenericFileReadKind, filename string, length int64, hasLength bool) error {
	if kind.Name() == "" || kind == functions.GenericFileStat && hasLength {
		return fmt.Errorf("stuntgres: invalid generic file read request")
	}
	if hasLength && length < 0 {
		return sqlError("22023", "requested length cannot be negative")
	}
	if err := checkGenericFilePath(h, filename); err != nil {
		return err
	}
	if hasLength && length > 1073741819 {
		return sqlError("22023", "requested length too large")
	}
	mechanism := "physical file reading"
	if kind == functions.GenericFileStat {
		mechanism = "physical file metadata"
	}
	return sqlError("0A000", kind.Name()+" "+mechanism+" is unsupported in embedded mode")
}

// GenericFileTmpdir checks the tablespace of pg_ls_tmpdir, then rejects
// physical listing.
func GenericFileTmpdir[A Authority](h Host[A], oid uint32) error {
	view, err := h.InquiryCatalog()
	if err != nil {
		return err
	}
	if _, found := view.Tablespace(ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: ids.OID(oid)}); !found {
		return sqlError("42704", "tablespace with OID "+strconv.FormatUint(uint64(oid), 10)+" does not exist")
	}
	return functions.GenericFileListUnsupported(functions.GenericFileTmp)
}
