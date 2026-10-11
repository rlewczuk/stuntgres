package fixture

import (
	"bytes"
	"context"
	"io"
)

type EnumCancelReader struct {
	*bytes.Reader
	Cancel context.CancelFunc
}

func (r *EnumCancelReader) Read(dst []byte) (int, error) {
	n, err := r.Reader.Read(dst)
	if err == io.EOF {
		r.Cancel()
	}
	return n, err
}
