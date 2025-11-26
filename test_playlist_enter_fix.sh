#!/bin/bash

echo "=== PlayList Enter播放修复测试 ==="
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

echo "=== 修复说明 ==="
echo "问题: PlayList页面按Enter播放歌曲时，PlayList的UI元素仍然显示"
echo "原因: PlayList.HandleKey在所有按键后都会调用View()，覆盖了Player页面"
echo "修复: 为Enter键添加needRedraw=false标志，避免重复绘制PlayList"
echo

echo "=== 测试步骤 ==="
echo "1. 运行: ./bm Nujabes"
echo "2. 按 '3' 键确保在Library页面"
echo "3. 使用空格键选择几首歌曲到PlayList"
echo "4. 按 '2' 键切换到PlayList页面"
echo "5. 使用方向键选择歌曲"
echo "6. 按 Enter 键播放"
echo "7. 验证: 应该完全切换到Player页面，无PlayList残留"
echo "8. 测试其他按键: 方向键、空格键等应该正常工作"
echo

echo "=== 预期结果 ==="
echo "✓ Enter播放: 完整切换到Player页面"
echo "✓ 专辑封面: 正确显示"
echo "✓ 歌曲信息: 标题、艺术家、专辑正确显示"
echo "✓ 进度条: 正常显示和更新"
echo "✓ 其他按键: 仍然正常工作"