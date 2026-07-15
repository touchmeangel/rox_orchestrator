package ui

import (
	"fmt"
	"os"
	"sync"

	"golang.org/x/term"
)

type LiveRegion struct {
	mu        sync.Mutex
	status    string
	shown     bool
	suspended bool
}

func NewLiveRegion() *LiveRegion {
	return &LiveRegion{}
}

func (r *LiveRegion) Suspend() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.shown {
		fmt.Print("\r\033[K\n")
	}

	r.suspended = true
	r.shown = false
}

func (r *LiveRegion) Resume() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.suspended = false
	r.redrawLocked()
}

func (r *LiveRegion) SetStatus(status string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = status
	r.redrawLocked()
}

func (r *LiveRegion) WriteLine(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.shown {
		fmt.Print("\r\033[K")
	}
	fmt.Println(line)
	r.redrawLocked()
}

func (r *LiveRegion) redrawLocked() {
	if r.suspended {
		return
	}

	line := truncateToWidth(r.status, terminalWidth())
	fmt.Print("\r" + line + "\033[K")
	r.shown = line != ""
}

func (r *LiveRegion) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.shown {
		fmt.Print("\r\033[K")
	}
	r.status = ""
	r.shown = false
}

func terminalWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 1 {
		return w - 1
	}
	return 79
}

func truncateToWidth(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return s
	}

	runes := []rune(s)
	var b []rune
	visible := 0
	i := 0

	for i < len(runes) {
		if runes[i] == '\033' && i+1 < len(runes) && runes[i+1] == '[' {
			start := i
			i += 2
			for i < len(runes) && runes[i] != 'm' {
				i++
			}
			if i < len(runes) {
				i++
			}
			b = append(b, runes[start:i]...)
			continue
		}

		if visible >= maxWidth {
			b = append(b, '…', '\033', '[', '0', 'm')
			return string(b)
		}

		b = append(b, runes[i])
		visible++
		i++
	}

	return string(b)
}
