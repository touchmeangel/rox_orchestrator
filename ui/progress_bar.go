package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

type pullImageState int

const (
	pullPending pullImageState = iota
	pullActive
	pullDone
	pullFailed
)

type pullImage struct {
	name    string
	state   pullImageState
	percent float64
	detail  string
}

type pullUpdateMsg struct {
	index   int
	percent float64
	detail  string
}

type pullAdvanceMsg struct{ index int }

type pullFailMsg struct {
	index int
	err   error
}

type pullDoneMsg struct{}

type multiPullModel struct {
	images []pullImage
	bar    progress.Model
	spin   spinner.Model
	done   bool
}

func newMultiPullModel(names []string) multiPullModel {
	images := make([]pullImage, len(names))
	for i, n := range names {
		images[i] = pullImage{name: n, state: pullPending}
	}
	if len(images) > 0 {
		images[0].state = pullActive
	}

	bar := progress.New(
		progress.WithSolidFill("14"),
		progress.WithoutPercentage(),
		progress.WithWidth(40),
	)
	s := spinner.New()
	s.Spinner = spinner.Points
	s.Style = cyanStyle

	return multiPullModel{images: images, bar: bar, spin: s}
}

func (m multiPullModel) Init() tea.Cmd { return m.spin.Tick }

func (m multiPullModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		w := msg.Width - 8
		if w > 50 {
			w = 50
		}
		if w < 10 {
			w = 10
		}
		m.bar.Width = w
		return m, nil
	case pullUpdateMsg:
		if msg.index >= 0 && msg.index < len(m.images) {
			m.images[msg.index].percent = msg.percent
			m.images[msg.index].detail = msg.detail
		}
		return m, nil
	case pullAdvanceMsg:
		if msg.index >= 0 && msg.index < len(m.images) {
			m.images[msg.index].state = pullDone
			m.images[msg.index].percent = 1
			if msg.index+1 < len(m.images) {
				m.images[msg.index+1].state = pullActive
			}
		}
		return m, nil
	case pullFailMsg:
		if msg.index >= 0 && msg.index < len(m.images) {
			m.images[msg.index].state = pullFailed
			m.images[msg.index].detail = msg.err.Error()
		}
		return m, nil
	case pullDoneMsg:
		m.done = true
		return m, tea.Quit
	case spinner.TickMsg:
		if m.done {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m multiPullModel) View() string {
	if m.done {
		return ""
	}
	var b strings.Builder
	for _, img := range m.images {
		switch img.state {
		case pullDone:
			fmt.Fprintf(&b, "  %s %s\n", cyanStyle.Render("✔"), dimStyle.Render(img.name))
		case pullFailed:
			fmt.Fprintf(&b, "  %s %s  %s\n", redStyle.Render("✗"), img.name, dimStyle.Render(img.detail))
		case pullActive:
			fmt.Fprintf(&b, "  %s %s\n", m.spin.View(), boldStyle.Render(img.name))
			fmt.Fprintf(&b, "    %s  %s\n", m.bar.ViewAs(img.percent), dimStyle.Render(img.detail))
		default:
			fmt.Fprintf(&b, "  %s %s\n", dimStyle.Render("○"), dimStyle.Render(img.name))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

type MultiPullProgress struct {
	program *tea.Program
	done    chan struct{}
}

func NewMultiPullProgress(imageNames []string) *MultiPullProgress {
	p := tea.NewProgram(newMultiPullModel(imageNames), tea.WithoutSignalHandler())
	return &MultiPullProgress{program: p, done: make(chan struct{})}
}

func (p *MultiPullProgress) Start() {
	go func() {
		defer close(p.done)
		_, _ = p.program.Run()
	}()
}

func (p *MultiPullProgress) Update(index int, percent float64, detail string) {
	p.program.Send(pullUpdateMsg{index: index, percent: percent, detail: detail})
}

func (p *MultiPullProgress) Advance(index int) {
	p.program.Send(pullAdvanceMsg{index: index})
}

func (p *MultiPullProgress) Fail(index int, err error) {
	p.program.Send(pullFailMsg{index: index, err: err})
}

func (p *MultiPullProgress) Stop() {
	p.program.Send(pullDoneMsg{})
	<-p.done
}
