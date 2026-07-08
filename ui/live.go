package ui

import (
	"fmt"
	"sync"
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
		fmt.Print("\r\033[K")
	}

	r.suspended = true
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

	fmt.Print("\r" + r.status + "\033[K")
	r.shown = r.status != ""
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
