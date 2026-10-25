package engine

type deleteFailingReader struct {
	text string
	err  error
}

func (r *deleteFailingReader) Read(dst []byte) (int, error) {
	if len(r.text) == 0 {
		return 0, r.err
	}
	n := copy(dst, r.text)
	r.text = r.text[n:]
	return n, nil
}
