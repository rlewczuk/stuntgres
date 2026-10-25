package fixture

type TypeLifecycleErrorReader struct{ Err error }

func (r TypeLifecycleErrorReader) Read([]byte) (int, error) { return 0, r.Err }
