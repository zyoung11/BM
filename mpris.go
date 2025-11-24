package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/dhowden/tag"
	"github.com/godbus/dbus/v5"
)

// MPRIS 服务端实现
type MPRISServer struct {
	conn      *dbus.Conn
	player    *audioPlayer
	flacPath  string
	isPlaying bool
	position  int64
	duration  int64
	metadata  map[string]dbus.Variant
}

// 创建 MPRIS 服务端
func NewMPRISServer(player *audioPlayer, flacPath string) (*MPRISServer, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("连接 D-Bus 失败: %v", err)
	}

	server := &MPRISServer{
		conn:      conn,
		player:    player,
		flacPath:  flacPath,
		isPlaying: false,
		position:  0,
		duration:  0,
		metadata:  make(map[string]dbus.Variant),
	}

	// 初始化元数据
	server.updateMetadata()

	// 计算音频时长
	if player.streamer != nil {
		server.duration = int64(player.streamer.Len())
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
	log.Println("✓ 导出 Properties 接口成功")

	// 注册媒体播放器2接口
	err = m.conn.Export(m, "/org/mpris/MediaPlayer2", "org.mpris.MediaPlayer2")
	if err != nil {
		return fmt.Errorf("导出 MediaPlayer2 接口失败: %v", err)
	}
	log.Println("✓ 导出 MediaPlayer2 接口成功")

	// 注册媒体播放器2.Player接口
	err = m.conn.Export(m, "/org/mpris/MediaPlayer2", "org.mpris.MediaPlayer2.Player")
	if err != nil {
		return fmt.Errorf("导出 Player 接口失败: %v", err)
	}
	log.Println("✓ 导出 Player 接口成功")

	// 注册服务名
	reply, err := m.conn.RequestName("org.mpris.MediaPlayer2.bm", dbus.NameFlagDoNotQueue)
	if err != nil {
		return fmt.Errorf("请求服务名失败: %v", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return fmt.Errorf("服务名已被占用")
	}

	log.Println("✓ MPRIS 服务已启动: org.mpris.MediaPlayer2.bm")
	return nil
}

// 停止 MPRIS 服务
func (m *MPRISServer) StopService() {
	if m.conn != nil {
		m.conn.ReleaseName("org.mpris.MediaPlayer2.bm")
		m.conn.Close()
	}
}

// 更新播放状态
func (m *MPRISServer) UpdatePlaybackStatus(playing bool) {
	m.isPlaying = playing
	m.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
		"PlaybackStatus": m.getPlaybackStatus(),
	})
}

// 更新播放位置
func (m *MPRISServer) UpdatePosition(pos int64) {
	m.position = pos
	m.sendPropertiesChanged("org.mpris.MediaPlayer2.Player", map[string]any{
		"Position": m.position,
	})
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

// 跳转到指定位置
func (m *MPRISServer) Seek(offset int64) *dbus.Error {
	if m.player != nil && m.player.streamer != nil {
		currentPos := int64(m.player.streamer.Position())
		newPos := currentPos + offset
		if newPos < 0 {
			newPos = 0
		}
		if newPos >= int64(m.player.streamer.Len()) {
			newPos = int64(m.player.streamer.Len()) - 1
		}
		if err := m.player.streamer.Seek(int(newPos)); err != nil {
			return dbus.MakeFailedError(fmt.Errorf("跳转失败: %v", err))
		}
		m.UpdatePosition(newPos)
	}
	return nil
}

// 设置位置
func (m *MPRISServer) SetPosition(trackID dbus.ObjectPath, position int64) *dbus.Error {
	if m.player != nil && m.player.streamer != nil {
		if position < 0 {
			position = 0
		}
		if position >= int64(m.player.streamer.Len()) {
			position = int64(m.player.streamer.Len()) - 1
		}
		if err := m.player.streamer.Seek(int(position)); err != nil {
			return dbus.MakeFailedError(fmt.Errorf("设置位置失败: %v", err))
		}
		m.UpdatePosition(position)
	}
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
			if m.player != nil && m.player.streamer != nil {
				return dbus.MakeVariant(int64(m.player.streamer.Position())), nil
			}
			return dbus.MakeVariant(m.position), nil
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
	if interfaceName == "org.mpris.MediaPlayer2.Player" {
		props := make(map[string]dbus.Variant)

		// 播放状态
		props["PlaybackStatus"] = dbus.MakeVariant(m.getPlaybackStatus())

		// 其他属性
		props["LoopStatus"] = dbus.MakeVariant("None")
		if m.player != nil {
			props["Rate"] = dbus.MakeVariant(m.player.resampler.Ratio())
			volume := float64(m.player.volume.Volume+5) / 5.0
			props["Volume"] = dbus.MakeVariant(volume)
			if m.player.streamer != nil {
				props["Position"] = dbus.MakeVariant(int64(m.player.streamer.Position()))
			} else {
				props["Position"] = dbus.MakeVariant(m.position)
			}
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

	// 添加专辑封面
	if coverData := m.extractAlbumArt(); coverData != "" {
		m.metadata["mpris:artUrl"] = dbus.MakeVariant(fmt.Sprintf("data:image/jpeg;base64,%s", coverData))
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
		// 这里应该将图片数据转换为 base64
		// 简化实现，返回空字符串
		return ""
	}

	return ""
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
		log.Printf("发送属性变化信号失败: %v", err)
	}
}

// 启动 MPRIS 更新循环
func (m *MPRISServer) StartUpdateLoop() {
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for range ticker.C {
			if m.player != nil && m.player.streamer != nil {
				currentPos := int64(m.player.streamer.Position())
				if currentPos != m.position {
					m.UpdatePosition(currentPos)
				}
			}
		}
	}()
}
