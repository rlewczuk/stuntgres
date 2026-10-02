package stuntgres

import "errors"

type result int64

func (r result) RowsAffected() (int64, error) { return int64(r), nil }
func (r result) LastInsertId() (int64, error) {
	return 0, errors.New("LastInsertId is not supported by this driver")
}
