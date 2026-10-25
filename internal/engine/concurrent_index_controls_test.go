package engine

type concurrentUnitSink struct{ calls, bytes int }

func (w *concurrentUnitSink) Write(data []byte) (int, error) {
	w.calls++
	w.bytes += len(data)
	return len(data), nil
}
