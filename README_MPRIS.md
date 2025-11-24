# MPRIS 集成说明

## 功能概述

已为 BM 音乐播放器实现了 MPRIS (Media Player Remote Interfacing Specification) 服务端，允许其他程序通过 D-Bus 控制播放器。

## 实现的功能

### 1. 媒体信息暴露
- **歌曲元数据**: 标题、艺术家、专辑
- **播放状态**: 播放中、暂停、停止
- **播放进度**: 当前位置和总时长
- **音量控制**: 当前音量设置
- **播放速率**: 播放速度控制

### 2. 远程控制支持
- **播放/暂停**: `PlayPause()` 方法
- **播放**: `Play()` 方法  
- **暂停**: `Pause()` 方法
- **停止**: `Stop()` 方法
- **跳转**: `Seek()` 和 `SetPosition()` 方法
- **音量调节**: `SetVolume()` 方法
- **播放速率**: `SetRate()` 方法
- **进度条跳转**: 支持外部程序点击进度条直接跳转播放位置

### 3. 属性变化通知
当播放状态、位置、音量等发生变化时，会自动发送 D-Bus 信号通知其他程序。

## 服务信息

- **服务名**: `org.mpris.MediaPlayer2.bm`
- **对象路径**: `/org/mpris/MediaPlayer2`
- **接口**: 
  - `org.mpris.MediaPlayer2`
  - `org.mpris.MediaPlayer2.Player`

## 使用示例

### 查看播放状态
```bash
dbus-send --session --dest=org.mpris.MediaPlayer2.bm --type=method_call --print-reply /org/mpris/MediaPlayer2 org.freedesktop.DBus.Properties.Get string:org.mpris.MediaPlayer2.Player string:PlaybackStatus
```

### 播放/暂停切换
```bash
dbus-send --session --dest=org.mpris.MediaPlayer2.bm --type=method_call --print-reply /org/mpris/MediaPlayer2 org.mpris.MediaPlayer2.Player.PlayPause
```

### 获取歌曲信息
```bash
dbus-send --session --dest=org.mpris.MediaPlayer2.bm --type=method_call --print-reply /org/mpris/MediaPlayer2 org.freedesktop.DBus.Properties.Get string:org.mpris.MediaPlayer2.Player string:Metadata
```

### 跳转播放位置
```bash
# 相对跳转（前进5秒）
dbus-send --session --dest=org.mpris.MediaPlayer2.bm --type=method_call --print-reply /org/mpris/MediaPlayer2 org.mpris.MediaPlayer2.Player.Seek int64:5000000

# 绝对位置跳转（跳转到30秒位置）
dbus-send --session --dest=org.mpris.MediaPlayer2.bm --type=method_call --print-reply /org/mpris/MediaPlayer2 org.mpris.MediaPlayer2.Player.SetPosition objpath:"/org/mpris/MediaPlayer2/TrackList/NoTrack" int64:30000000
```

## 支持的桌面环境

- GNOME (通过 GNOME Shell 媒体控制)
- KDE Plasma (通过媒体控制器)
- XFCE (通过 xfce4-panel 插件)
- 其他支持 MPRIS 的桌面环境和应用程序

## 技术实现

- 使用 `github.com/godbus/dbus/v5` 库实现 D-Bus 通信
- 遵循 MPRIS 2.2 规范
- 自动注册服务到会话总线
- 实时更新播放状态和位置
- 支持外部程序点击进度条跳转播放位置
- 基于时间的播放位置计算，确保暂停/播放后时间同步正确

## 注意事项

1. 需要 D-Bus 会话总线正常运行
2. 服务在播放器启动时自动注册
3. 播放器退出时自动注销服务
4. 支持多个客户端同时连接和控制