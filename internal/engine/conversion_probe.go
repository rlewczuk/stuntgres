package engine

func conversionProbeResult(name string, consumed int) error {
	if consumed != 0 {
		return sqlError("42P17", "encoding conversion function "+name+" returned incorrect result for empty input")
	}
	return nil
}
