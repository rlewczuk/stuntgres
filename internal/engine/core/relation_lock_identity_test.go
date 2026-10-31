package core_test

type relationFailReader struct{ err error }

func (r relationFailReader) Read([]byte) (int, error) { return 0, r.err }
