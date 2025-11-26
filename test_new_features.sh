#!/bin/bash

echo "=== BM 音乐播放器测试 ==="
echo

echo "1. 测试启动参数验证（无参数）:"
./bm 2>&1 | head -1
echo

echo "2. 测试启动参数验证（文件而非目录）:"
touch test_file.txt
./bm test_file.txt 2>&1 | head -1
rm test_file.txt
echo

echo "3. 测试编译成功:"
if [ -f "./bm" ]; then
    echo "✓ 编译成功"
else
    echo "✗ 编译失败"
fi
echo

echo "4. 检查FLAC文件:"
find . -name "*.flac" | wc -l | xargs echo "找到FLAC文件数量:"
echo

echo "=== 测试完成 ==="
echo "现在可以运行: ./bm Nujabes"
echo "然后使用Tab键切换页面，在PlayList中按Enter播放歌曲"