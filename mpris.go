package main

import (
	"encoding/base64"
	"fmt"
	"hash/fnv"
	"maps"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dhowden/tag"
	"github.com/godbus/dbus/v5"
	"github.com/gopxl/beep/v2/speaker"
)

// seekCoalesceWindow is how long a burst of seeks is held back so rapid
// position changes collapse into one Seeked signal carrying the final
// position, mirroring the idle batching of other MPRIS implementations.
//
// seekCoalesceWindow 是连续 seek 的合批窗口，窗口内的多次位置跳变只发
// 一次携带最终位置的 Seeked 信号，对齐其他 MPRIS 实现的空闲合批做法。
const seekCoalesceWindow = 50 * time.Millisecond

// seekJumpTolerance is how far the sampled position may deviate from the
// linear progression implied by the playback rate before the jump counts as a
// seek and is announced through the Seeked signal.
//
// seekJumpTolerance 是采样位置相对播放速率线性推进的允许偏差，
// 超出即视为跳转并通过 Seeked 信号广播。
const seekJumpTolerance = 1500 * time.Millisecond

// MPRISServer implements the D-Bus MPRIS2 specification.
//
// MPRISServer 实现了 D-Bus MPRIS2 规范。
type MPRISServer struct {
	conn         *dbus.Conn
	app          *App
	player       *audioPlayer
	flacPath     string
	trackID      dbus.ObjectPath
	playMode     int
	position     int64
	duration     int64
	metadata     map[string]dbus.Variant
	originalFile *os.File // Keep a reference to the original file for duration calculation. / 保留对原始文件的引用以计算时长。

	stopChan chan struct{} // Channel to signal goroutines to stop. / 用于通知 goroutine 停止的通道。
	stopped  bool          // Whether the server has been stopped. / 服务器是否已停止。

	stoppedPlayback bool        // Whether Stop ended playback until the next Play. / Stop 是否已结束播放，直到下次 Play。
	pendingSeek     bool        // Whether a coalesced Seeked signal is pending. / 是否有合批待发的 Seeked 信号。
	pendingSeekPos  int64       // Position the pending Seeked signal will carry. / 待发 Seeked 信号携带的位置。
	seekTimer       *time.Timer // Timer flushing the coalesced Seeked signal. / 触发合批 Seeked 信号的定时器。
	lastSampleUs    int64       // Last position sample for jump detection. / 跳变检测的上次位置采样。
	lastSampleAt    time.Time   // Time of the last position sample. / 上次位置采样的时间。

	// Guards position, metadata, trackID, playMode, stopped and the seek
	// bookkeeping, which are accessed from D-Bus handler goroutines and the
	// update loop.
	//
	// 保护 position、metadata、trackID、playMode、stopped 与 seek 记账字段，
	// 这些字段会被 D-Bus 处理 goroutine 和更新循环并发访问。
	mu sync.Mutex
}

// NewMPRISServer creates a new MPRIS server instance.
//
// NewMPRISServer 创建一个新的 MPRIS 服务端实例。
func NewMPRISServer(app *App, player *audioPlayer, flacPath string) (*MPRISServer, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("Failed to connect to D-Bus: %v\n\n连接 D-Bus 失败: %v", err, err)
	}

	f, err := os.Open(flacPath)
	if err != nil {
		return nil, fmt.Errorf("Failed to open file: %v\n\n打开文件失败: %v", err, err)
	}

	server := &MPRISServer{
		conn:         conn,
		app:          app,
		player:       player,
		flacPath:     flacPath,
		playMode:     app.playMode,
		metadata:     make(map[string]dbus.Variant),
		originalFile: f,
		stopChan:     make(chan struct{}),
		stopped:      false,
		lastSampleAt: time.Now(),
	}

	if err := server.calculateDuration(); err != nil {
		// Ignore duration calculation errors for now.
	}

	server.updateMetadata()

	return server, nil
}

// Start starts the MPRIS service.
//
// Start 启动 MPRIS 服务。
func (m *MPRISServer) Start() error {
	err := m.conn.Export(m, "/org/mpris/MediaPlayer2", "org.freedesktop.DBus.Properties")
	if err != nil {
		return fmt.Errorf("Failed to export Properties interface: %v\n\n导出 Properties 接口失败: %v", err, err)
	}

	err = m.conn.Export(m, "/org/mpris/MediaPlayer2", "org.mpris.MediaPlayer2")
	if err != nil {
		return fmt.Errorf("Failed to export MediaPlayer2 interface: %v\n\n导出 MediaPlayer2 接口失败: %v", err, err)
	}

	err = m.conn.Export(m, "/org/mpris/MediaPlayer2", "org.mpris.MediaPlayer2.Player")
	if err != nil {
		return fmt.Errorf("Failed to export Player interface: %v\n\n导出 Player 接口失败: %v", err, err)
	}

	reply, err := m.conn.RequestName("org.mpris.MediaPlayer2.bm", dbus.NameFlagDoNotQueue)
	if err != nil {
		return fmt.Errorf("Failed to request service name: %v\n\n请求服务名失败: %v", err, err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return fmt.Errorf("Service name is already taken\n\n服务名已被占用")
	}

	return nil
}

// StopService stops the MPRIS service.
//
// StopService 停止 MPRIS 服务。
func (m *MPRISServer) StopService() {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	if m.seekTimer != nil {
		m.seekTimer.Stop()
		m.seekTimer = nil
	}
	m.mu.Unlock()

	// Signal goroutines to stop
	close(m.stopChan)

	// Give goroutines a moment to exit
	time.Sleep(50 * time.Millisecond)

	if m.conn != nil {
		m.conn.ReleaseName("org.mpris.MediaPlayer2.bm")
		m.conn.Close()
	}
	if m.originalFile != nil {
		m.originalFile.Close()
	}
}

// UpdatePlaybackStatus updates the playback status.
//
// UpdatePlaybackStatus 更新播放状态。
func (m *MPRISServer) UpdatePlaybackStatus(playing bool) {
	if playing {
		m.mu.Lock()
		m.stoppedPlayback = false
		m.mu.Unlock()
	}
	m.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
		"PlaybackStatus": m.getPlaybackStatus(),
	})
}

// NotifySeek announces a position discontinuity to MPRIS clients by emitting
// the Seeked signal. Position is a polled property whose PropertiesChanged
// signal is never emitted; per the MPRIS specification clients only learn
// that playback stopped progressing according to Rate through Seeked, so
// every seek must emit it.
//
// NotifySeek 通过 Seeked 信号向 MPRIS 客户端广播位置跳变。
// Position 是只轮询属性，不发变更信号；按 MPRIS 规范，客户端只能通过
// Seeked 得知播放脱离 Rate 线性推进，因此每次跳转都必须发出。
func (m *MPRISServer) NotifySeek(pos int64) {
	m.mu.Lock()
	m.position = pos
	m.lastSampleUs = pos
	m.lastSampleAt = time.Now()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.pendingSeek = true
	m.pendingSeekPos = pos
	if m.seekTimer != nil {
		m.seekTimer.Stop()
	}
	m.seekTimer = time.AfterFunc(seekCoalesceWindow, m.flushSeeked)
	m.mu.Unlock()
}

// flushSeeked emits the coalesced Seeked signal carrying the position of the
// last seek in the burst.
//
// flushSeeked 发出合批后的 Seeked 信号，携带本轮最后一次跳转的位置。
func (m *MPRISServer) flushSeeked() {
	m.mu.Lock()
	if m.stopped || !m.pendingSeek {
		m.mu.Unlock()
		return
	}
	pos := m.pendingSeekPos
	m.pendingSeek = false
	m.seekTimer = nil
	conn := m.conn
	m.mu.Unlock()
	if conn == nil {
		return
	}
	conn.Emit(
		dbus.ObjectPath("/org/mpris/MediaPlayer2"),
		"org.mpris.MediaPlayer2.Player.Seeked",
		pos,
	)
}

// detectPositionJump reports whether the sampled position deviates from the
// linear progression implied by the playback rate beyond the tolerance, which
// means playback stopped progressing the way clients assume and they must be
// told through the Seeked signal.
//
// detectPositionJump 判断采样位置是否超出播放速率隐含的线性推进容差，
// 超出即表示播放脱离客户端假设的推进方式，须通过 Seeked 信号告知。
func (m *MPRISServer) detectPositionJump(pos int64, rate float64, paused bool) bool {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	elapsed := now.Sub(m.lastSampleAt).Seconds()
	expected := float64(m.lastSampleUs)
	if !paused {
		expected += elapsed * rate * 1e6
	}
	deviation := float64(pos) - expected
	m.lastSampleUs = pos
	m.lastSampleAt = now
	tolerance := float64(seekJumpTolerance.Microseconds())
	return deviation < -tolerance || deviation > tolerance
}

// getCurrentPosition returns the playback position in microseconds from the
// decoder that is actually streaming, so every client sees one timeline. The
// cached position only backs it up when no player exists.
//
// getCurrentPosition 以正在播放的解码器为准返回播放位置（微秒），
// 使所有客户端看到同一条时间线。缓存位置仅在无播放器时兜底。
func (m *MPRISServer) getCurrentPosition() int64 {
	if m.player != nil && m.player.streamer != nil {
		speaker.Lock()
		samplePos := m.player.streamer.Position()
		speaker.Unlock()
		if rate := float64(m.player.sampleRate); rate > 0 {
			pos := int64(float64(samplePos) / rate * 1e6)
			m.mu.Lock()
			m.position = pos
			m.mu.Unlock()
			return pos
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.position
}

// getLoopStatus maps the playback mode to the MPRIS loop status. Random mode
// reports "Playlist" because the playlist keeps playing; the Shuffle property
// carries the randomness.
//
// getLoopStatus 将播放模式映射为 MPRIS 循环状态。随机模式报告 "Playlist"，
// 因为歌单仍在循环播放；随机性由 Shuffle 属性表达。
func (m *MPRISServer) getLoopStatus() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.playMode == 0 {
		return "Track"
	}
	return "Playlist"
}

// getShuffle reports whether the playback mode is random.
//
// getShuffle 报告播放模式是否为随机。
func (m *MPRISServer) getShuffle() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.playMode == 2
}

// updatePlayMode refreshes the cached playback mode and announces the
// matching LoopStatus and Shuffle values. It runs on the main thread after the
// play mode changed.
//
// updatePlayMode 刷新缓存的播放模式并广播对应的 LoopStatus 与 Shuffle 值。
// 在播放模式变更后由主线程调用。
func (m *MPRISServer) updatePlayMode(mode int) {
	m.mu.Lock()
	m.playMode = mode
	m.mu.Unlock()
	m.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
		"LoopStatus": m.getLoopStatus(),
		"Shuffle":    m.getShuffle(),
	})
}

// UpdateMetadata updates the metadata.
//
// UpdateMetadata 更新元数据。
func (m *MPRISServer) UpdateMetadata() {
	m.updateMetadata()
	m.mu.Lock()
	metadata := m.metadata
	m.mu.Unlock()

	m.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
		"Metadata": metadata,
	})
}

// UpdateProperties sends a PropertiesChanged signal for CanGoNext and CanGoPrevious.
//
// UpdateProperties 发送 CanGoNext 和 CanGoPrevious 的 PropertiesChanged 信号。
func (m *MPRISServer) UpdateProperties() {
	m.mu.Lock()
	stopped := m.stopped
	m.mu.Unlock()
	if m.conn == nil || stopped {
		return
	}
	changedProperties := map[string]any{
		"CanGoNext":     m.app.PlaylistLen() > 1,
		"CanGoPrevious": m.app.PlaylistLen() > 1,
	}
	m.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", changedProperties)
}

// --- org.mpris.MediaPlayer2 interface implementation ---
// --- org.mpris.MediaPlayer2 接口实现 ---

// Quit quits the player.
//
// Quit 退出播放器。
func (m *MPRISServer) Quit() *dbus.Error {
	if m.app != nil {
		m.app.Quit()
	}
	return nil
}

// Raise raises the player window (not implemented).
//
// Raise 提升播放器窗口（未实现）。
func (m *MPRISServer) Raise() *dbus.Error {
	return nil
}

// CanQuit checks if the player can be quit.
//
// CanQuit 检查播放器是否可以退出。
func (m *MPRISServer) CanQuit() (bool, *dbus.Error) {
	return true, nil
}

// CanRaise checks if the player window can be raised.
//
// CanRaise 检查播放器窗口是否可以提升。
func (m *MPRISServer) CanRaise() (bool, *dbus.Error) {
	return false, nil
}

// HasTrackList checks if the player has a tracklist.
//
// HasTrackList 检查播放器是否有曲目列表。
func (m *MPRISServer) HasTrackList() (bool, *dbus.Error) {
	return false, nil
}

// Identity gets the player's identity.
//
// Identity 获取播放器的标识。
func (m *MPRISServer) Identity() (string, *dbus.Error) {
	return "BM", nil
}

// DesktopEntry gets the desktop entry name.
//
// DesktopEntry 获取桌面入口名称。
func (m *MPRISServer) DesktopEntry() (string, *dbus.Error) {
	return "", nil
}

// SupportedUriSchemes gets the supported URI schemes.
//
// SupportedUriSchemes 获取支持的URI方案。
func (m *MPRISServer) SupportedUriSchemes() ([]string, *dbus.Error) {
	return []string{"file"}, nil
}

// SupportedMimeTypes gets the supported MIME types.
//
// SupportedMimeTypes 获取支持的MIME类型。
func (m *MPRISServer) SupportedMimeTypes() ([]string, *dbus.Error) {
	return []string{"audio/flac"}, nil
}

// --- org.mpris.MediaPlayer2.Player interface implementation ---
// --- org.mpris.MediaPlayer2.Player 接口实现 ---

// Next plays the next track.
//
// Next 播放下一首曲目。
func (m *MPRISServer) Next() *dbus.Error {
	if m.app != nil {
		m.app.NextSong()
	}
	return nil
}

// Previous plays the previous track.
//
// Previous 播放上一首曲目。
func (m *MPRISServer) Previous() *dbus.Error {
	if m.app != nil {
		m.app.PreviousSong()
	}
	return nil
}

// Pause pauses the playback.
//
// Pause 暂停播放。
func (m *MPRISServer) Pause() *dbus.Error {
	if m.player != nil {
		speaker.Lock()
		wasPlaying := !m.player.ctrl.Paused
		m.player.ctrl.Paused = true
		speaker.Unlock()
		if wasPlaying {
			m.UpdatePlaybackStatus(false)
		}
	}
	return nil
}

// PlayPause toggles between play and pause.
//
// PlayPause 切换播放和暂停。
func (m *MPRISServer) PlayPause() *dbus.Error {
	if m.player != nil {
		speaker.Lock()
		m.player.ctrl.Paused = !m.player.ctrl.Paused
		playing := !m.player.ctrl.Paused
		speaker.Unlock()
		m.UpdatePlaybackStatus(playing)
	}
	return nil
}

// Stop stops playback and rewinds to the beginning of the track so a later
// Play starts from there, as the MPRIS specification requires.
//
// Stop 停止播放并回退到曲目开头，使之后的 Play 从头开始，
// 符合 MPRIS 规范要求。
func (m *MPRISServer) Stop() *dbus.Error {
	if m.player == nil {
		return nil
	}
	speaker.Lock()
	m.player.ctrl.Paused = true
	streamer := m.player.streamer
	speaker.Unlock()
	if streamer != nil {
		speaker.Lock()
		err := streamer.Seek(0)
		speaker.Unlock()
		if err != nil {
			l.Warnf("rewind failed: %v\n\n警告: 回退失败: %v", err, err)
		}
	}
	m.mu.Lock()
	m.stoppedPlayback = true
	m.mu.Unlock()
	m.NotifySeek(0)
	m.UpdatePlaybackStatus(false)
	return nil
}

// Play starts or resumes the playback.
//
// Play 开始或恢复播放。
func (m *MPRISServer) Play() *dbus.Error {
	if m.player != nil {
		speaker.Lock()
		wasPaused := m.player.ctrl.Paused
		m.player.ctrl.Paused = false
		speaker.Unlock()
		if wasPaused {
			m.UpdatePlaybackStatus(true)
		}
	}
	return nil
}

// Seek seeks the track by the given offset in microseconds.
//
// Seek 按给定的偏移量（微秒）在曲目中跳转。
func (m *MPRISServer) Seek(offset int64) (int64, *dbus.Error) {
	newPos := m.seekToUs(m.getCurrentPosition() + offset)
	return newPos, nil
}

// SetPosition sets the track's position in microseconds. Calls naming a track
// other than the current one are ignored as the MPRIS specification requires.
//
// SetPosition 设置曲目的位置（微秒）。按 MPRIS 规范要求，
// 指定曲目不是当前曲目时调用会被忽略。
func (m *MPRISServer) SetPosition(trackID dbus.ObjectPath, position int64) *dbus.Error {
	m.mu.Lock()
	current := m.trackID
	m.mu.Unlock()
	if trackID != current {
		return nil
	}
	m.seekToUs(position)
	return nil
}

// seekToUs seeks the streaming decoder to the given position in microseconds
// and reports where playback actually landed through the Seeked signal and a
// Position property change.
//
// seekToUs 将正在播放的解码器跳转到给定位置（微秒），并以 Seeked 信号
// 和 Position 属性变更报告实际落点。
func (m *MPRISServer) seekToUs(pos int64) int64 {
	pos = max(pos, 0)
	m.mu.Lock()
	if m.duration > 0 {
		pos = min(pos, m.duration-1)
	}
	m.mu.Unlock()

	if m.player != nil && m.player.streamer != nil {
		samplePos := int(float64(pos) / 1e6 * float64(m.player.sampleRate))
		speaker.Lock()
		err := m.player.streamer.Seek(samplePos)
		speaker.Unlock()
		if err != nil {
			l.Warnf("seek failed: %v\n\n警告: 跳转失败: %v", err, err)
		}
	}

	pos = m.getCurrentPosition()
	m.NotifySeek(pos)
	return pos
}

// OpenUri plays a local audio file addressed by a file URI. The song joins
// the playlist and plays in the repeat-one mode, mirroring what launching bm
// with a song argument does.
//
// OpenUri 播放 file URI 指向的本地音频文件。歌曲加入播放列表并以单曲循环
// 模式播放，与用歌曲参数启动 bm 的行为一致。
func (m *MPRISServer) OpenUri(uri string) *dbus.Error {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "file" {
		return dbus.MakeFailedError(fmt.Errorf("Unsupported URI: %s\n\n不支持的 URI: %s", uri, uri))
	}
	path, err := filepath.Abs(parsed.Path)
	if err != nil {
		return dbus.MakeFailedError(fmt.Errorf("Unable to resolve path: %v\n\n无法解析路径: %v", err, err))
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return dbus.MakeFailedError(fmt.Errorf("Unable to access audio file: %s\n\n无法访问音频文件: %s", path, path))
	}
	if !isAudioFile(path) {
		return dbus.MakeFailedError(fmt.Errorf("Unsupported audio file: %s\n\n不支持的音频文件: %s", path, path))
	}
	if m.app == nil {
		return dbus.MakeFailedError(fmt.Errorf("Player is not ready\n\n播放器未就绪"))
	}
	inside, err := isInsideDir(m.app.LibraryPath, path)
	if err != nil || !inside {
		return dbus.MakeFailedError(fmt.Errorf("The song is not inside your music folder (%s):\n%s\n\n这首歌不在你的歌曲文件夹内（%s）:\n%s", m.app.LibraryPath, path, m.app.LibraryPath, path))
	}
	m.app.actionQueue <- func() {
		if !slices.Contains(m.app.Playlist, path) {
			m.app.setPlaylist(append(m.app.Playlist, path))
			if err := SavePlaylist(m.app.Playlist, m.app.LibraryPath); err != nil {
				l.Warnf("could not save playlist: %v\n\n警告: 无法保存播放列表: %v", err, err)
			}
		}
		m.app.setPlayMode(0)
		if err := m.app.PlaySongWithSwitchAndRender(path, true, true); err != nil {
			l.Warnf("could not play %s: %v\n\n警告: 无法播放 %s: %v", path, err, path, err)
		}
	}
	return nil
}

// --- D-Bus Properties interface implementation ---
// --- D-Bus Properties 接口实现 ---

// Get implements D-Bus Properties.Get.
//
// Get 实现 D-Bus Properties.Get。
func (m *MPRISServer) Get(interfaceName, propertyName string) (dbus.Variant, *dbus.Error) {
	switch interfaceName {
	case "org.mpris.MediaPlayer2":
		switch propertyName {
		case "CanQuit":
			return dbus.MakeVariant(true), nil
		case "CanRaise":
			return dbus.MakeVariant(false), nil
		case "HasTrackList":
			return dbus.MakeVariant(false), nil
		case "Identity":
			return dbus.MakeVariant("BM"), nil
		case "DesktopEntry":
			return dbus.MakeVariant(""), nil
		case "SupportedUriSchemes":
			return dbus.MakeVariant([]string{"file"}), nil
		case "SupportedMimeTypes":
			return dbus.MakeVariant([]string{"audio/flac", "audio/mpeg", "audio/wav", "audio/ogg"}), nil
		}
	case "org.mpris.MediaPlayer2.Player":
		switch propertyName {
		case "PlaybackStatus":
			return dbus.MakeVariant(m.getPlaybackStatus()), nil
		case "LoopStatus":
			return dbus.MakeVariant(m.getLoopStatus()), nil
		case "Rate":
			if m.player != nil {
				speaker.Lock()
				ratio := m.player.resampler.Ratio()
				speaker.Unlock()
				return dbus.MakeVariant(ratio), nil
			}
			return dbus.MakeVariant(1.0), nil
		case "Shuffle":
			return dbus.MakeVariant(m.getShuffle()), nil
		case "Metadata":
			m.mu.Lock()
			metadata := m.metadata
			m.mu.Unlock()
			return dbus.MakeVariant(metadata), nil
		case "Volume":
			if m.app != nil {
				speaker.Lock()
				linearVolume := m.app.linearVolume
				speaker.Unlock()
				return dbus.MakeVariant(linearVolume), nil
			}
			return dbus.MakeVariant(1.0), nil
		case "Position":
			return dbus.MakeVariant(m.getCurrentPosition()), nil
		case "MinimumRate":
			return dbus.MakeVariant(0.1), nil
		case "MaximumRate":
			return dbus.MakeVariant(4.0), nil
		case "CanGoNext":
			return dbus.MakeVariant(m.app != nil && m.app.PlaylistLen() > 1), nil
		case "CanGoPrevious":
			return dbus.MakeVariant(m.app != nil && m.app.PlaylistLen() > 1), nil
		case "CanPlay":
			return dbus.MakeVariant(true), nil
		case "CanPause":
			return dbus.MakeVariant(true), nil
		case "CanSeek":
			return dbus.MakeVariant(true), nil
		case "CanControl":
			return dbus.MakeVariant(true), nil
		}
	}
	return dbus.Variant{}, dbus.MakeFailedError(fmt.Errorf("Unknown property: %s.%s\n\n未知属性: %s.%s", interfaceName, propertyName, interfaceName, propertyName))
}

// GetAll implements D-Bus Properties.GetAll.
//
// GetAll 实现 D-Bus Properties.GetAll。
func (m *MPRISServer) GetAll(interfaceName string) (map[string]dbus.Variant, *dbus.Error) {
	switch interfaceName {
	case "org.mpris.MediaPlayer2":
		props := make(map[string]dbus.Variant)
		props["CanQuit"] = dbus.MakeVariant(true)
		props["CanRaise"] = dbus.MakeVariant(false)
		props["HasTrackList"] = dbus.MakeVariant(false)
		props["Identity"] = dbus.MakeVariant("BM")
		props["DesktopEntry"] = dbus.MakeVariant("")
		props["SupportedUriSchemes"] = dbus.MakeVariant([]string{"file"})
		props["SupportedMimeTypes"] = dbus.MakeVariant([]string{"audio/flac", "audio/mpeg", "audio/wav", "audio/ogg"})
		return props, nil
	case "org.mpris.MediaPlayer2.Player":
		props := make(map[string]dbus.Variant)

		props["PlaybackStatus"] = dbus.MakeVariant(m.getPlaybackStatus())
		props["LoopStatus"] = dbus.MakeVariant(m.getLoopStatus())
		if m.player != nil {
			speaker.Lock()
			ratio := m.player.resampler.Ratio()
			speaker.Unlock()
			props["Rate"] = dbus.MakeVariant(ratio)
			if m.app != nil {
				speaker.Lock()
				linearVolume := m.app.linearVolume
				speaker.Unlock()
				props["Volume"] = dbus.MakeVariant(linearVolume)
			} else {
				props["Volume"] = dbus.MakeVariant(1.0)
			}
			props["Position"] = dbus.MakeVariant(m.getCurrentPosition())
		} else {
			props["Rate"] = dbus.MakeVariant(1.0)
			props["Volume"] = dbus.MakeVariant(1.0)
			props["Position"] = dbus.MakeVariant(m.position)
		}

		props["Shuffle"] = dbus.MakeVariant(m.getShuffle())
		m.mu.Lock()
		metadata := m.metadata
		m.mu.Unlock()
		props["Metadata"] = dbus.MakeVariant(metadata)
		props["MinimumRate"] = dbus.MakeVariant(0.1)
		props["MaximumRate"] = dbus.MakeVariant(4.0)
		props["CanGoNext"] = dbus.MakeVariant(m.app != nil && m.app.PlaylistLen() > 1)
		props["CanGoPrevious"] = dbus.MakeVariant(m.app != nil && m.app.PlaylistLen() > 1)
		props["CanPlay"] = dbus.MakeVariant(true)
		props["CanPause"] = dbus.MakeVariant(true)
		props["CanSeek"] = dbus.MakeVariant(true)
		props["CanControl"] = dbus.MakeVariant(true)

		return props, nil
	}
	return nil, dbus.MakeFailedError(fmt.Errorf("Unknown interface: %s\n\n未知接口: %s", interfaceName, interfaceName))
}

// Set implements D-Bus Properties.Set.
//
// Set 实现 D-Bus Properties.Set。
func (m *MPRISServer) Set(interfaceName, propertyName string, value dbus.Variant) *dbus.Error {
	switch interfaceName {
	case "org.mpris.MediaPlayer2.Player":
		switch propertyName {
		case "Volume":
			if m.app != nil {
				v, ok := value.Value().(float64)
				if !ok {
					return dbus.MakeFailedError(fmt.Errorf("invalid Volume value: %v", value.Value()))
				}
				linearVol := min(max(v, 0.0), 1.0)
				m.app.actionQueue <- func() {
					if m.app.player == nil {
						return
					}
					speaker.Lock()
					m.app.linearVolume = linearVol
					if m.app.linearVolume == 0 {
						m.app.volume = -10
					} else {
						m.app.volume = math.Log2(m.app.linearVolume)
					}
					m.app.player.volume.Volume = m.app.volume
					speaker.Unlock()
				}
				m.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
					"Volume": linearVol,
				})
			}
		case "Rate":
			if m.app != nil {
				v, ok := value.Value().(float64)
				if !ok {
					return dbus.MakeFailedError(fmt.Errorf("invalid Rate value: %v", value.Value()))
				}
				rate := min(max(v, 0.1), 4.0)
				m.app.actionQueue <- func() {
					if m.app.player == nil {
						return
					}
					speaker.Lock()
					m.app.playbackRate = rate
					m.app.player.resampler.SetRatio(rate)
					speaker.Unlock()
				}
				m.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
					"Rate": rate,
				})
			}
		case "LoopStatus":
			status, ok := value.Value().(string)
			if !ok {
				return dbus.MakeFailedError(fmt.Errorf("LoopStatus must be a string\n\nLoopStatus 必须是字符串"))
			}
			var mode int
			switch status {
			case "Track":
				mode = 0
			case "Playlist", "None":
				mode = 1
			default:
				return dbus.MakeFailedError(fmt.Errorf("Unknown loop status: %s\n\n未知循环状态: %s", status, status))
			}
			m.app.actionQueue <- func() { m.app.setPlayMode(mode) }
		case "Shuffle":
			enabled, ok := value.Value().(bool)
			if !ok {
				return dbus.MakeFailedError(fmt.Errorf("Shuffle must be a boolean\n\nShuffle 必须是布尔值"))
			}
			m.app.actionQueue <- func() {
				if enabled {
					m.app.setPlayMode(2)
				} else if m.app.playMode == 2 {
					m.app.setPlayMode(1)
				}
			}
		default:
			return dbus.MakeFailedError(fmt.Errorf("Property %s is not writable\n\n属性 %s 不可写", propertyName, propertyName))
		}
		return nil
	}
	return dbus.MakeFailedError(fmt.Errorf("Unknown interface: %s\n\n未知接口: %s", interfaceName, interfaceName))
}

// --- Helper Methods ---
// --- 辅助方法 ---

// getPlaybackStatus gets the playback status as a string.
//
// getPlaybackStatus 以字符串形式获取播放状态。
func (m *MPRISServer) getPlaybackStatus() string {
	if m.player == nil {
		return "Stopped"
	}
	m.mu.Lock()
	stopped := m.stoppedPlayback
	m.mu.Unlock()
	if stopped {
		return "Stopped"
	}
	speaker.Lock()
	paused := m.player.ctrl.Paused
	speaker.Unlock()
	if paused {
		return "Paused"
	}
	return "Playing"
}

// updateMetadata updates the track metadata.
//
// updateMetadata 更新曲目元数据。
func (m *MPRISServer) updateMetadata() {
	title, artist, album := getSongMetadata(m.flacPath)

	m.mu.Lock()
	defer m.mu.Unlock()

	h := fnv.New64a()
	h.Write([]byte(filepath.Clean(m.flacPath)))
	m.trackID = dbus.ObjectPath(fmt.Sprintf("/org/mpris/MediaPlayer2/TrackList/%016x", h.Sum64()))

	m.metadata = map[string]dbus.Variant{
		"mpris:trackid": dbus.MakeVariant(m.trackID),
		"mpris:length":  dbus.MakeVariant(int64(m.duration)),
		"xesam:title":   dbus.MakeVariant(title),
		"xesam:artist":  dbus.MakeVariant([]string{artist}),
		"xesam:album":   dbus.MakeVariant(album),
		"xesam:url":     dbus.MakeVariant((&url.URL{Scheme: "file", Path: m.flacPath}).String()),
	}

	maps.Copy(m.metadata, readSongExtraTags(m.flacPath, m.duration))

	if coverData := m.extractAlbumArt(); coverData != "" {
		m.metadata["mpris:artUrl"] = dbus.MakeVariant(coverData)
	}
}

// readSongExtraTags reads the extended Xesam metadata fields of an audio file.
// Fields the file does not carry are left out of the result.
//
// readSongExtraTags 读取音频文件的扩展 Xesam 元数据字段。
// 文件中缺失的字段不会出现在结果中。
func readSongExtraTags(flacPath string, durationUs int64) map[string]dbus.Variant {
	tags := make(map[string]dbus.Variant)
	f, err := os.Open(flacPath)
	if err != nil {
		return tags
	}
	defer f.Close()
	md, err := tag.ReadFrom(f)
	if err != nil {
		return tags
	}
	if genre := md.Genre(); genre != "" {
		tags["xesam:genre"] = dbus.MakeVariant([]string{genre})
	}
	if albumArtist := md.AlbumArtist(); albumArtist != "" {
		tags["xesam:albumArtist"] = dbus.MakeVariant([]string{albumArtist})
	}
	if track, _ := md.Track(); track > 0 {
		tags["xesam:trackNumber"] = dbus.MakeVariant(int32(track))
	}
	if disc, _ := md.Disc(); disc > 0 {
		tags["xesam:discNumber"] = dbus.MakeVariant(int32(disc))
	}
	if year := md.Year(); year > 0 {
		tags["xesam:contentCreated"] = dbus.MakeVariant(fmt.Sprintf("%04d-01-01T00:00:00Z", year))
	}
	if durationUs > 0 {
		if info, err := os.Stat(flacPath); err == nil {
			tags["xesam:audioBitrate"] = dbus.MakeVariant(int32(float64(info.Size()*8) / (float64(durationUs) / 1e6)))
		}
	}
	return tags
}

// extractAlbumArt extracts the album art.
// If the audio file has no cover, it uses the default cover image.
//
// extractAlbumArt 提取专辑封面。
// 如果音频文件没有封面，则使用默认封面图片。
func (m *MPRISServer) extractAlbumArt() string {
	// Try to get cover from audio file
	f, err := os.Open(m.flacPath)
	if err != nil {
		return m.getDefaultCoverArt()
	}
	defer f.Close()

	metadata, err := tag.ReadFrom(f)
	if err != nil {
		return m.getDefaultCoverArt()
	}

	if pic := metadata.Picture(); pic != nil {
		return m.encodePictureToBase64(pic)
	}

	return m.getDefaultCoverArt()
}

// getDefaultCoverArt returns the default cover art as a base64 encoded data URL.
//
// getDefaultCoverArt 返回默认封面图片的base64编码数据URL。
func (m *MPRISServer) getDefaultCoverArt() string {
	defaultCoverPath := getDefaultCoverPath()
	if defaultCoverPath == "" {
		return ""
	}

	data, err := os.ReadFile(defaultCoverPath)
	if err != nil {
		return ""
	}

	// Determine MIME type from file extension
	ext := strings.ToLower(filepath.Ext(defaultCoverPath))
	var mimeType string
	switch ext {
	case ".jpg", ".jpeg":
		mimeType = "image/jpeg"
	case ".png":
		mimeType = "image/png"
	default:
		return ""
	}

	return fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(data))
}

// encodePictureToBase64 saves the picture data to a temporary file and returns its file URL.
//
// encodePictureToBase64 将图片数据保存到临时文件并返回其文件URL。
func (m *MPRISServer) encodePictureToBase64(pic *tag.Picture) string {
	var fileExt string
	switch pic.MIMEType {
	case "image/jpeg", "image/jpg":
		fileExt = "jpg"
	case "image/png":
		fileExt = "png"
	default:
		if len(pic.Data) > 8 && pic.Data[0] == 0x89 && pic.Data[1] == 0x50 && pic.Data[2] == 0x4E && pic.Data[3] == 0x47 {
			fileExt = "png"
		} else if len(pic.Data) > 2 && pic.Data[0] == 0xFF && pic.Data[1] == 0xD8 {
			fileExt = "jpg"
		} else {
			fileExt = "jpg"
		}
	}

	tempFile, err := os.CreateTemp("", "bm_cover_*."+fileExt)
	if err != nil {
		return ""
	}
	defer tempFile.Close()

	if _, err := tempFile.Write(pic.Data); err != nil {
		return ""
	}

	trackTempCoverFile(tempFile.Name())
	return "file://" + tempFile.Name()
}

// sendPropertiesChanged sends a PropertiesChanged signal.
//
// sendPropertiesChanged 发送 PropertiesChanged 信号。
func (m *MPRISServer) sendPropertiesChanged(interfaceName string, changedProperties map[string]any) {
	m.mu.Lock()
	stopped := m.stopped
	m.mu.Unlock()
	if m.conn == nil || stopped {
		return
	}

	m.conn.Emit(
		dbus.ObjectPath("/org/mpris/MediaPlayer2"),
		"org.freedesktop.DBus.Properties.PropertiesChanged",
		interfaceName,
		changedProperties,
		[]string{},
	)
}

// calculateDuration calculates the audio duration in microseconds.
//
// calculateDuration 计算音频时长（以微秒为单位）。
func (m *MPRISServer) calculateDuration() error {
	streamer, format, err := decodeAudioFile(m.flacPath)
	if err != nil {
		return fmt.Errorf("Failed to decode file: %v\n\n解码文件失败: %v", err, err)
	}
	defer streamer.Close()

	totalSamples := streamer.Len()
	duration := int64(float64(totalSamples) / float64(format.SampleRate) * 1e6)

	m.mu.Lock()
	m.duration = duration
	m.mu.Unlock()

	return nil
}

// UpdateSong re-targets the server at a new track after a seamless queue
// handoff, refreshing duration, metadata and position without a service
// restart.
//
// UpdateSong 在队列无缝换源后将服务重新指向新曲目，刷新时长、元数据与位置，
// 无需重启服务。
func (m *MPRISServer) UpdateSong(path string) {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.flacPath = path
	m.position = 0
	m.lastSampleUs = 0
	m.lastSampleAt = time.Now()
	m.stoppedPlayback = false
	m.mu.Unlock()

	m.calculateDuration()
	m.updateMetadata()

	m.mu.Lock()
	metadata := m.metadata
	m.mu.Unlock()
	m.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
		"Metadata": metadata,
	})
}

// StartUpdateLoop keeps the cached position fresh for the rare callers that
// read it while no client is polling.
//
// StartUpdateLoop 在无客户端轮询时保持缓存位置新鲜，
// 供少数直接读取缓存的调用方使用。
func (m *MPRISServer) StartUpdateLoop() {
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-m.stopChan:
				return
			case <-ticker.C:
				m.mu.Lock()
				stopped := m.stopped
				m.mu.Unlock()
				if stopped {
					return
				}
				pos := m.getCurrentPosition()
				rate, paused := 1.0, true
				if m.player != nil {
					speaker.Lock()
					rate = m.player.resampler.Ratio()
					paused = m.player.ctrl.Paused
					speaker.Unlock()
				}
				if m.detectPositionJump(pos, rate, paused) {
					m.NotifySeek(pos)
				}
			}
		}
	}()
}
