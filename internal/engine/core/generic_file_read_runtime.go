package core

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

func (h *statementHost) GenericFileRead(kind functions.GenericFileReadKind, filename string, length int64, hasLength bool) error {
	if kind.Name() == "" || kind == functions.GenericFileStat && hasLength {
		return fmt.Errorf("stuntgres: invalid generic file read request")
	}
	if hasLength && length < 0 {
		return SQLError("22023", "requested length cannot be negative")
	}
	if err := h.checkGenericFilePath(filename); err != nil {
		return err
	}
	if hasLength && length > 1073741819 {
		return SQLError("22023", "requested length too large")
	}
	mechanism := "physical file reading"
	if kind == functions.GenericFileStat {
		mechanism = "physical file metadata"
	}
	return SQLError("0A000", kind.Name()+" "+mechanism+" is unsupported in embedded mode")
}

func (h *statementHost) GenericFileTmpdir(oid uint32) error {
	view, err := h.catalogInquiryCatalog()
	if err != nil {
		return err
	}
	if _, found := view.Tablespace(ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: ids.OID(oid)}); !found {
		return SQLError("42704", "tablespace with OID "+strconv.FormatUint(uint64(oid), 10)+" does not exist")
	}
	return functions.GenericFileListUnsupported(functions.GenericFileTmp)
}
