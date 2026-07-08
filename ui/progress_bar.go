package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type Phase int

const (
	PhaseWaiting Phase = iota
	PhasePulling
	PhaseDownloading
	PhaseVerifying
	PhaseDownloaded
	PhaseExtracting
	PhaseComplete
	PhaseExists
	PhaseFailed
)

func (p Phase) label() string {
	switch p {
	case PhaseWaiting:
		return "Waiting"
	case PhasePulling:
		return "Pulling fs layer"
	case PhaseDownloading:
		return "Downloading"
	case PhaseVerifying:
		return "Verifying Checksum"
	case PhaseDownloaded:
		return "Download complete"
	case PhaseExtracting:
		return "Extracting"
	case PhaseComplete:
		return "Pull complete"
	case PhaseExists:
		return "Already exists"
	case PhaseFailed:
		return "Error"
	default:
		return ""
	}
}

func (p Phase) hasBar() bool {
	return p == PhaseDownloading || p == PhaseExtracting
}

func ParsePhase(status string) Phase {
	switch status {
	case "Waiting":
		return PhaseWaiting
	case "Pulling fs layer":
		return PhasePulling
	case "Downloading":
		return PhaseDownloading
	case "Verifying Checksum":
		return PhaseVerifying
	case "Download complete":
		return PhaseDownloaded
	case "Extracting":
		return PhaseExtracting
	case "Pull complete":
		return PhaseComplete
	case "Already exists":
		return PhaseExists
	default:
		return PhaseWaiting
	}
}

func PullHeader(ref string) string {
	repo, tag := ref, "latest"
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		repo, tag = ref[:i], ref[i+1:]
	}
	return fmt.Sprintf("%s: Pulling from %s", tag, repo)
}

type pullLayer struct {
	id      string
	phase   Phase
	current int64
	total   int64
	err     error
}

type pullUpdateMsg struct {
	index   int
	phase   Phase
	current int64
	total   int64
}

type pullAdvanceMsg struct{ index int }

type pullFailMsg struct {
	index int
	err   error
}

type pullDoneMsg struct{}

type multiPullModel struct {
	header   string
	layers   []pullLayer
	barWidth int
	done     bool
}

func newMultiPullModel(header string, ids []string) multiPullModel {
	layers := make([]pullLayer, len(ids))
	for i, id := range ids {
		layers[i] = pullLayer{id: id, phase: PhaseWaiting}
	}
	if len(layers) > 0 {
		layers[0].phase = PhasePulling
	}
	return multiPullModel{header: header, layers: layers, barWidth: 50}
}

func (m multiPullModel) Init() tea.Cmd { return nil }

func (m multiPullModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Leave room for "<id>: <status>  <cur>/<total>" around the bar.
		w := msg.Width - 45
		if w > 50 {
			w = 50
		}
		if w < 10 {
			w = 10
		}
		m.barWidth = w
		return m, nil

	case pullUpdateMsg:
		if msg.index >= 0 && msg.index < len(m.layers) {
			m.layers[msg.index].phase = msg.phase
			m.layers[msg.index].current = msg.current
			m.layers[msg.index].total = msg.total
		}
		return m, nil

	case pullAdvanceMsg:
		if msg.index >= 0 && msg.index < len(m.layers) {
			m.layers[msg.index].phase = PhaseComplete
			m.layers[msg.index].current = m.layers[msg.index].total
			if msg.index+1 < len(m.layers) {
				m.layers[msg.index+1].phase = PhasePulling
			}
		}
		return m, nil

	case pullFailMsg:
		if msg.index >= 0 && msg.index < len(m.layers) {
			m.layers[msg.index].phase = PhaseFailed
			m.layers[msg.index].err = msg.err
		}
		return m, nil

	case pullDoneMsg:
		m.done = true
		return m, tea.Quit
	}
	return m, nil
}

func (m multiPullModel) View() string {
	if m.done {
		return ""
	}
	var b strings.Builder
	if m.header != "" {
		fmt.Fprintf(&b, "%s\n", m.header)
	}
	for _, layer := range m.layers {
		switch {
		case layer.phase == PhaseFailed:
			fmt.Fprintf(&b, "%s: %s  %s\n",
				redStyle.Render(layer.id),
				redStyle.Render("Error"),
				dimStyle.Render(errText(layer.err)),
			)
		case layer.phase.hasBar():
			fmt.Fprintf(&b, "%s %s %s  %s\n",
				boldStyle.Render(layer.id+":"),
				cyanStyle.Render(layer.phase.label()),
				renderBar(m.barWidth, layer.current, layer.total),
				dimStyle.Render(fmt.Sprintf("%s/%s", humanSize(layer.current), humanSize(layer.total))),
			)
		default:
			fmt.Fprintf(&b, "%s: %s\n",
				dimStyle.Render(layer.id),
				dimStyle.Render(layer.phase.label()),
			)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderBar(width int, current, total int64) string {
	if width < 1 {
		width = 1
	}
	var ratio float64
	if total > 0 {
		ratio = float64(current) / float64(total)
	}
	if ratio > 1 {
		ratio = 1
	}
	if ratio < 0 {
		ratio = 0
	}
	filled := int(ratio * float64(width))
	if filled > width {
		filled = width
	}

	var b strings.Builder
	b.WriteByte('[')
	switch {
	case filled >= width:
		b.WriteString(strings.Repeat("=", width))
	case filled <= 0:
		b.WriteByte('>')
		b.WriteString(strings.Repeat(" ", width-1))
	default:
		b.WriteString(strings.Repeat("=", filled-1))
		b.WriteByte('>')
		b.WriteString(strings.Repeat(" ", width-filled))
	}
	b.WriteByte(']')
	return b.String()
}

func humanSize(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%dB", n)
	}
	units := []string{"kB", "MB", "GB", "TB", "PB"}
	f := float64(n) / 1000
	i := 0
	for f >= 1000 && i < len(units)-1 {
		f /= 1000
		i++
	}
	return fmt.Sprintf("%.4g%s", f, units[i])
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

type MultiPullProgress struct {
	program *tea.Program
	done    chan struct{}
}

func NewMultiPullProgress(header string, ids []string) *MultiPullProgress {
	p := tea.NewProgram(newMultiPullModel(header, ids), tea.WithoutSignalHandler())
	return &MultiPullProgress{program: p, done: make(chan struct{})}
}

func (p *MultiPullProgress) Start() {
	go func() {
		defer close(p.done)
		_, _ = p.program.Run()
	}()
}

func (p *MultiPullProgress) Update(index int, phase Phase, current, total int64) {
	p.program.Send(pullUpdateMsg{index: index, phase: phase, current: current, total: total})
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
