#!/bin/bash

# 测试 MPRIS 服务

# 检查服务是否注册
echo "=== 检查 MPRIS 服务 ==="
dbus-send --session --dest=org.freedesktop.DBus --type=method_call --print-reply /org/freedesktop/DBus org.freedesktop.DBus.ListNames | grep mpris

echo ""
echo "=== 检查播放器属性 ==="
# 检查基本属性
dbus-send --session --dest=org.mpris.MediaPlayer2.bm --type=method_call --print-reply /org/mpris/MediaPlayer2 org.freedesktop.DBus.Properties.Get string:org.mpris.MediaPlayer2 string:Identity

echo ""
echo "=== 检查播放器状态 ==="
# 检查播放状态
dbus-send --session --dest=org.mpris.MediaPlayer2.bm --type=method_call --print-reply /org/mpris/MediaPlayer2 org.freedesktop.DBus.Properties.Get string:org.mpris.MediaPlayer2.Player string:PlaybackStatus

echo ""
echo "=== 检查元数据 ==="
# 检查元数据
dbus-send --session --dest=org.mpris.MediaPlayer2.bm --type=method_call --print-reply /org/mpris/MediaPlayer2 org.freedesktop.DBus.Properties.Get string:org.mpris.MediaPlayer2.Player string:Metadata

echo ""
echo "=== 检查时长 ==="
# 检查时长
dbus-send --session --dest=org.mpris.MediaPlayer2.bm --type=method_call --print-reply /org/mpris/MediaPlayer2 org.freedesktop.DBus.Properties.Get string:org.mpris.MediaPlayer2.Player string:Metadata | grep -A5 "mpris:length"