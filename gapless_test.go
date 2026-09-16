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
	if !ok || n != 16 {
		t.Fatalf("expected queue to pad silence after exhaustion, got n=%d ok=%v", n, ok)
	}
	if buf[9][0] != 9 || buf[10][0] != 0 {
		t.Fatalf("expected boundary old[9] then new[0], got %v then %v", buf[9][0], buf[10][0])
	}
	if buf[14][0] != 4 || buf[15][0] != 0 {
		t.Fatalf("expected new[4] then silence, got %v then %v", buf[14][0], buf[15][0])
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

	for i := range buf {
		buf[i] = [2]float64{1, 1}
	}
	n, ok = q.Stream(buf)
	if !ok || n != 16 {
		t.Fatalf("expected silence padding on later pulls, got n=%d ok=%v", n, ok)
	}
	if buf[0][0] != 0 {
		t.Fatalf("expected padded silence, got %v", buf[0][0])
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
	for i := range buf {
		buf[i] = [2]float64{1, 1}
	}
	n, ok := q.Stream(buf)
	if !ok || n != 8 {
		t.Fatalf("expected silence-padded (8, true) at clean EOF, got n=%d ok=%v", n, ok)
	}
	if buf[3][0] != 3 || buf[4][0] != 0 {
		t.Fatalf("expected 4 real samples then silence, got %v then %v", buf[3][0], buf[4][0])
	}
	if !q.exhausted.Load() {
		t.Fatal("expected exhausted=true at clean EOF")
	}

	n, ok = q.Stream(buf)
	if !ok || n != 8 {
		t.Fatalf("expected later pulls to keep padding silence, got n=%d ok=%v", n, ok)
	}
}

func TestGaplessQueueErrorRecordsErroredPath(t *testing.T) {
	old := &fakeDecoder{length: 4, err: errFake}
	q := newGaplessQueue(old, "a")

	buf := make([][2]float64, 8)
	if n, ok := q.Stream(buf); !ok || n != 8 {
		t.Fatalf("expected silence padding on decoder error, got n=%d ok=%v", n, ok)
	}
	if got := q.takeErroredPath(); got != "a" {
		t.Fatalf("expected errored path a, got %q", got)
	}
	if got := q.takeErroredPath(); got != "" {
		t.Fatalf("expected errored path to be consumed, got %q", got)
	}
}

func TestGaplessQueueRecoversAfterExhaustViaResetTo(t *testing.T) {
	fired := 0
	old := &fakeDecoder{length: 4}
	q := newGaplessQueue(old, "a")
	q.onExhausted = func() { fired++ }

	buf := make([][2]float64, 8)
	n, ok := q.Stream(buf)
	if !ok || n != 8 {
		t.Fatalf("expected silence padding at exhaust, got n=%d ok=%v", n, ok)
	}
	if fired != 1 {
		t.Fatalf("expected exhaust callback to fire once, got %d", fired)
	}

	// Simulate the fallback advance arming a fresh decoder; playback must
	// resume from it instead of staying stuck in padded silence.
	//
	// 模拟兜底推进武装新解码器；播放必须从中恢复，而不是卡在填充的静音里。
	fresh := &fakeDecoder{length: 20}
	q.resetTo(fresh, "b")

	n, ok = q.Stream(buf)
	if !ok || n != 8 {
		t.Fatalf("expected playback to resume from the new decoder, got n=%d ok=%v", n, ok)
	}
	if buf[0][0] != 0 {
		t.Fatalf("expected new decoder samples, got %v", buf[0][0])
	}
	if fired != 1 {
		t.Fatalf("expected exhaust callback to fire only once per arm, got %d", fired)
	}

	if n, ok = q.Stream(buf); !ok || n != 8 {
		t.Fatalf("expected steady playback from the new decoder, got n=%d ok=%v", n, ok)
	}
	if fired != 1 {
		t.Fatalf("expected no exhaust while the new decoder plays, got %d", fired)
	}

	// Drain the fresh decoder; the callback must fire once more on re-exhaust.
	if n, ok = q.Stream(buf); !ok || n != 8 {
		t.Fatalf("expected final pull to pad silence, got n=%d ok=%v", n, ok)
	}
	if buf[3][0] != 19 || buf[4][0] != 0 {
		t.Fatalf("expected fresh[19] then silence, got %v then %v", buf[3][0], buf[4][0])
	}
	if fired != 2 {
		t.Fatalf("expected exhaust callback to fire again after re-arm, got %d", fired)
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

// chunkyDecoder serves at most maxChunk samples per call, so mid-stream
// partial fills exercise the queue's in-loop retry.
//
// chunkyDecoder 每次调用最多提供 maxChunk 个样本，用中途部分填充来检验队列的
// 循环重拉逻辑。
type chunkyDecoder struct {
	fakeDecoder
	maxChunk int
}

func (f *chunkyDecoder) Stream(samples [][2]float64) (int, bool) {
	if len(samples) > f.maxChunk {
		samples = samples[:f.maxChunk]
	}
	return f.fakeDecoder.Stream(samples)
}

func TestGaplessQueueRetriesMidStreamPartialFills(t *testing.T) {
	old := &chunkyDecoder{fakeDecoder{length: 10}, 2}
	next := &chunkyDecoder{fakeDecoder{length: 10}, 3}
	q := newGaplessQueue(old, "a")
	q.setNext(next, "b")

	buf := make([][2]float64, 16)
	n, ok := q.Stream(buf)
	if !ok || n != 16 {
		t.Fatalf("expected full 16 samples across chunky partials, got n=%d ok=%v", n, ok)
	}
	if buf[9][0] != 9 || buf[10][0] != 0 {
		t.Fatalf("expected boundary old[9] then new[0], got %v then %v", buf[9][0], buf[10][0])
	}
	if !old.closed {
		t.Fatal("expected old decoder to be closed at handoff")
	}
}

func TestSetPlaylistPublishesLength(t *testing.T) {
	a := &App{}
	a.setPlaylist([]string{"a", "b", "c"})
	if a.PlaylistLen() != 3 {
		t.Fatalf("expected len 3, got %d", a.PlaylistLen())
	}
	a.setPlaylist([]string{"a"})
	if a.PlaylistLen() != 1 {
		t.Fatalf("expected len 1, got %d", a.PlaylistLen())
	}
	a.setPlaylist(nil)
	if a.PlaylistLen() != 0 {
		t.Fatalf("expected len 0, got %d", a.PlaylistLen())
	}
}

// TestPlaylistLenConcurrentAccess exercises the published length against a
// concurrent reader; run under -race to verify the ownership model.
//
// TestPlaylistLenConcurrentAccess 在并发读取方下演练发布的长度值；
// 在 -race 下运行以验证所有权模型。
func TestPlaylistLenConcurrentAccess(t *testing.T) {
	a := &App{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 5000 {
			_ = a.PlaylistLen() > 1
		}
	}()
	for i := range 5000 {
		a.setPlaylist(make([]string, i%5))
	}
	<-done
}
