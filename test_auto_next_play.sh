#!/bin/bash

echo "=== 删除歌曲自动播放下一首功能测试 ==="
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
echo "• 在PlayList页面删除正在播放的歌曲时自动播放下一首"
echo "• 如果删除后PlayList为空，停止播放并显示空状态"
echo "• 智能选择下一首：优先当前位置，超出则选择最后一首"
echo

echo "=== 测试步骤 ==="
echo "1. 运行: ./bm Nujabes"
echo "2. 在Library页面选择多首歌曲（至少3首）到PlayList"
echo "3. 按 '2' 键切换到PlayList页面"
echo "4. 按Enter播放第一首歌（跳转到Player页面）"
echo "5. 按 '2' 键回到PlayList页面"
echo "6. 删除正在播放的歌曲（按空格键）"
echo "7. 验证: 自动播放下一首歌，不跳转页面"
echo "8. 继续删除歌曲直到PlayList为空"
echo "9. 验证: 最后删除后停止播放，显示空状态"
echo "10. 按 '1' 键查看Player页面"
echo "11. 验证: 显示'PlayList is empty'提示"
echo

echo "=== 预期结果 ==="
echo "✓ 删除播放中歌曲: 自动播放下一首"
echo "✓ 智能选歌: 优先当前位置，超出选最后一首"
echo "✓ PlayList为空: 停止播放，显示空状态"
echo "✓ Player页面: 正确显示空状态提示"
echo "✓ 不跳转页面: 自动播放时保持在PlayList页面"
echo "✓ Library同步: 删除时同步取消选择状态"