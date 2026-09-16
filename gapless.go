package main

import (
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/speaker"
)

// predecodeWindow is how much remaining playback time triggers the background
// decode of the next song.
//
// predecodeWindow 是当前歌曲剩余多少播放时间时触发后台预解码下一首。
const predecodeWindow = 15 * time.Second

// queuedSong is a decoder prepared to start right after the current one.
//
// queuedSong 是准备好接在当前解码器之后播放的解码器。
type queuedSong struct {
	decoder beep.StreamSeekCloser
	path    string
}

// gaplessQueue sequences decoders back to back. The audio thread pulls from it
// through the playback chain; when the current decoder drains and another one
// is queued, the handoff happens inside Stream, so the mixer never observes a
// gap and the speaker buffers never run dry.
//
// All Stream calls run while the speaker lock is held; q.mu only orders those
// against the main thread's setNext/takeNext/path/hasNext calls.
//
// gaplessQueue 将解码器首尾相接顺序播放。音频线程通过播放链从它拉取数据；
// 当前解码器耗尽且已排入下一首时，换源在 Stream 内部完成，混音器永远不会
// 观察到间隙，扬声器缓冲也不会枯竭。
//
// 所有 Stream 调用都在 speaker 锁内执行；q.mu 仅用于与主线程的
// setNext/takeNext/path/hasNext 调用同步。
type gaplessQueue struct {
	mu          sync.Mutex
	current     beep.StreamSeekCloser
	currentPath string
	next        *queuedSong
	player      *audioPlayer

	exhausted    atomic.Bool // current drained with nothing queued. / 当前流耗尽且没有排入下一首。
	exhaustedErr atomic.Bool // current errored instead of reaching clean EOF. / 当前流出错而非正常播放到结尾。

	// onExhausted is invoked from the audio thread once when the queue exhausts;
	// it must not block. It lets the fallback advance start immediately instead
	// of waiting up to one UI tick.
	//
	// onExhausted 在队列耗尽时由音频线程调用一次；不得阻塞。它让兜底推进立即启动，
	// 而不是等待最多一个 UI tick。
	onExhausted func()
}

func newGaplessQueue(current beep.StreamSeekCloser, path string) *gaplessQueue {
	return &gaplessQueue{current: current, currentPath: path}
}

// Stream implements beep.Streamer. The handoff to the queued decoder happens
// inside this call, so the mixer never sees a partial buffer at the boundary.
func (q *gaplessQueue) Stream(samples [][2]float64) (int, bool) {
	total := 0
	for len(samples) > 0 {
		q.mu.Lock()
		n, ok := q.current.Stream(samples)
		total += n
		atEnd := !ok || q.current.Position() >= q.current.Len()
		if !atEnd {
			q.mu.Unlock()
			if n == 0 {
				return total, true
			}
			samples = samples[n:]
			continue
		}
		if err := q.current.Err(); err != nil {
			q.exhaustedErr.Store(true)
			q.exhausted.Store(true)
			q.mu.Unlock()
			q.fireExhausted()
			return total, false
		}
		if q.next == nil {
			q.exhausted.Store(true)
			q.mu.Unlock()
			q.fireExhausted()
			return total, false
		}
		old := q.current
		q.current = q.next.decoder
		q.currentPath = q.next.path
		q.next = nil
		if q.player != nil {
			q.player.streamer = q.current
		}
		q.mu.Unlock()
		old.Close()
		samples = samples[n:]
	}
	return total, true
}

// Err implements beep.Streamer.
func (q *gaplessQueue) Err() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.current.Err()
}

// fireExhausted invokes the exhaust callback from the audio thread. The chain
// is removed by the mixer in the same Stream call, so this fires once per arm.
//
// fireExhausted 在音频线程触发耗尽回调。mixer 会在同一次 Stream 调用中移除链，
// 因此每次武装只会触发一次。
func (q *gaplessQueue) fireExhausted() {
	if q.onExhausted != nil {
		q.onExhausted()
	}
}

// setNext queues a decoder to start seamlessly when the current one drains.
// Any previously queued song is closed and replaced.
//
// setNext 排入一个解码器，在当前解码器耗尽时无缝接续。之前排入的歌曲会被
// 关闭并替换。
func (q *gaplessQueue) setNext(decoder beep.StreamSeekCloser, path string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.next != nil {
		q.next.decoder.Close()
	}
	q.next = &queuedSong{decoder: decoder, path: path}
}

// takeNext removes and returns the queued song, if any.
//
// takeNext 移除并返回已排入的歌曲（如果存在）。
func (q *gaplessQueue) takeNext() *queuedSong {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := q.next
	q.next = nil
	return n
}

// hasNext reports whether a handoff is armed.
//
// hasNext 报告是否已排入接续解码器。
func (q *gaplessQueue) hasNext() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.next != nil
}

// resetTo re-arms the queue around a fresh decoder after it was drained.
//
// resetTo 在队列耗尽后用新解码器重新武装队列。
func (q *gaplessQueue) resetTo(decoder beep.StreamSeekCloser, path string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.current = decoder
	q.currentPath = path
	q.next = nil
	q.exhausted.Store(false)
	q.exhaustedErr.Store(false)
}

// path returns the path of the decoder currently being streamed.
//
// path 返回当前正在流式播放的解码器对应的路径。
func (q *gaplessQueue) path() string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.currentPath
}

// tickPlayback advances the gapless machinery once per UI tick regardless of
// the visible page: applies state after a seamless queue handoff, arms the
// background predecode, and drives the fallback advance at true EOF.
//
// tickPlayback 每个 UI tick 执行一次，与当前可见页面无关：在队列无缝换源后
// 同步状态、武装后台预解码，并在真实 EOF 时驱动兜底推进。
func (a *App) tickPlayback() {
	if a.player == nil || len(a.Playlist) == 0 {
		return
	}

	if a.player.queue != nil {
		if hp := a.player.queue.path(); hp != a.currentSongPath {
			a.finishAutoSongSwitch(hp)
		}
	}

	a.prepareNextIfNeeded()

	if a.playMode != 0 {
		speaker.Lock()
		pos := a.player.streamer.Position()
		total := a.player.streamer.Len()
		speaker.Unlock()
		if total > 0 && pos >= total {
			a.handleAutoAdvance()
		}
	}
}

// computeNextPath determines which song follows currentPath under the active
// play mode, mirroring the manual next-song selection.
//
// computeNextPath 按当前播放模式确定 currentPath 之后的歌曲，与手动切歌的选择逻辑一致。
func (a *App) computeNextPath(currentPath string) (string, bool) {
	if len(a.Playlist) == 0 {
		return "", false
	}
	idx := slices.Index(a.Playlist, currentPath)
	if idx < 0 {
		return "", false
	}
	switch a.playMode {
	case 1:
		return a.Playlist[(idx+1)%len(a.Playlist)], true
	case 2:
		if a.isNavigatingHistory && a.historyIndex >= 0 && a.historyIndex < len(a.playHistory)-1 {
			return a.playHistory[a.historyIndex+1], true
		}
		return a.Playlist[a.pickRandomIndex(currentPath)], true
	default:
		return "", false
	}
}

// prepareNextIfNeeded decodes the next song in the background once the current
// one is within the predecode window of its end. Same-rate songs are queued
// into the gapless queue for a seamless handoff; songs needing a sample-rate
// change are held for the advance path, which still skips the decode wait.
//
// prepareNextIfNeeded 在当前歌曲进入预解码窗口后于后台解码下一首。采样率相同的
// 歌曲排入无缝队列；需要变更采样率的歌曲暂存给推进路径，仍然省去解码等待。
func (a *App) prepareNextIfNeeded() {
	if a.player == nil || a.player.queue == nil || a.isSingleSongMode {
		return
	}
	if a.playMode != 1 && a.playMode != 2 || len(a.Playlist) == 0 {
		return
	}

	speaker.Lock()
	pos := a.player.streamer.Position()
	total := a.player.streamer.Len()
	speaker.Unlock()
	if total <= 0 || pos > total {
		return
	}
	if total-pos > a.player.sampleRate.N(predecodeWindow) {
		return
	}

	q := a.player.queue
	a.pendingMu.Lock()
	if a.pendingPreparing || a.pendingDecoder != nil || q.hasNext() {
		a.pendingMu.Unlock()
		return
	}
	a.pendingPreparing = true
	token := a.pendingToken
	a.pendingMu.Unlock()

	path, ok := a.computeNextPath(a.currentSongPath)
	if !ok {
		a.pendingMu.Lock()
		a.pendingPreparing = false
		a.pendingMu.Unlock()
		return
	}

	go func() {
		dec, format, err := decodeAudioFile(path)
		if err != nil {
			a.actionQueue <- func() {
				a.MarkFileAsCorrupted(path)
				a.pendingMu.Lock()
				a.pendingPreparing = false
				a.pendingMu.Unlock()
			}
			return
		}

		a.pendingMu.Lock()
		if token != a.pendingToken {
			a.pendingMu.Unlock()
			dec.Close()
			return
		}
		a.pendingPreparing = false
		if format.SampleRate == a.sampleRate && a.player != nil && a.player.queue == q {
			q.setNext(dec, path)
			a.pendingMu.Unlock()
			return
		}
		if format.SampleRate != a.sampleRate {
			a.pendingDecoder = dec
			a.pendingPath = path
			a.pendingFormat = format
		} else {
			dec.Close()
		}
		a.pendingMu.Unlock()
	}()
}

// armQueueExhaust wires the queue's exhaust callback so the fallback advance
// starts immediately instead of waiting up to one UI tick. The send is
// non-blocking; the tick safety net remains as a fallback.
//
// armQueueExhaust 接上队列的耗尽回调，使兜底推进立即启动而非等待最多一个
// UI tick。发送为非阻塞；tick 安全网仍作兜底。
func (a *App) armQueueExhaust(q *gaplessQueue) {
	q.onExhausted = func() {
		select {
		case a.actionQueue <- a.handleAutoAdvance:
		default:
		}
	}
}

// invalidatePendingNext drops any prepared decoder (queued, held or in
// flight); called whenever the playlist, mode or current song changes.
//
// invalidatePendingNext 丢弃所有已准备的解码器（已排入、暂存或在途）；
// 播放列表、模式或当前歌曲变化时调用。
func (a *App) invalidatePendingNext() {
	a.pendingMu.Lock()
	a.pendingToken++
	dec := a.pendingDecoder
	a.pendingDecoder = nil
	a.pendingPath = ""
	a.pendingPreparing = false
	var queued *queuedSong
	if a.player != nil && a.player.queue != nil {
		queued = a.player.queue.takeNext()
	}
	a.pendingMu.Unlock()
	if dec != nil {
		dec.Close()
	}
	if queued != nil {
		queued.decoder.Close()
	}
}

// handleAutoAdvance switches to the next song after the queue exhausted at
// true EOF, which happens when the predecode lost the race or the next track
// needs a sample-rate change. The whole song has already played; only the
// transition gap remains, and only for the rate-change case. Idempotent.
//
// handleAutoAdvance 在队列于真实 EOF 处耗尽后切换到下一首——预解码未赶上或
// 下一首需要变更采样率时发生。整首歌已完整播放；仅换源间隙残留，且仅限
// 采样率变化的情形。幂等。
func (a *App) handleAutoAdvance() {
	if a.player == nil || a.player.queue == nil {
		return
	}
	q := a.player.queue
	if !q.exhausted.Load() || q.path() != a.currentSongPath {
		return
	}
	if a.advancing.Swap(true) {
		return
	}
	defer a.advancing.Store(false)

	if q.exhaustedErr.Load() {
		a.MarkFileAsCorrupted(a.currentSongPath)
	}

	nextPath, ok := a.computeNextPath(a.currentSongPath)
	if !ok {
		a.stopCurrentPlayback()
		a.setCurrentSong("")
		if playerPage, ok2 := a.pages[0].(*PlayerPage); ok2 {
			playerPage.UpdateSong("")
		}
		return
	}

	a.pendingMu.Lock()
	dec := a.pendingDecoder
	format := a.pendingFormat
	ppath := a.pendingPath
	a.pendingDecoder, a.pendingPath = nil, ""
	a.pendingMu.Unlock()
	if dec != nil && ppath != nextPath {
		dec.Close()
		dec = nil
	}

	if dec == nil {
		tried := map[string]bool{a.currentSongPath: true}
		for range len(a.Playlist) + 1 {
			tried[nextPath] = true
			var err error
			var format2 beep.Format
			dec, format2, err = decodeAudioFile(nextPath)
			if err == nil {
				format = format2
				break
			}
			a.MarkFileAsCorrupted(nextPath)
			np, ok2 := a.computeNextPath(nextPath)
			if !ok2 || tried[np] {
				dec = nil
				break
			}
			nextPath = np
		}
		if dec == nil {
			a.stopCurrentPlayback()
			a.setCurrentSong("")
			if playerPage, ok2 := a.pages[0].(*PlayerPage); ok2 {
				playerPage.UpdateSong("")
			}
			return
		}
	}

	if format.SampleRate != a.sampleRate {
		if err := speaker.ReInit(format.SampleRate, format.SampleRate.N(time.Second/30)); err != nil {
			dec.Close()
			l.Warnf("failed to reinit speaker for auto advance: %v\n\n警告: 自动切歌时重新初始化扬声器失败: %v", err, err)
			return
		}
		a.sampleRate = format.SampleRate
	}

	speaker.Lock()
	old := a.player.streamer
	a.player.streamer = dec
	a.player.queue.resetTo(dec, nextPath)
	speaker.Unlock()
	old.Close()

	speaker.Play(a.player.volume)
	a.finishAutoSongSwitch(nextPath)
}

// finishAutoSongSwitch applies playlist history, storage, MPRIS and UI state
// after the audio has already switched songs (queue handoff or advance).
//
// finishAutoSongSwitch 在音频已切换歌曲（队列换源或推进）后应用播放历史、
// 存储、MPRIS 与界面状态。
func (a *App) finishAutoSongSwitch(path string) {
	if path == a.currentSongPath {
		return
	}
	if a.playMode == 2 && a.isNavigatingHistory && a.historyIndex >= 0 && a.historyIndex < len(a.playHistory)-1 && a.playHistory[a.historyIndex+1] == path {
		a.historyIndex++
	} else {
		a.addToPlayHistory(path)
	}
	a.setCurrentSong(path)
	if a.mprisServer != nil {
		a.mprisServer.UpdateSong(path)
	}
	if playerPage, ok := a.pages[0].(*PlayerPage); ok {
		playerPage.UpdateSong(path)
		if a.currentPageIndex == 0 {
			playerPage.View()
		}
	}
	if len(a.Playlist) > 1 {
		title, artist, _ := getSongMetadata(path)
		coverPath := saveCoverArt(path)
		sendNotification(artist, title, coverPath)
	}
}

// rebuildChainForPlaybackMode swaps the chain's inner streamer after the play
// mode changed: repeat-one keeps looping through Loop2, list/random modes play
// through the gapless queue. The decoder and its position are preserved.
//
// rebuildChainForPlaybackMode 在播放模式变化后替换链的内层流：单曲循环继续用
// Loop2 循环，列表/随机模式走无缝队列。解码器及其位置保持不变。
func (a *App) rebuildChainForPlaybackMode() {
	if a.player == nil || a.isSingleSongMode {
		return
	}
	speaker.Lock()
	dec := a.player.streamer
	wantQueue := a.playMode == 1 || a.playMode == 2
	switch {
	case wantQueue && a.player.queue == nil:
		q := newGaplessQueue(dec, a.currentSongPath)
		q.player = a.player
		a.armQueueExhaust(q)
		a.player.queue = q
		a.player.ctrl.Streamer = q
	case !wantQueue && a.player.queue != nil:
		if n := a.player.queue.takeNext(); n != nil {
			n.decoder.Close()
		}
		ls, err := beep.Loop2(dec)
		if err != nil {
			speaker.Unlock()
			l.Warnf("failed to rebuild loop streamer: %v\n\n警告: 重建循环流失败: %v", err, err)
			return
		}
		a.player.queue = nil
		a.player.ctrl.Streamer = ls
	default:
		speaker.Unlock()
		return
	}
	speaker.Unlock()
	a.invalidatePendingNext()
}
