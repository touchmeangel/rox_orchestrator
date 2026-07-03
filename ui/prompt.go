package ui

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

var ErrAborted = fmt.Errorf("cancelled by user")

type keyKind int

const (
	keyRunes keyKind = iota
	keyEnter
	keyBackspace
	keyUp
	keyDown
	keyLeft
	keyRight
	keyAbort
)

type keyEvent struct {
	kind  keyKind
	runes []rune
}

type rawReader struct {
	buf []byte
}

func newRawReader() (*rawReader, func(), error) {
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return nil, nil, fmt.Errorf("failed setting raw terminal state: %w", err)
	}
	fmt.Print("\x1b[?2004l")
	restore := func() {
		fmt.Print("\x1b[?2004h")
		term.Restore(int(os.Stdin.Fd()), oldState)
	}
	return &rawReader{buf: make([]byte, 4096)}, restore, nil
}

func (r *rawReader) next() (keyEvent, error) {
	n, err := os.Stdin.Read(r.buf)
	if err != nil {
		return keyEvent{}, ErrAborted
	}
	chunk := r.buf[:n]

	if n == 1 {
		switch chunk[0] {
		case 3, 4:
			return keyEvent{kind: keyAbort}, nil
		case 13:
			return keyEvent{kind: keyEnter}, nil
		case 127, 8:
			return keyEvent{kind: keyBackspace}, nil
		}
	}

	if n >= 3 && chunk[0] == 27 && chunk[1] == 91 {
		switch chunk[2] {
		case 65:
			return keyEvent{kind: keyUp}, nil
		case 66:
			return keyEvent{kind: keyDown}, nil
		case 67:
			return keyEvent{kind: keyRight}, nil
		case 68:
			return keyEvent{kind: keyLeft}, nil
		}
	}

	runes := make([]rune, 0, len(chunk))
	for _, b := range chunk {
		if b >= 32 && b <= 126 {
			runes = append(runes, rune(b))
		}
	}
	if len(runes) > 0 {
		return keyEvent{kind: keyRunes, runes: runes}, nil
	}
	return r.next()
}

func Select(label string, choices []string, defaultIdx int) (int, error) {
	if len(choices) == 0 {
		return -1, fmt.Errorf("empty options list provided")
	}

	fmt.Println("  " + Bold(label))
	currentIdx := defaultIdx

	r, restore, err := newRawReader()
	if err != nil {
		return -1, err
	}
	defer restore()

	renderMenu := func() {
		for i, choice := range choices {
			if i == currentIdx {
				fmt.Printf("\r\x1b[2K   %s %s\r\n", Cyan("›"), Cyan(choice))
			} else {
				fmt.Printf("\r\x1b[2K     %s\r\n", Dim(choice))
			}
		}
		fmt.Printf("\x1b[%dA", len(choices))
	}
	renderMenu()

	for {
		ev, err := r.next()
		if err != nil {
			return -1, err
		}
		switch ev.kind {
		case keyAbort:
			fmt.Printf("\x1b[%dB\r\n", len(choices))
			return -1, ErrAborted
		case keyEnter:
			fmt.Printf("\x1b[%dB\r\n", len(choices))
			return currentIdx, nil
		case keyUp:
			if currentIdx > 0 {
				currentIdx--
			} else {
				currentIdx = len(choices) - 1
			}
			renderMenu()
		case keyDown:
			if currentIdx < len(choices)-1 {
				currentIdx++
			} else {
				currentIdx = 0
			}
			renderMenu()
		}
	}
}

func Text(label, defaultValue string) (string, error) {
	if defaultValue != "" {
		fmt.Printf("  %s %s", Bold(label), defaultValue)
	} else {
		fmt.Printf("  %s ", Bold(label))
	}

	r, restore, err := newRawReader()
	if err != nil {
		return "", err
	}
	defer restore()

	input := []rune(defaultValue)
	pos := len(input)

	insert := func(ch rune) {
		input = append(input[:pos], append([]rune{ch}, input[pos:]...)...)
		pos++
		fmt.Printf("%c%s", ch, string(input[pos:]))
		if diff := len(input) - pos; diff > 0 {
			fmt.Printf("\x1b[%dD", diff)
		}
	}

	for {
		ev, err := r.next()
		if err != nil {
			return "", err
		}
		switch ev.kind {
		case keyAbort:
			fmt.Print("\r\n")
			return "", ErrAborted
		case keyEnter:
			fmt.Print("\r\n")
			return string(input), nil
		case keyBackspace:
			if pos > 0 {
				input = append(input[:pos-1], input[pos:]...)
				pos--
				fmt.Printf("\b\x1b[K%s", string(input[pos:]))
				if diff := len(input) - pos; diff > 0 {
					fmt.Printf("\x1b[%dD", diff)
				}
			}
		case keyLeft:
			if pos > 0 {
				pos--
				fmt.Print("\x1b[D")
			}
		case keyRight:
			if pos < len(input) {
				pos++
				fmt.Print("\x1b[C")
			}
		case keyRunes:
			for _, ch := range ev.runes {
				insert(ch)
			}
		}
	}
}

func Password(label, defaultValue string) (string, error) {
	fmt.Printf("  %s ", Bold(label))

	r, restore, err := newRawReader()
	if err != nil {
		return "", err
	}
	defer restore()

	var input []rune

	for {
		ev, err := r.next()
		if err != nil {
			return "", err
		}
		switch ev.kind {
		case keyAbort:
			fmt.Print("\r\n")
			return "", ErrAborted
		case keyEnter:
			fmt.Print("\r\n")
			val := string(input)
			if val == "" && defaultValue != "" {
				return defaultValue, nil
			}
			return val, nil
		case keyBackspace:
			if len(input) > 0 {
				input = input[:len(input)-1]
				fmt.Print("\b \b")
			}
		case keyRunes:
			input = append(input, ev.runes...)
			fmt.Print(strings.Repeat("*", len(ev.runes)))
		}
	}
}

func Confirm(label string, def bool) (bool, error) {
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	fmt.Printf("  %s %s ", Bold(label), Dim("["+hint+"]:"))

	r, restore, err := newRawReader()
	if err != nil {
		return false, err
	}
	defer restore()

	for {
		ev, err := r.next()
		if err != nil {
			return false, err
		}
		switch ev.kind {
		case keyAbort:
			fmt.Print("\r\n")
			return false, ErrAborted
		case keyEnter:
			fmt.Print("\r\n")
			return def, nil
		case keyRunes:
			for _, ch := range ev.runes {
				switch strings.ToLower(string(ch)) {
				case "y":
					fmt.Print("y\r\n")
					return true, nil
				case "n":
					fmt.Print("n\r\n")
					return false, nil
				}
			}
		}
	}
}
