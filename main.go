package main

import (
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/effects"
	"github.com/gopxl/beep/v2/flac"
	"github.com/gopxl/beep/v2/speaker"
)

// ---------- 模型 ----------
type model struct {
	file       string
	streamer   beep.StreamSeeker
	ctrl       *beep.Ctrl
	resampler  *beep.Resampler
	volume     *effects.Volume
	sampleRate beep.SampleRate

	pos  time.Duration // 当前播放位置
	leng time.Duration // 总长度
}

// ---------- Bubble Tea 标准接口 ----------
func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.tickCmd(),    // 定时刷新
		tea.HideCursor, // 隐藏光标
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case " ":
			speaker.Lock()
			m.ctrl.Paused = !m.ctrl.Paused
			speaker.Unlock()
		case "q":
			speaker.Lock()
			newPos := max(0, m.streamer.Position()-m.sampleRate.N(time.Second))
			_ = m.streamer.Seek(newPos)
			speaker.Unlock()
		case "w":
			speaker.Lock()
			newPos := min(m.streamer.Len()-1, m.streamer.Position()+m.sampleRate.N(time.Second))
			_ = m.streamer.Seek(newPos)
			speaker.Unlock()
		case "a":
			speaker.Lock()
			m.volume.Volume -= 0.1
			speaker.Unlock()
		case "s":
			speaker.Lock()
			m.volume.Volume += 0.1
			speaker.Unlock()
		case "z":
			speaker.Lock()
			r := m.resampler.Ratio() * 15 / 16
			if r < 0.001 {
				r = 0.001
			}
			m.resampler.SetRatio(r)
			speaker.Unlock()
		case "x":
			speaker.Lock()
			r := m.resampler.Ratio() * 16 / 15
			if r > 100 {
				r = 100
			}
			m.resampler.SetRatio(r)
			speaker.Unlock()
		}

	case tickMsg:
		speaker.Lock()
		m.pos = m.sampleRate.D(m.streamer.Position())
		speaker.Unlock()
		return m, m.tickCmd()
	}

	return m, nil
}

func (m model) View() string {
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#D7D8A2")).
		Render("Speedy Player (FLAC + Bubble Tea)")

	key := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#DDC074")).
		Render("Space 暂停/继续  |  Q/W ±1s  |  A/S 音量  |  Z/X 变速  |  ESC 退出")

	status := fmt.Sprintf("%v / %v    vol=%.1f    speed=%.3fx",
		m.pos.Round(time.Second), m.leng.Round(time.Second),
		m.volume.Volume, m.resampler.Ratio())

	return lipgloss.JoinVertical(lipgloss.Left,
		title,
		key,
		"",
		status,
	)
}

// ---------- 定时刷新 ----------
type tickMsg time.Time

func (m model) tickCmd() tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// ---------- 入口 ----------
func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s song.flac\n", os.Args[0])
		os.Exit(1)
	}
	file := os.Args[1]

	// 1. 解码
	f, err := os.Open(file)
	if err != nil {
		die(err)
	}
	streamer, format, err := flac.Decode(f)
	if err != nil {
		die(err)
	}

	// 2. 播放链
	ctrl := &beep.Ctrl{Streamer: streamer}
	res := beep.ResampleRatio(4, 1, ctrl)
	vol := &effects.Volume{Streamer: res, Base: 2}

	// 3. 启动扬声器
	if err := speaker.Init(format.SampleRate, format.SampleRate.N(time.Second/30)); err != nil {
		die(err)
	}
	speaker.Play(vol)

	// 4. 初始模型
	m := model{
		file:       file,
		streamer:   streamer,
		ctrl:       ctrl,
		resampler:  res,
		volume:     vol,
		sampleRate: format.SampleRate,
		leng:       format.SampleRate.D(streamer.Len()),
	}

	// 5. 启动 Bubble Tea
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		die(err)
	}
}

func die(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
