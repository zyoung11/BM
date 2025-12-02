package main

import (
	"fmt"
	"math"
	"os"
	"time"

	"github.com/dhowden/tag"
	"github.com/godbus/dbus/v5"
	"github.com/gopxl/beep/v2/flac"
	"github.com/gopxl/beep/v2/speaker"
)

// MPRISServer implements the D-Bus MPRIS2 specification.
//
// MPRISServer 实现了 D-Bus MPRIS2 规范。
type MPRISServer struct {
	conn         *dbus.Conn
	app          *App
	player       *audioPlayer
	flacPath     string
	isPlaying    bool
	position     int64
	duration     int64
	metadata     map[string]dbus.Variant
	originalFile *os.File  // Keep a reference to the original file for duration calculation. / 保留对原始文件的引用以计算时长。
	lastUpdate   time.Time // Time of the last update. / 上次更新的时间。
	startTime    time.Time // Time when playback started. / 播放开始的时间。

	stopChan chan struct{} // Channel to signal goroutines to stop. / 用于通知 goroutine 停止的通道。
	stopped  bool          // Whether the server has been stopped. / 服务器是否已停止。
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
		isPlaying:    false,
		position:     0,
		duration:     0,
		metadata:     make(map[string]dbus.Variant),
		originalFile: f,
		lastUpdate:   time.Now(),
		startTime:    time.Time{},
		stopChan:     make(chan struct{}),
		stopped:      false,
	}

	server.updateMetadata()

	if err := server.calculateDuration(); err != nil {
		// Ignore duration calculation errors for now.
	}

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
	if m.stopped {
		return
	}
	m.stopped = true

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
	if playing && !m.isPlaying {
		m.startTime = time.Now().Add(-time.Duration(m.position) * time.Microsecond)
	} else if !playing && m.isPlaying {
		m.updatePositionFromTime()
	}
	m.isPlaying = playing
	m.lastUpdate = time.Now()
	m.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
		"PlaybackStatus": m.getPlaybackStatus(),
	})
}

// UpdatePosition updates the playback position.
//
// UpdatePosition 更新播放位置。
func (m *MPRISServer) UpdatePosition(pos int64) {
	m.position = pos
	m.lastUpdate = time.Now()
	m.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
		"Position": m.position,
	})
}

// updatePositionFromTime updates the position based on elapsed time.
//
// updatePositionFromTime 根据经过的时间更新位置。
func (m *MPRISServer) updatePositionFromTime() {
	if !m.isPlaying || m.startTime.IsZero() {
		return
	}

	elapsed := time.Since(m.startTime)
	newPosition := int64(elapsed.Microseconds())

	if m.duration > 0 && newPosition >= m.duration {
		newPosition = newPosition % m.duration
		m.startTime = time.Now().Add(-time.Duration(newPosition) * time.Microsecond)
	}

	if newPosition != m.position {
		m.position = newPosition
		m.lastUpdate = time.Now()
	}
}

// getCurrentPosition gets the current position in microseconds.
//
// getCurrentPosition 获取当前位置（以微秒为单位）。
func (m *MPRISServer) getCurrentPosition() int64 {
	if m.isPlaying && !m.startTime.IsZero() {
		m.updatePositionFromTime()
	}
	return m.position
}

// getLoopStatus gets the loop status.
// Accessing App.playMode here would cause a circular dependency, so it returns a default value.
//
// getLoopStatus 获取循环状态。
// 此处访问 App.playMode 会导致循环依赖，因此返回默认值。
func (m *MPRISServer) getLoopStatus() string {
	return "None"
}

// UpdateMetadata updates the metadata.
//
// UpdateMetadata 更新元数据。
func (m *MPRISServer) UpdateMetadata() {
	m.updateMetadata()
	m.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
		"Metadata": m.metadata,
	})
}

// UpdateProperties sends a PropertiesChanged signal for CanGoNext and CanGoPrevious.
//
// UpdateProperties 发送 CanGoNext 和 CanGoPrevious 的 PropertiesChanged 信号。
func (m *MPRISServer) UpdateProperties() {
	if m.conn == nil || m.stopped {
		return
	}
	changedProperties := map[string]any{
		"CanGoNext":     len(m.app.Playlist) > 1,
		"CanGoPrevious": len(m.app.Playlist) > 1,
	}
	m.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", changedProperties)
}

// --- org.mpris.MediaPlayer2 interface implementation ---
// --- org.mpris.MediaPlayer2 接口实现 ---

// Quit quits the player.
//
// Quit 退出播放器。
func (m *MPRISServer) Quit() *dbus.Error {
	os.Exit(0)
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
	if m.player != nil && !m.player.ctrl.Paused {
		m.player.ctrl.Paused = true
		m.UpdatePlaybackStatus(false)
	}
	return nil
}

// PlayPause toggles between play and pause.
//
// PlayPause 切换播放和暂停。
func (m *MPRISServer) PlayPause() *dbus.Error {
	if m.player != nil {
		m.player.ctrl.Paused = !m.player.ctrl.Paused
		m.UpdatePlaybackStatus(!m.player.ctrl.Paused)
	}
	return nil
}

// Stop stops the playback.
//
// Stop 停止播放。
func (m *MPRISServer) Stop() *dbus.Error {
	if m.player != nil {
		m.player.ctrl.Paused = true
		m.UpdatePlaybackStatus(false)
	}
	return nil
}

// Play starts or resumes the playback.
//
// Play 开始或恢复播放。
func (m *MPRISServer) Play() *dbus.Error {
	if m.player != nil && m.player.ctrl.Paused {
		m.player.ctrl.Paused = false
		m.UpdatePlaybackStatus(true)
	}
	return nil
}

// Seek seeks the track by the given offset in microseconds.
//
// Seek 按给定的偏移量（微秒）在曲目中跳转。
func (m *MPRISServer) Seek(offset int64) (int64, *dbus.Error) {
	currentPos := m.getCurrentPosition()
	newPos := currentPos + offset
	if newPos < 0 {
		newPos = 0
	}
	if newPos >= m.duration {
		newPos = m.duration - 1
	}

	m.position = newPos
	if m.isPlaying {
		m.startTime = time.Now().Add(-time.Duration(newPos) * time.Microsecond)
	}
	m.lastUpdate = time.Now()

	if m.player != nil && m.player.streamer != nil {
		samplePos := int(float64(newPos) / 1e6 * float64(m.player.sampleRate))
		speaker.Lock()
		if err := m.player.streamer.Seek(samplePos); err != nil {
			// Ignore seek errors
		}
		speaker.Unlock()
	}

	m.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
		"Position": m.position,
	})
	return newPos, nil
}

// SetPosition sets the track's position in microseconds.
//
// SetPosition 设置曲目的位置（微秒）。
func (m *MPRISServer) SetPosition(trackID dbus.ObjectPath, position int64) *dbus.Error {
	if position < 0 {
		position = 0
	}
	if position >= m.duration {
		position = m.duration - 1
	}

	m.position = position
	if m.isPlaying {
		m.startTime = time.Now().Add(-time.Duration(position) * time.Microsecond)
	}
	m.lastUpdate = time.Now()

	if m.player != nil && m.player.streamer != nil {
		samplePos := int(float64(position) / 1e6 * float64(m.player.sampleRate))
		speaker.Lock()
		if err := m.player.streamer.Seek(samplePos); err != nil {
			// Ignore seek errors
		}
		speaker.Unlock()
	}

	m.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
		"Position": m.position,
	})
	return nil
}

// OpenUri opens a URI (not supported).
//
// OpenUri 打开一个URI（不支持）。
func (m *MPRISServer) OpenUri(uri string) *dbus.Error {
	return dbus.MakeFailedError(fmt.Errorf("Opening URI is not supported\n\n不支持打开 URI"))
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
			return dbus.MakeVariant([]string{"audio/flac"}), nil
		}
	case "org.mpris.MediaPlayer2.Player":
		switch propertyName {
		case "PlaybackStatus":
			return dbus.MakeVariant(m.getPlaybackStatus()), nil
		case "LoopStatus":
			return dbus.MakeVariant("None"), nil
		case "Rate":
			if m.player != nil {
				return dbus.MakeVariant(m.player.resampler.Ratio()), nil
			}
			return dbus.MakeVariant(1.0), nil
		case "Shuffle":
			return dbus.MakeVariant(false), nil
		case "Metadata":
			return dbus.MakeVariant(m.metadata), nil
		case "Volume":
			if m.app != nil {
				return dbus.MakeVariant(m.app.linearVolume), nil
			}
			return dbus.MakeVariant(1.0), nil
		case "Position":
			return dbus.MakeVariant(m.getCurrentPosition()), nil
		case "MinimumRate":
			return dbus.MakeVariant(0.1), nil
		case "MaximumRate":
			return dbus.MakeVariant(4.0), nil
		case "CanGoNext":
			canGoNext := m.app != nil && len(m.app.Playlist) > 1
			return dbus.MakeVariant(canGoNext), nil
		case "CanGoPrevious":
			canGoPrevious := m.app != nil && len(m.app.Playlist) > 1
			return dbus.MakeVariant(canGoPrevious), nil
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
	if interfaceName == "org.mpris.MediaPlayer2" {
		props := make(map[string]dbus.Variant)
		props["CanQuit"] = dbus.MakeVariant(true)
		props["CanRaise"] = dbus.MakeVariant(false)
		props["HasTrackList"] = dbus.MakeVariant(false)
		props["Identity"] = dbus.MakeVariant("BM")
		props["DesktopEntry"] = dbus.MakeVariant("")
		props["SupportedUriSchemes"] = dbus.MakeVariant([]string{"file"})
		props["SupportedMimeTypes"] = dbus.MakeVariant([]string{"audio/flac"})
		return props, nil
	} else if interfaceName == "org.mpris.MediaPlayer2.Player" {
		props := make(map[string]dbus.Variant)

		props["PlaybackStatus"] = dbus.MakeVariant(m.getPlaybackStatus())
		props["LoopStatus"] = dbus.MakeVariant(m.getLoopStatus())
		if m.player != nil {
			props["Rate"] = dbus.MakeVariant(m.player.resampler.Ratio())
			if m.app != nil {
				props["Volume"] = dbus.MakeVariant(m.app.linearVolume)
			} else {
				props["Volume"] = dbus.MakeVariant(1.0)
			}
			props["Position"] = dbus.MakeVariant(m.getCurrentPosition())
		} else {
			props["Rate"] = dbus.MakeVariant(1.0)
			props["Volume"] = dbus.MakeVariant(1.0)
			props["Position"] = dbus.MakeVariant(m.position)
		}

		props["Shuffle"] = dbus.MakeVariant(false)
		props["Metadata"] = dbus.MakeVariant(m.metadata)
		props["MinimumRate"] = dbus.MakeVariant(0.1)
		props["MaximumRate"] = dbus.MakeVariant(4.0)
		props["CanGoNext"] = dbus.MakeVariant(m.app != nil && len(m.app.Playlist) > 1)
		props["CanGoPrevious"] = dbus.MakeVariant(m.app != nil && len(m.app.Playlist) > 1)
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
			if m.player != nil && m.app != nil {
				linearVol := value.Value().(float64)
				m.app.linearVolume = min(max(linearVol, 0.0), 1.0)
				if m.app.linearVolume == 0 {
					m.app.volume = -10
				} else {
					m.app.volume = math.Log2(m.app.linearVolume)
				}
				m.player.volume.Volume = m.app.volume
				m.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
					"Volume": m.app.linearVolume,
				})
			}
		case "Rate":
			if m.player != nil {
				rate := value.Value().(float64)
				m.player.resampler.SetRatio(rate)
				m.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
					"Rate": rate,
				})
			}
		case "LoopStatus":
			// Not supported
		case "Shuffle":
			// Not supported
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
	if m.player.ctrl.Paused {
		return "Paused"
	}
	return "Playing"
}

// updateMetadata updates the track metadata.
//
// updateMetadata 更新曲目元数据。
func (m *MPRISServer) updateMetadata() {
	title, artist, album := getSongMetadata(m.flacPath)

	m.metadata = map[string]dbus.Variant{
		"mpris:trackid": dbus.MakeVariant(dbus.ObjectPath("/org/mpris/MediaPlayer2/TrackList/NoTrack")),
		"mpris:length":  dbus.MakeVariant(int64(m.duration)),
		"xesam:title":   dbus.MakeVariant(title),
		"xesam:artist":  dbus.MakeVariant([]string{artist}),
		"xesam:album":   dbus.MakeVariant(album),
	}

	if coverData := m.extractAlbumArt(); coverData != "" {
		m.metadata["mpris:artUrl"] = dbus.MakeVariant(coverData)
	}
}

// extractAlbumArt extracts the album art.
//
// extractAlbumArt 提取专辑封面。
func (m *MPRISServer) extractAlbumArt() string {
	f, err := os.Open(m.flacPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	metadata, err := tag.ReadFrom(f)
	if err != nil {
		return ""
	}

	if pic := metadata.Picture(); pic != nil {
		return m.encodePictureToBase64(pic)
	}

	return ""
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

	return "file://" + tempFile.Name()
}

// sendPropertiesChanged sends a PropertiesChanged signal.
//
// sendPropertiesChanged 发送 PropertiesChanged 信号。
func (m *MPRISServer) sendPropertiesChanged(interfaceName string, changedProperties map[string]any) {
	if m.conn == nil || m.stopped {
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
	if m.originalFile == nil {
		return fmt.Errorf("Original file not open\n\n原始文件未打开")
	}

	if _, err := m.originalFile.Seek(0, 0); err != nil {
		return fmt.Errorf("Failed to reset file pointer: %v\n\n重置文件指针失败: %v", err, err)
	}

	streamer, format, err := flac.Decode(m.originalFile)
	if err != nil {
		return fmt.Errorf("Failed to re-decode file: %v\n\n重新解码文件失败: %v", err, err)
	}

	totalSamples := streamer.Len()
	m.duration = int64(float64(totalSamples) / float64(format.SampleRate) * 1e6)

	return nil
}

// StartUpdateLoop starts the MPRIS update loop.
//
// StartUpdateLoop 启动 MPRIS 更新循环。
func (m *MPRISServer) StartUpdateLoop() {
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-m.stopChan:
				return
			case <-ticker.C:
				if m.stopped {
					return
				}
				if m.isPlaying && !m.startTime.IsZero() {
					m.updatePositionFromTime()
				} else if m.player != nil && m.player.streamer != nil {
					samplePos := m.player.streamer.Position()
					m.position = int64(float64(samplePos) / float64(m.player.sampleRate) * 1e6)
				}

				m.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
					"Position": m.position,
				})
			}
		}
	}()
}
