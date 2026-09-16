package main

import (
	"testing"
)

// fakeDecoder is a StreamSeekCloser serving deterministic samples and recording
// Close calls.
//
// fakeDecoder 是提供确定性样本并记录 Close 调用的 StreamSeekCloser。
type fakeDecoder struct {
	pos    int
	length int
	closed bool
	err    error
}

func (f *fakeDecoder) Stream(samples [][2]float64) (int, bool) {
	if f.err != nil {
		return 0, false
	}
	if f.pos >= f.length {
		return 0, false
	}
	n := len(samples)
	if n > f.length-f.pos {
		n = f.length - f.pos
	}
	for i := range n {
		samples[i] = [2]float64{float64(f.pos + i), float64(f.pos + i)}
	}
	f.pos += n
	return n, true
}

func (f *fakeDecoder) Err() error    { return f.err }
func (f *fakeDecoder) Len() int      { return f.length }
func (f *fakeDecoder) Position() int { return f.pos }
func (f *fakeDecoder) Seek(p int) error {
	f.pos = p
	return nil
}
func (f *fakeDecoder) Close() error { f.closed = true; return nil }

func TestGaplessQueueSeamlessHandoff(t *testing.T) {
	old := &fakeDecoder{length: 10}
	next := &fakeDecoder{length: 5}
	q := newGaplessQueue(old, "a")
	q.setNext(next, "b")

	buf := make([][2]float64, 16)
	n, ok := q.Stream(buf)
	if ok {
		t.Fatalf("expected ok=false once both decoders drained, got n=%d", n)
	}
	if n != 15 {
		t.Fatalf("expected 15 samples across handoff, got %d", n)
	}
	if buf[9][0] != 9 || buf[10][0] != 0 {
		t.Fatalf("expected boundary old[9] then new[0], got %v then %v", buf[9][0], buf[10][0])
	}
	if !old.closed {
		t.Fatal("expected old decoder to be closed at handoff")
	}
	if q.path() != "b" {
		t.Fatalf("expected currentPath=b after handoff, got %s", q.path())
	}
	if q.hasNext() {
		t.Fatal("expected no pending song after handoff")
	}
	if !q.exhausted.Load() {
		t.Fatal("expected exhausted flag to be set after next drained")
	}

	n, ok = q.Stream(buf)
	if ok || n != 0 {
		t.Fatalf("expected exhausted after next drained, got n=%d ok=%v", n, ok)
	}
}

func TestGaplessQueueFillsCompletely(t *testing.T) {
	old := &fakeDecoder{length: 3}
	next := &fakeDecoder{length: 20}
	q := newGaplessQueue(old, "a")
	q.setNext(next, "b")

	buf := make([][2]float64, 16)
	n, ok := q.Stream(buf)
	if !ok || n != 16 {
		t.Fatalf("expected queue to fill 16 samples, got n=%d ok=%v", n, ok)
	}
	if buf[2][0] != 2 || buf[3][0] != 0 {
		t.Fatalf("expected boundary old[2] then new[0], got %v then %v", buf[2][0], buf[3][0])
	}
}

func TestGaplessQueueExhaustedWithoutNext(t *testing.T) {
	old := &fakeDecoder{length: 4}
	q := newGaplessQueue(old, "a")

	buf := make([][2]float64, 8)
	n, ok := q.Stream(buf)
	if ok || n != 4 {
		t.Fatalf("expected (4, false) at clean EOF, got n=%d ok=%v", n, ok)
	}
	if !q.exhausted.Load() || q.exhaustedErr.Load() {
		t.Fatal("expected exhausted=true, exhaustedErr=false at clean EOF")
	}
}

func TestGaplessQueueErrorMarksExhaustedErr(t *testing.T) {
	old := &fakeDecoder{length: 4, err: errFake}
	q := newGaplessQueue(old, "a")

	buf := make([][2]float64, 8)
	if _, ok := q.Stream(buf); ok {
		t.Fatal("expected ok=false on decoder error")
	}
	if !q.exhaustedErr.Load() {
		t.Fatal("expected exhaustedErr=true on decoder error")
	}
}

func TestGaplessQueueSetNextReplacesAndCloses(t *testing.T) {
	old := &fakeDecoder{length: 10}
	first := &fakeDecoder{length: 5}
	second := &fakeDecoder{length: 5}
	q := newGaplessQueue(old, "a")

	q.setNext(first, "b")
	q.setNext(second, "c")
	if !first.closed {
		t.Fatal("expected replaced queued decoder to be closed")
	}
	if n := q.takeNext(); n == nil || n.path != "c" {
		t.Fatalf("expected takeNext to return song c, got %+v", n)
	}
	if q.takeNext() != nil {
		t.Fatal("expected takeNext to drain the pending slot")
	}
}

var errFake = &fakeError{}

type fakeError struct{}

func (*fakeError) Error() string { return "fake decoder error" }
