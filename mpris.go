package main

import (
	"fmt"
	"os"
	"time"

	"github.com/dhowden/tag"
	"github.com/godbus/dbus/v5"
	"github.com/gopxl/beep/v2/flac"
	"github.com/gopxl/beep/v2/speaker"
)

// MPRIS 服务端实现
type MPRISServer struct {
	conn         *dbus.Conn
	player       *audioPlayer
	flacPath     string
	isPlaying    bool
	position     int64
	duration     int64
	metadata     map[string]dbus.Variant
	originalFile *os.File  // 保持对原始文件的引用用于时长计算
	lastUpdate   time.Time // 上次更新时间
	startTime    time.Time // 播放开始时间
}

// 创建 MPRIS 服务端
func NewMPRISServer(player *audioPlayer, flacPath string) (*MPRISServer, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("连接 D-Bus 失败: %v", err)
	}

	// 重新打开文件用于时长计算
	f, err := os.Open(flacPath)
	if err != nil {
		return nil, fmt.Errorf("打开文件失败: %v", err)
	}

	server := &MPRISServer{
		conn:         conn,
		player:       player,
		flacPath:     flacPath,
		isPlaying:    false,
		position:     0,
		duration:     0,
		metadata:     make(map[string]dbus.Variant),
		originalFile: f,
		lastUpdate:   time.Now(),
		startTime:    time.Time{},
	}

	// 初始化元数据
	server.updateMetadata()

	// 计算音频时长（以微秒为单位）
	if err := server.calculateDuration(); err != nil {
		// log.Printf("计算音频时长失败: %v", err)
	}

	return server, nil
}

// 启动 MPRIS 服务
func (m *MPRISServer) Start() error {
	// 注册 D-Bus Properties 接口
	err := m.conn.Export(m, "/org/mpris/MediaPlayer2", "org.freedesktop.DBus.Properties")
	if err != nil {
		return fmt.Errorf("导出 Properties 接口失败: %v", err)
	}
	// log.Println("✓ 导出 Properties 接口成功")

	// 注册媒体播放器2接口
	err = m.conn.Export(m, "/org/mpris/MediaPlayer2", "org.mpris.MediaPlayer2")
	if err != nil {
		return fmt.Errorf("导出 MediaPlayer2 接口失败: %v", err)
	}
	// log.Println("✓ 导出 MediaPlayer2 接口成功")

	// 注册媒体播放器2.Player接口
	err = m.conn.Export(m, "/org/mpris/MediaPlayer2", "org.mpris.MediaPlayer2.Player")
	if err != nil {
		return fmt.Errorf("导出 Player 接口失败: %v", err)
	}
	// log.Println("✓ 导出 Player 接口成功")

	// 注册服务名
	reply, err := m.conn.RequestName("org.mpris.MediaPlayer2.bm", dbus.NameFlagDoNotQueue)
	if err != nil {
		return fmt.Errorf("请求服务名失败: %v", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return fmt.Errorf("服务名已被占用")
	}

	// log.Println("✓ MPRIS 服务已启动: org.mpris.MediaPlayer2.bm")
	return nil
}

// 停止 MPRIS 服务
func (m *MPRISServer) StopService() {
	if m.conn != nil {
		m.conn.ReleaseName("org.mpris.MediaPlayer2.bm")
		m.conn.Close()
	}
	if m.originalFile != nil {
		m.originalFile.Close()
	}
}

// 更新播放状态
func (m *MPRISServer) UpdatePlaybackStatus(playing bool) {
	if playing && !m.isPlaying {
		// 开始播放，记录开始时间
		m.startTime = time.Now().Add(-time.Duration(m.position) * time.Microsecond)
	} else if !playing && m.isPlaying {
		// 暂停播放，更新当前位置
		m.updatePositionFromTime()
	}
	m.isPlaying = playing
	m.lastUpdate = time.Now()
	m.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
		"PlaybackStatus": m.getPlaybackStatus(),
	})
}

// 更新播放位置
func (m *MPRISServer) UpdatePosition(pos int64) {
	m.position = pos
	m.lastUpdate = time.Now()
	m.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
		"Position": m.position,
	})
}

// 根据时间更新位置
func (m *MPRISServer) updatePositionFromTime() {
	if !m.isPlaying || m.startTime.IsZero() {
		return
	}

	elapsed := time.Since(m.startTime)
	newPosition := int64(elapsed.Microseconds())

	// 如果超过总时长，循环
	if m.duration > 0 && newPosition >= m.duration {
		newPosition = newPosition % m.duration
		m.startTime = time.Now().Add(-time.Duration(newPosition) * time.Microsecond)
	}

	if newPosition != m.position {
		m.position = newPosition
		m.lastUpdate = time.Now()
	}
}

// 获取当前位置（以微秒为单位）
func (m *MPRISServer) getCurrentPosition() int64 {
	// 如果正在播放，根据时间计算当前位置
	if m.isPlaying && !m.startTime.IsZero() {
		m.updatePositionFromTime()
	}
	return m.position
}

// getLoopStatus 获取循环状态
func (m *MPRISServer) getLoopStatus() string {
	// 这里需要访问App的playMode字段，但由于循环导入问题，暂时返回默认值
	// 在实际使用中，应该通过某种方式获取当前的播放模式
	return "None"
}

// 更新元数据
func (m *MPRISServer) UpdateMetadata() {
	m.updateMetadata()
	m.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
		"Metadata": m.metadata,
	})
}

// --- org.mpris.MediaPlayer2 接口实现 ---

// 退出播放器
func (m *MPRISServer) Quit() *dbus.Error {
	os.Exit(0)
	return nil
}

// 提升播放器窗口
func (m *MPRISServer) Raise() *dbus.Error {
	return nil
}

// 检查是否支持全屏
func (m *MPRISServer) CanQuit() (bool, *dbus.Error) {
	return true, nil
}

// 检查是否支持全屏
func (m *MPRISServer) CanRaise() (bool, *dbus.Error) {
	return false, nil
}

// 检查是否支持全屏
func (m *MPRISServer) HasTrackList() (bool, *dbus.Error) {
	return false, nil
}

// 获取程序标识符
func (m *MPRISServer) Identity() (string, *dbus.Error) {
	return "BM", nil
}

// 获取桌面入口
func (m *MPRISServer) DesktopEntry() (string, *dbus.Error) {
	return "", nil
}

// 获取支持的URI协议
func (m *MPRISServer) SupportedUriSchemes() ([]string, *dbus.Error) {
	return []string{"file"}, nil
}

// 获取支持的MIME类型
func (m *MPRISServer) SupportedMimeTypes() ([]string, *dbus.Error) {
	return []string{"audio/flac"}, nil
}

// --- org.mpris.MediaPlayer2.Player 接口实现 ---

// 下一首
func (m *MPRISServer) Next() *dbus.Error {
	return nil
}

// 上一首
func (m *MPRISServer) Previous() *dbus.Error {
	return nil
}

// 暂停
func (m *MPRISServer) Pause() *dbus.Error {
	if m.player != nil && !m.player.ctrl.Paused {
		m.player.ctrl.Paused = true
		m.UpdatePlaybackStatus(false)
	}
	return nil
}

// 播放/暂停
func (m *MPRISServer) PlayPause() *dbus.Error {
	if m.player != nil {
		m.player.ctrl.Paused = !m.player.ctrl.Paused
		m.UpdatePlaybackStatus(!m.player.ctrl.Paused)
	}
	return nil
}

// 停止
func (m *MPRISServer) Stop() *dbus.Error {
	if m.player != nil {
		m.player.ctrl.Paused = true
		m.UpdatePlaybackStatus(false)
	}
	return nil
}

// 播放
func (m *MPRISServer) Play() *dbus.Error {
	if m.player != nil && m.player.ctrl.Paused {
		m.player.ctrl.Paused = false
		m.UpdatePlaybackStatus(true)
	}
	return nil
}

// 跳转到指定位置（offset 以微秒为单位）
func (m *MPRISServer) Seek(offset int64) *dbus.Error {
	currentPos := m.getCurrentPosition()
	newPos := currentPos + offset
	if newPos < 0 {
		newPos = 0
	}
	if newPos >= m.duration {
		newPos = m.duration - 1
	}

	// 更新位置和开始时间
	m.position = newPos
	if m.isPlaying {
		m.startTime = time.Now().Add(-time.Duration(newPos) * time.Microsecond)
	}
	m.lastUpdate = time.Now()

	// 同步到实际音频播放器
	if m.player != nil && m.player.streamer != nil {
		// 将微秒转换为样本数
		samplePos := int(float64(newPos) / 1e6 * float64(m.player.sampleRate))
		speaker.Lock()
		if err := m.player.streamer.Seek(samplePos); err != nil {
			// 跳转失败，忽略错误
		}
		speaker.Unlock()
	}

	m.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
		"Position": m.position,
	})
	return nil
}

// 设置位置（position 以微秒为单位）
func (m *MPRISServer) SetPosition(trackID dbus.ObjectPath, position int64) *dbus.Error {
	if position < 0 {
		position = 0
	}
	if position >= m.duration {
		position = m.duration - 1
	}

	// 更新位置和开始时间
	m.position = position
	if m.isPlaying {
		m.startTime = time.Now().Add(-time.Duration(position) * time.Microsecond)
	}
	m.lastUpdate = time.Now()

	// 同步到实际音频播放器
	if m.player != nil && m.player.streamer != nil {
		// 将微秒转换为样本数
		samplePos := int(float64(position) / 1e6 * float64(m.player.sampleRate))
		speaker.Lock()
		if err := m.player.streamer.Seek(samplePos); err != nil {
			// 跳转失败，忽略错误
		}
		speaker.Unlock()
	}

	m.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
		"Position": m.position,
	})
	return nil
}

// 打开 URI
func (m *MPRISServer) OpenUri(uri string) *dbus.Error {
	return dbus.MakeFailedError(fmt.Errorf("不支持打开 URI"))
}

// --- D-Bus Properties 接口实现 ---

// Get 方法实现 D-Bus Properties.Get
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
			if m.player != nil {
				volume := float64(m.player.volume.Volume+5) / 5.0 // 转换为 0.0-1.0 范围
				return dbus.MakeVariant(volume), nil
			}
			return dbus.MakeVariant(1.0), nil
		case "Position":
			return dbus.MakeVariant(m.getCurrentPosition()), nil
		case "MinimumRate":
			return dbus.MakeVariant(0.1), nil
		case "MaximumRate":
			return dbus.MakeVariant(4.0), nil
		case "CanGoNext":
			return dbus.MakeVariant(false), nil
		case "CanGoPrevious":
			return dbus.MakeVariant(false), nil
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
	return dbus.Variant{}, dbus.MakeFailedError(fmt.Errorf("未知属性: %s.%s", interfaceName, propertyName))
}

// GetAll 方法实现 D-Bus Properties.GetAll
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

		// 播放状态
		props["PlaybackStatus"] = dbus.MakeVariant(m.getPlaybackStatus())

		// 其他属性
		props["LoopStatus"] = dbus.MakeVariant(m.getLoopStatus())
		if m.player != nil {
			props["Rate"] = dbus.MakeVariant(m.player.resampler.Ratio())
			volume := float64(m.player.volume.Volume+5) / 5.0
			props["Volume"] = dbus.MakeVariant(volume)
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
		props["CanGoNext"] = dbus.MakeVariant(false)
		props["CanGoPrevious"] = dbus.MakeVariant(false)
		props["CanPlay"] = dbus.MakeVariant(true)
		props["CanPause"] = dbus.MakeVariant(true)
		props["CanSeek"] = dbus.MakeVariant(true)
		props["CanControl"] = dbus.MakeVariant(true)

		return props, nil
	}
	return nil, dbus.MakeFailedError(fmt.Errorf("未知接口: %s", interfaceName))
}

// Set 方法实现 D-Bus Properties.Set
func (m *MPRISServer) Set(interfaceName, propertyName string, value dbus.Variant) *dbus.Error {
	switch interfaceName {
	case "org.mpris.MediaPlayer2.Player":
		switch propertyName {
		case "Volume":
			if m.player != nil {
				volume := value.Value().(float64)
				m.player.volume.Volume = volume*5.0 - 5.0 // 转换为 -5.0-0.0 范围
				m.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
					"Volume": volume,
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
			// 忽略，不支持循环
		case "Shuffle":
			// 忽略，不支持随机播放
		default:
			return dbus.MakeFailedError(fmt.Errorf("属性 %s 不可写", propertyName))
		}
		return nil
	}
	return dbus.MakeFailedError(fmt.Errorf("未知接口: %s", interfaceName))
}

// --- 辅助方法 ---

// 获取播放状态
func (m *MPRISServer) getPlaybackStatus() string {
	if m.player == nil {
		return "Stopped"
	}
	if m.player.ctrl.Paused {
		return "Paused"
	}
	return "Playing"
}

// 更新元数据
func (m *MPRISServer) updateMetadata() {
	title, artist, album := getSongMetadata(m.flacPath)

	// 构建元数据
	m.metadata = map[string]dbus.Variant{
		"mpris:trackid": dbus.MakeVariant(dbus.ObjectPath("/org/mpris/MediaPlayer2/TrackList/NoTrack")),
		"mpris:length":  dbus.MakeVariant(int64(m.duration)),
		"xesam:title":   dbus.MakeVariant(title),
		"xesam:artist":  dbus.MakeVariant([]string{artist}),
		"xesam:album":   dbus.MakeVariant(album),
	}
	// log.Printf("元数据构建完成: 时长=%d微秒, 标题=%s, 艺术家=%s, 专辑=%s", m.duration, title, artist, album)

	// 添加专辑封面
	if coverData := m.extractAlbumArt(); coverData != "" {
		// MPRIS 规范要求使用 file:// 或 http:// URL
		// 这里我们使用 file:// URL 格式
		m.metadata["mpris:artUrl"] = dbus.MakeVariant(coverData)
	}
}

// 提取专辑封面
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
		// 将图片数据转换为 base64
		return m.encodePictureToBase64(pic)
	}

	return ""
}

// 将图片数据保存为临时文件并返回文件URL
func (m *MPRISServer) encodePictureToBase64(pic *tag.Picture) string {
	// 根据图片类型设置文件扩展名
	var fileExt string
	switch pic.MIMEType {
	case "image/jpeg", "image/jpg":
		fileExt = "jpg"
	case "image/png":
		fileExt = "png"
	default:
		// 根据图片数据自动检测类型
		if len(pic.Data) > 8 && pic.Data[0] == 0x89 && pic.Data[1] == 0x50 && pic.Data[2] == 0x4E && pic.Data[3] == 0x47 {
			fileExt = "png"
		} else if len(pic.Data) > 2 && pic.Data[0] == 0xFF && pic.Data[1] == 0xD8 {
			fileExt = "jpg"
		} else {
			// 默认使用 jpg
			fileExt = "jpg"
		}
	}

	// 创建临时文件
	tempFile, err := os.CreateTemp("", "bm_cover_*."+fileExt)
	if err != nil {
		return ""
	}
	defer tempFile.Close()

	// 写入图片数据
	if _, err := tempFile.Write(pic.Data); err != nil {
		return ""
	}

	// 返回 file:// URL
	return "file://" + tempFile.Name()
}

// 发送属性变化信号
func (m *MPRISServer) sendPropertiesChanged(interfaceName string, changedProperties map[string]any) {
	if m.conn == nil {
		return
	}

	err := m.conn.Emit(
		dbus.ObjectPath("/org/mpris/MediaPlayer2"),
		"org.freedesktop.DBus.Properties.PropertiesChanged",
		interfaceName,
		changedProperties,
		[]string{},
	)
	if err != nil {
		// log.Printf("发送属性变化信号失败: %v", err)
	}
}

// 计算音频时长
func (m *MPRISServer) calculateDuration() error {
	if m.originalFile == nil {
		return fmt.Errorf("原始文件未打开")
	}

	// 重新解码文件以获取准确的时长
	if _, err := m.originalFile.Seek(0, 0); err != nil {
		return fmt.Errorf("重置文件指针失败: %v", err)
	}

	streamer, format, err := flac.Decode(m.originalFile)
	if err != nil {
		return fmt.Errorf("重新解码文件失败: %v", err)
	}

	// streamer.Len() 返回的是样本数，需要转换为微秒
	totalSamples := streamer.Len()
	// 转换为微秒：样本数 / 采样率 * 1,000,000
	m.duration = int64(float64(totalSamples) / float64(format.SampleRate) * 1e6)
	// log.Printf("音频时长计算: 样本数=%d, 采样率=%d, 时长=%d微秒", totalSamples, format.SampleRate, m.duration)

	return nil
}

// 启动 MPRIS 更新循环
func (m *MPRISServer) StartUpdateLoop() {
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for range ticker.C {
			// 如果正在播放，使用基于时间的计算
			if m.isPlaying && !m.startTime.IsZero() {
				m.updatePositionFromTime()
			} else if m.player != nil && m.player.streamer != nil {
				// 如果暂停，使用播放器的实际位置
				samplePos := m.player.streamer.Position()
				m.position = int64(float64(samplePos) / float64(m.player.sampleRate) * 1e6)
			}

			m.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
				"Position": m.position,
			})
		}
	}()
}
