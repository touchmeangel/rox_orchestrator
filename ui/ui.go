package ui

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	reset  = "\x1b[0m"
	bold   = "\x1b[1m"
	dim    = "\x1b[2m"
	cyan   = "\x1b[36m"
	red    = "\x1b[31m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
)

var noColor = os.Getenv("NO_COLOR") != ""

func wrap(code, s string) string {
	if noColor {
		return s
	}
	return code + s + reset
}

func Cyan(s string) string     { return wrap(cyan, s) }
func Bold(s string) string     { return wrap(bold, s) }
func Dim(s string) string      { return wrap(dim, s) }
func Red(s string) string      { return wrap(red, s) }
func Green(s string) string    { return wrap(green, s) }
func Yellow(s string) string   { return wrap(yellow, s) }
func BoldCyan(s string) string { return wrap(bold+cyan, s) }

const Logo = `   __
__(o )>
\___)`

func Banner() {
	fmt.Println()
	for _, line := range strings.Split(Logo, "\n") {
		fmt.Println("  " + Cyan(line))
	}
	fmt.Println()
	fmt.Println("  " + Bold("ignite") + Dim("  ·  EVM Smart Contract Security Research Agent"))
	fmt.Println()
}

func Rule(label string) {
	if label == "" {
		fmt.Println(Dim(strings.Repeat("─", 60)))
		return
	}
	fmt.Println(Dim(fmt.Sprintf("── %s %s", label, strings.Repeat("─", max(2, 56-utf8.RuneCountInString(label))))))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Panel draws a rounded box around body (which may be multi-line).
func Panel(body string) {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	width := 0
	for _, l := range lines {
		if w := visibleWidth(l); w > width {
			width = w
		}
	}
	pad := 2
	fmt.Println(Dim("  ╭" + strings.Repeat("─", width+pad*2) + "╮"))
	for _, l := range lines {
		gap := width - visibleWidth(l)
		fmt.Println(Dim("  │") + strings.Repeat(" ", pad) + l + strings.Repeat(" ", gap+pad) + Dim("│"))
	}
	fmt.Println(Dim("  ╰" + strings.Repeat("─", width+pad*2) + "╯"))
}

// visibleWidth strips ANSI escapes before measuring — otherwise colored
// strings throw off column alignment.
func visibleWidth(s string) int {
	inEscape := false
	n := 0
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		n++
	}
	return n
}

// Table renders a simple aligned table, roughly matching rich's box.SIMPLE.
type Table struct {
	Headers []string
	Rows    [][]string
}

func (t Table) Print() {
	widths := make([]int, len(t.Headers))
	for i, h := range t.Headers {
		widths[i] = visibleWidth(h)
	}
	for _, row := range t.Rows {
		for i, cell := range row {
			if i < len(widths) {
				if w := visibleWidth(cell); w > widths[i] {
					widths[i] = w
				}
			}
		}
	}

	printRow := func(cells []string, style func(string) string) {
		parts := make([]string, len(cells))
		for i, c := range cells {
			gap := widths[i] - visibleWidth(c)
			parts[i] = style(c) + strings.Repeat(" ", gap)
		}
		fmt.Println("  " + strings.Join(parts, "   "))
	}

	printRow(t.Headers, Dim)
	for _, row := range t.Rows {
		printRow(row, func(s string) string { return s })
	}
}

// Spinner is a single-line spinner for long-running steps (docker pulls,
// container runs) — stopped and cleared once real output starts streaming.
type Spinner struct {
	label string
	stop  chan struct{}
	done  chan struct{}
	mu    sync.Mutex
	live  bool
}

var spinnerFrames = []string{"·", "*", "✷", "✸", "✹", "✺", "✹", "✸", "✷", "*"}

func NewSpinner(label string) *Spinner {
	return &Spinner{label: label, stop: make(chan struct{}), done: make(chan struct{})}
}

func (s *Spinner) Start() {
	s.mu.Lock()
	s.live = true
	s.mu.Unlock()
	go func() {
		defer close(s.done)
		i := 0
		t := time.NewTicker(120 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				fmt.Print("\r\x1b[2K")
				return
			case <-t.C:
				fmt.Printf("\r  %s %s", Cyan(spinnerFrames[i%len(spinnerFrames)]), Dim(s.label))
				i++
			}
		}
	}()
}

func WelcomeBanner(pipelineLabel string, primaryModel string, providerKey string, totalModels int) {
	fmt.Println()
	for _, line := range strings.Split(Logo, "\n") {
		fmt.Println("  " + Cyan(line))
	}
	fmt.Println()

	header := Bold("ignite") + Dim("  ·  EVM Smart Contract Security Research Agent") + "\n\n" +
		"Welcome back — point me at a repo and I'll start digging.\n"

	var modelsText string
	if primaryModel != "" {
		suffix := ""
		if totalModels > 1 {
			s := "s"
			if totalModels == 2 {
				s = ""
			}
			suffix = Dim(fmt.Sprintf("  +%d fallback%s", totalModels-1, s))
		}

		modelsText = fmt.Sprintf("  %s   %s   %s",
			Cyan(strings.ToLower(pipelineLabel)),
			primaryModel+suffix,
			Dim("("+providerKey+")"),
		)
	}

	footer := "\n" + Dim("github.com/touchmeangel/ignite_agent") + "\n" + Dim(time.Now().Format("2006-01-02 15:04:05"))

	Panel(header + "\n" + modelsText + "\n" + footer)
	fmt.Println()
}

func (s *Spinner) Stop() {
	s.mu.Lock()
	if !s.live {
		s.mu.Unlock()
		return
	}
	s.live = false
	s.mu.Unlock()
	close(s.stop)
	<-s.done
}
