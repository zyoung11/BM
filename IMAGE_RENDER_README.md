# 终端图像渲染器升级说明

## 概述

已成功将音乐播放器的专辑封面渲染系统升级为支持多种终端协议的统一渲染器。现在支持以下协议：

1. **Kitty协议** - 现代终端（Kitty、WezTerm、Ghostty等）
2. **Sixel协议** - 传统终端（保持向后兼容）
3. **iTerm2协议** - macOS iTerm2终端
4. **Halfblocks协议** - Unicode半块字符（通用回退）

## 主要更改

### 1. 新增文件
- `term_renderer.go` - 统一的终端图像渲染器

### 2. 修改文件
- `player.go` - 替换了第935行的sixel渲染调用
- `config.go` - 添加了`ImageProtocol`配置字段
- `default_config.toml` - 添加了`image_protocol`配置选项

### 3. 功能特性

#### 自动协议检测
渲染器会自动检测终端支持的协议，优先级如下：
1. Kitty协议（如果检测到Kitty、WezTerm、Ghostty等）
2. iTerm2协议（如果检测到iTerm2）
3. Sixel协议（传统支持）
4. Halfblocks协议（通用回退）

#### 配置支持
用户可以在配置文件中指定使用的协议：
```toml
# 图像渲染协议 - 决定使用哪种终端协议来显示专辑封面。
# 可用选项: "auto", "kitty", "sixel", "iterm2", "halfblocks"
# "auto" 会自动检测最佳可用协议。
image_protocol = "auto"
```

#### 向后兼容
- 如果新渲染器失败，会自动回退到原来的sixel渲染器
- 默认协议为Sixel，确保现有功能不受影响

## 协议性能对比

根据go-termimg库的基准测试：
- **Halfblocks**: ~800µs (最快，通用支持)
- **Kitty**: ~2.5ms (高效，现代终端)
- **iTerm2**: ~2.5ms (快速，macOS)
- **Sixel**: ~90ms (高质量，较慢)

**Kitty协议比Sixel快约36倍！**

## 环境变量检测

渲染器会检查以下环境变量来确定终端能力：

### Kitty协议检测
- `KITTY_WINDOW_ID` - Kitty终端窗口ID
- `TERM` - 包含"kitty"
- `TERM_PROGRAM` - "WezTerm"、"ghostty"、"rio"
- `WEZTERM_EXECUTABLE` - WezTerm可执行文件路径

### iTerm2协议检测
- `TERM_PROGRAM` - "iTerm.app"

### Sixel协议检测
- `TERM` - 包含"sixel"或"mlterm"

### 真彩色支持检测
- `COLORTERM` - "truecolor"或"24bit"
- 终端类型检测

## 使用示例

### 1. 强制使用Kitty协议
在配置文件中设置：
```toml
image_protocol = "kitty"
```

### 2. 强制使用Sixel协议
```toml
image_protocol = "sixel"
```

### 3. 自动检测（默认）
```toml
image_protocol = "auto"
```

## 测试方法

### 检查当前终端支持的协议
```bash
# 设置环境变量模拟不同终端
export TERM_PROGRAM=WezTerm
# 然后运行播放器
```

### 验证渲染功能
1. 播放带有封面的音乐文件
2. 观察专辑封面的显示效果
3. 检查控制台是否有错误输出

## 故障排除

### 问题1: 图像不显示
- 检查终端是否支持所选协议
- 尝试切换到"auto"或"sixel"
- 查看控制台错误信息

### 问题2: 图像显示异常
- 可能是协议实现问题
- 尝试不同的协议
- 检查图像尺寸是否合适

### 问题3: 性能问题
- Kitty协议应该最快
- Sixel协议可能较慢但兼容性好
- 调整图像尺寸减少数据量

## 高级功能

### Kitty协议特性
- 支持图像压缩（zlib）
- 支持分块传输（避免缓冲区溢出）
- 支持tmux穿透
- 支持清除图像命令

### 字体大小检测
渲染器会自动检测终端字体大小，用于精确的像素到字符转换。

### 真彩色支持
检测终端是否支持24位真彩色，优化颜色显示。

## 代码集成

### 主要函数
```go
// 检测终端支持的协议
func DetectTerminalProtocol() Protocol

// 渲染图像到终端
func RenderImage(img image.Image, widthChars, heightChars int) error

// 清除Kitty图像
func ClearKittyImages() error

// 获取终端字体大小
func GetTerminalFontSize() (width, height int, err error)

// 检查终端是否支持真彩色
func SupportsTrueColor() bool
```

### 在player.go中的使用
```go
// 替换原来的sixel渲染
if err := RenderImage(scaledImg, imageWidthInChars, imageHeightInChars); err != nil {
    // 如果新渲染器失败，回退到原来的sixel渲染器
    _ = NewEncoder(os.Stdout).Encode(scaledImg)
}
```

## 总结

这次升级为音乐播放器带来了：
1. **更好的性能** - Kitty协议比Sixel快36倍
2. **更广的兼容性** - 支持多种终端协议
3. **用户可配置** - 允许用户选择喜欢的协议
4. **向后兼容** - 确保现有功能不受影响
5. **现代化** - 支持现代终端特性

现在你的播放器可以在Kitty、WezTerm等现代终端中获得更好的专辑封面显示体验！