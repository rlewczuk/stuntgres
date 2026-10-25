package fixture

type UUIDReferenceFailingReader struct{ Err error }

func (r UUIDReferenceFailingReader) Read([]byte) (int, error) { return 0, r.Err }
