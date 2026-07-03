package ui

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

var ErrAborted = fmt.Errorf("cancelled by user")

type selectItem string

func (i selectItem) FilterValue() string { return "" }

type selectDelegate struct{}

func (d selectDelegate) Height() int                         { return 1 }
func (d selectDelegate) Spacing() int                        { return 0 }
func (d selectDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }
func (d selectDelegate) Render(w io.Writer, m list.Model, index int, li list.Item) {
	item, ok := li.(selectItem)
	if !ok {
		return
	}
	if index == m.Index() {
		fmt.Fprint(w, cyanStyle.Render("› "+string(item)))
	} else {
		fmt.Fprint(w, dimStyle.Render("  "+string(item)))
	}
}

type selectModel struct {
	list     list.Model
	label    string
	choice   int
	err      error
	quitting bool
}

func (m selectModel) Init() tea.Cmd { return nil }

func (m selectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.list.SetWidth(msg.Width)
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.err = ErrAborted
			m.quitting = true
			return m, tea.Quit
		case "enter":
			m.choice = m.list.Index()
			m.quitting = true
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m selectModel) View() string {
	if m.quitting {
		return ""
	}
	return "  " + boldStyle.Render(m.label) + "\n" + m.list.View()
}

func Select(label string, choices []string, defaultIdx int) (int, error) {
	if len(choices) == 0 {
		return -1, fmt.Errorf("empty options list provided")
	}
	items := make([]list.Item, len(choices))
	for i, c := range choices {
		items[i] = selectItem(c)
	}
	l := list.New(items, selectDelegate{}, 40, len(choices))
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(false)
	l.SetShowPagination(false)
	l.Select(defaultIdx)

	m := selectModel{list: l, label: label, choice: defaultIdx}
	res, err := tea.NewProgram(m).Run()
	if err != nil {
		return -1, err
	}
	final := res.(selectModel)
	if final.err != nil {
		return -1, final.err
	}
	return final.choice, nil
}

type textModel struct {
	input    textinput.Model
	label    string
	quitting bool
	aborted  bool
}

func (m textModel) Init() tea.Cmd { return textinput.Blink }

func (m textModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.aborted = true
			m.quitting = true
			return m, tea.Quit
		case "enter":
			m.quitting = true
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m textModel) View() string {
	if m.quitting {
		return ""
	}
	return "  " + boldStyle.Render(m.label) + " " + m.input.View()
}

func runTextPrompt(label, defaultValue string, mask bool) (string, error) {
	ti := textinput.New()
	ti.Placeholder = defaultValue
	ti.Prompt = ""
	ti.Focus()
	if mask {
		ti.EchoMode = textinput.EchoPassword
		ti.EchoCharacter = '*'
	}
	m := textModel{input: ti, label: label}
	res, err := tea.NewProgram(m).Run()
	if err != nil {
		return "", err
	}
	final := res.(textModel)
	if final.aborted {
		return "", ErrAborted
	}
	val := final.input.Value()
	if val == "" {
		return defaultValue, nil
	}
	return val, nil
}

func Text(label, defaultValue string) (string, error) {
	return runTextPrompt(label, defaultValue, false)
}

func Password(label, defaultValue string) (string, error) {
	return runTextPrompt(label, defaultValue, true)
}

type confirmModel struct {
	label    string
	def      bool
	result   bool
	quitting bool
	aborted  bool
}

func (m confirmModel) Init() tea.Cmd { return nil }

func (m confirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.KeyMsg); ok {
		switch strings.ToLower(msg.String()) {
		case "ctrl+c", "esc":
			m.aborted = true
			m.quitting = true
			return m, tea.Quit
		case "enter":
			m.result = m.def
			m.quitting = true
			return m, tea.Quit
		case "y":
			m.result = true
			m.quitting = true
			return m, tea.Quit
		case "n":
			m.result = false
			m.quitting = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m confirmModel) View() string {
	if m.quitting {
		return ""
	}
	hint := "y/N"
	if m.def {
		hint = "Y/n"
	}
	return "  " + boldStyle.Render(m.label) + " " + dimStyle.Render("["+hint+"]:") + " "
}

func Confirm(label string, def bool) (bool, error) {
	m := confirmModel{label: label, def: def}
	res, err := tea.NewProgram(m).Run()
	if err != nil {
		return false, err
	}
	final := res.(confirmModel)
	if final.aborted {
		return false, ErrAborted
	}
	return final.result, nil
}
