package ui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

type spinnerModel struct {
	spinner spinner.Model
	label   string
	stopped bool
}

type (
	setLabelMsg string
	stopMsg     struct{}
)

func newSpinnerModel(label string) spinnerModel {
	s := spinner.New()
	s.Spinner = spinner.Points
	s.Style = cyanStyle
	return spinnerModel{spinner: s, label: label}
}

func (m spinnerModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m spinnerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case setLabelMsg:
		m.label = string(msg)
		return m, nil
	case stopMsg:
		m.stopped = true
		return m, tea.Quit
	case spinner.TickMsg:
		if m.stopped {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m spinnerModel) View() string {
	if m.stopped {
		return ""
	}
	return fmt.Sprintf("  %s %s", m.spinner.View(), dimStyle.Render(m.label))
}

type Spinner struct {
	program *tea.Program
	done    chan struct{}
}

func NewSpinner(label string) *Spinner {
	p := tea.NewProgram(newSpinnerModel(label), tea.WithoutSignalHandler())
	return &Spinner{program: p, done: make(chan struct{})}
}

func (s *Spinner) Start() {
	go func() {
		defer close(s.done)
		_, _ = s.program.Run()
	}()
}

func (s *Spinner) SetLabel(label string) {
	s.program.Send(setLabelMsg(label))
}

func (s *Spinner) Stop() {
	s.program.Send(stopMsg{})
	<-s.done
}
