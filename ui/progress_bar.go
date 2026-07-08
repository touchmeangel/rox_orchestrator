package ui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
)

type progressUpdateMsg struct {
	label   string
	percent float64
}

type progressDoneMsg struct{}

type progressModel struct {
	bar     progress.Model
	label   string
	percent float64
	done    bool
}

func newProgressModel(label string) progressModel {
	bar := progress.New(progress.WithDefaultGradient())
	return progressModel{bar: bar, label: label}
}

func (m progressModel) Init() tea.Cmd { return nil }

func (m progressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.bar.Width = msg.Width - 4
		return m, nil
	case progressUpdateMsg:
		m.label = msg.label
		m.percent = msg.percent
		return m, nil
	case progressDoneMsg:
		m.done = true
		return m, tea.Quit
	}
	return m, nil
}

func (m progressModel) View() string {
	if m.done {
		return ""
	}
	return fmt.Sprintf("  %s %s", m.bar.ViewAs(m.percent), dimStyle.Render(m.label))
}

type PullProgress struct {
	program *tea.Program
	done    chan struct{}
}

func NewPullProgress(label string) *PullProgress {
	p := tea.NewProgram(newProgressModel(label), tea.WithoutSignalHandler())
	return &PullProgress{program: p, done: make(chan struct{})}
}

func (p *PullProgress) Start() {
	go func() {
		defer close(p.done)
		_, _ = p.program.Run()
	}()
}

func (p *PullProgress) Update(label string, percent float64) {
	p.program.Send(progressUpdateMsg{label: label, percent: percent})
}

func (p *PullProgress) Stop() {
	p.program.Send(progressDoneMsg{})
	<-p.done
}
