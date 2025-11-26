#!/bin/bash

echo "=== 自动播放第一首歌功能测试 ==="
echo

echo "1. 测试编译:"
if go build -o bm .; then
    echo "✓ 编译成功"
else
    echo "✗ 编译失败"
    exit 1
fi
echo

echo "2. 检查FLAC文件:"
flac_count=$(find . -name "*.flac" | wc -l)
echo "   找到FLAC文件数量: $flac_count"
echo

echo "=== 功能说明 ==="
echo "新增功能:"
echo "• 当PlayList为空时，添加第一首歌会自动播放"
echo "• 自动播放时不跳转到Player页面，保持在当前页面"
echo "• 用户在PlayList页面按Enter播放时会跳转到Player页面"
echo

echo "=== 测试步骤 ==="
echo "1. 运行: ./bm Nujabes"
echo "2. 确保在Library页面（默认启动页面）"
echo "3. 选择一首歌曲（按空格键）"
echo "4. 验证: 歌曲开始播放，但仍在Library页面"
echo "5. 按 '2' 键切换到PlayList页面"
echo "6. 选择另一首歌曲（按方向键导航，按Enter）"
echo "7. 验证: 跳转到Player页面并播放新歌曲"
echo "8. 按 '3' 键回到Library页面"
echo "9. 选择更多歌曲（按空格键）"
echo "10. 验证: 不会自动播放新歌曲（因为已有歌曲在播放）"
echo

echo "=== 预期结果 ==="
echo "✓ 第一首歌: 自动播放，不跳转页面"
echo "✓ Enter播放: 跳转到Player页面"
echo "✓ 后续歌曲: 不自动播放，避免打断"
echo "✓ 页面切换: 正常工作"
echo "✓ 播放状态: 正确显示和更新"