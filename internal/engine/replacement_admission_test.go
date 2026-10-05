package engine

import (
	"runtime"
	"testing"
	"time"
)

func TestRelationLockReplacementAdmission(t *testing.T) {
	var admission replacementAdmission
	admission.RLock()
	if !admission.mu.TryLock() {
		t.Fatal("reader reservation retained synchronization")
	}
	admission.mu.Unlock()
	if admission.TryLock() {
		t.Fatal("replacement entered an active reader reservation")
	}

	writerEntered := make(chan struct{})
	releaseWriter := make(chan struct{})
	writerDone := make(chan struct{})
	readerEntered := make(chan struct{})
	go func() {
		admission.Lock()
		close(writerEntered)
		<-releaseWriter
		admission.Unlock()
		close(writerDone)
	}()
	defer close(releaseWriter)
	t.Cleanup(func() {
		select {
		case <-writerDone:
		case <-time.After(5 * time.Second):
			t.Error("replacement did not release")
		}
		select {
		case <-readerEntered:
		case <-time.After(5 * time.Second):
			t.Error("reader did not enter after replacement released")
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		admission.mu.Lock()
		waiting := admission.writers != 0
		admission.mu.Unlock()
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			admission.RUnlock()
			t.Fatal("replacement did not register its pending reservation")
		}
		runtime.Gosched()
	}
	go func() {
		admission.RLock()
		close(readerEntered)
		admission.RUnlock()
	}()
	admission.RUnlock()
	select {
	case <-writerEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("replacement did not enter after the last reader released")
	}
	select {
	case <-readerEntered:
		t.Fatal("new reader bypassed a waiting replacement")
	default:
	}
	if !admission.mu.TryLock() {
		t.Fatal("writer reservation retained synchronization")
	}
	admission.mu.Unlock()
	if admission.TryLock() {
		t.Fatal("second replacement entered an active writer reservation")
	}
}
