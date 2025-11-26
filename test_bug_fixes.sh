#!/bin/bash

echo "=== BM Bug修复测试 ==="
echo

echo "1. 测试编译:"
if go build -o bm .; then
    echo "✓ 编译成功"
else
    echo "✗ 编译失败"
    exit 1
fi
echo

echo "2. 测试启动参数验证:"
echo "   无参数:"
./bm 2>&1 | head -1
echo "   文件参数:"
touch test.txt
./bm test.txt 2>&1 | head -1
rm test.txt
echo

echo "3. 检查FLAC文件:"
flac_count=$(find . -name "*.flac" | wc -l)
echo "   找到FLAC文件数量: $flac_count"
echo

echo "=== 测试完成 ==="
echo
echo "手动测试步骤:"
echo "1. 运行: ./bm Nujabes"
echo "2. 按 '1' 键切换到Player页面（应该显示空状态提示）"
echo "3. 按 '3' 键回到Library页面"
echo "4. 使用空格键选择几首歌曲"
echo "5. 按 '2' 键切换到PlayList页面"
echo "6. 使用方向键选择歌曲，按Enter播放（应该完整跳转到Player页面）"
echo "7. 检查播放页面是否正确显示专辑封面和进度条"