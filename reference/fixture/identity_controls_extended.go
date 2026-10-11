package fixture

type IdentityFailingReader struct{ Err error }

func (r IdentityFailingReader) Read([]byte) (int, error) { return 0, r.Err }
