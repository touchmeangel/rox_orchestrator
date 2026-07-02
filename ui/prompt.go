package ui

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

var ErrAborted = fmt.Errorf("cancelled by user")

func Select(label string, choices []string, defaultIdx int) (int, error) {
	if len(choices) == 0 {
		return -1, fmt.Errorf("empty options list provided")
	}

	fmt.Println("  " + Bold(label))
	currentIdx := defaultIdx

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return -1, fmt.Errorf("failed setting raw terminal state: %w", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	renderMenu := func() {
		for i, choice := range choices {
			if i == currentIdx {
				// Clear current line, draw arrow indicator
				fmt.Printf("\r\x1b[2K   %s %s\r\n", Cyan("›"), Cyan(choice))
			} else {
				fmt.Printf("\r\x1b[2K     %s\r\n", Dim(choice))
			}
		}
		// Move cursor back up to the top of our menu block so we can redraw over it safely
		fmt.Printf("\x1b[%dA", len(choices))
	}

	renderMenu()

	buf := make([]byte, 3)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return -1, ErrAborted
		}

		if n == 1 {
			switch buf[0] {
			case 3, 4: // Ctrl+C or Ctrl+D
				// Restore terminal layout and bubble abort error cleanly
				fmt.Printf("\x1b[%dB\r\n", len(choices))
				return -1, ErrAborted
			case 13: // Enter key
				// Move cursor cleanly past the menu print lines block
				fmt.Printf("\x1b[%dB\r\n", len(choices))
				return currentIdx, nil
			}
		} else if n == 3 && buf[0] == 27 && buf[1] == 91 { // Escape sequencing logic for arrows
			switch buf[2] {
			case 65: // Arrow Up
				if currentIdx > 0 {
					currentIdx--
				} else {
					currentIdx = len(choices) - 1 // wrap around
				}
				renderMenu()
			case 66: // Arrow Down
				if currentIdx < len(choices)-1 {
					currentIdx++
				} else {
					currentIdx = 0 // wrap around
				}
				renderMenu()
			}
		}
	}
}

func Text(label, def string) (string, error) {
	suffix := ""
	if def != "" {
		suffix = " " + Dim("["+def+"]")
	}
	fmt.Printf("  %s%s: ", Bold(label), suffix)

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return "", err
	}

	var input []byte
	buf := make([]byte, 1)

	for {
		_, err := os.Stdin.Read(buf)
		if err != nil {
			term.Restore(int(os.Stdin.Fd()), oldState)
			return "", ErrAborted
		}

		char := buf[0]
		if char == 3 || char == 4 { // Ctrl+C / Ctrl+D
			term.Restore(int(os.Stdin.Fd()), oldState)
			fmt.Println()
			return "", ErrAborted
		}
		if char == 13 { // Enter
			break
		}
		if char == 127 { // Backspace
			if len(input) > 0 {
				input = input[:len(input)-1]
				fmt.Print("\b \b") // Visual wipe character
			}
			continue
		}
		// Print the native text character back to layout stream
		if char >= 32 && char <= 126 {
			input = append(input, char)
			fmt.Print(string(char))
		}
	}

	term.Restore(int(os.Stdin.Fd()), oldState)
	fmt.Println()

	res := strings.TrimSpace(string(input))
	if res == "" {
		return def, nil
	}
	return res, nil
}

// Password reads an obscured masking configuration line securely.
func Password(label string) (string, error) {
	fmt.Printf("  %s: ", Bold(label))
	state, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", ErrAborted
	}
	return strings.TrimSpace(string(state)), nil
}

// Confirm uses immediate single key character evaluation flags.
func Confirm(label string, def bool) (bool, error) {
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	fmt.Printf("  %s %s ", Bold(label), Dim("["+hint+"]:"))

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return false, err
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	buf := make([]byte, 1)
	for {
		_, err := os.Stdin.Read(buf)
		if err != nil {
			return false, ErrAborted
		}
		char := buf[0]
		if char == 3 || char == 4 {
			return false, ErrAborted
		}
		if char == 13 {
			return def, nil
		}
		charStr := strings.ToLower(string(char))
		if charStr == "y" {
			return true, nil
		}
		if charStr == "n" {
			return false, nil
		}
	}
}
