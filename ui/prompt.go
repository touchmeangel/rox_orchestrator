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
				fmt.Printf("\r\x1b[2K   %s %s\r\n", Cyan("›"), Cyan(choice))
			} else {
				fmt.Printf("\r\x1b[2K     %s\r\n", Dim(choice))
			}
		}
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
				fmt.Printf("\x1b[%dB\r\n", len(choices))
				return -1, ErrAborted
			case 13: // Enter
				fmt.Printf("\x1b[%dB\r\n", len(choices))
				return currentIdx, nil
			}
		} else if n == 3 && buf[0] == 27 && buf[1] == 91 { // Arrows
			switch buf[2] {
			case 65: // Up
				if currentIdx > 0 {
					currentIdx--
				} else {
					currentIdx = len(choices) - 1
				}
				renderMenu()
			case 66: // Down
				if currentIdx < len(choices)-1 {
					currentIdx++
				} else {
					currentIdx = 0
				}
				renderMenu()
			}
		}
	}
}

func Text(label, defaultValue string) (string, error) {
	if defaultValue != "" {
		fmt.Printf("  %s %s", Bold(label), defaultValue)
	} else {
		fmt.Printf("  %s ", Bold(label))
	}

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return "", err
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	input := []rune(defaultValue)
	pos := len(input)

	buf := make([]byte, 3)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return "", ErrAborted
		}

		if n == 1 {
			b := buf[0]
			switch b {
			case 3, 4: // Ctrl+C, Ctrl+D
				fmt.Print("\r\n")
				return "", ErrAborted
			case 13: // Enter
				fmt.Print("\r\n")
				return string(input), nil
			case 127, 8: // Backspace
				if pos > 0 {
					input = append(input[:pos-1], input[pos:]...)
					pos--
					fmt.Printf("\b\x1b[K%s", string(input[pos:]))
					if diff := len(input) - pos; diff > 0 {
						fmt.Printf("\x1b[%dD", diff)
					}
				}
			default:
				if b >= 32 && b <= 126 { // Printable ASCII
					input = append(input[:pos], append([]rune{rune(b)}, input[pos:]...)...)
					pos++
					fmt.Printf("%c%s", b, string(input[pos:]))
					if diff := len(input) - pos; diff > 0 {
						fmt.Printf("\x1b[%dD", diff)
					}
				}
			}
		} else if n == 3 && buf[0] == 27 && buf[1] == 91 { // Arrows
			switch buf[2] {
			case 68: // Left
				if pos > 0 {
					pos--
					fmt.Print("\x1b[D")
				}
			case 67: // Right
				if pos < len(input) {
					pos++
					fmt.Print("\x1b[C")
				}
			}
		}
	}
}

func Password(label, defaultValue string) (string, error) {
	fmt.Printf("  %s ", Bold(label))

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return "", err
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	var input []rune
	buf := make([]byte, 3)

	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return "", ErrAborted
		}

		if n == 1 {
			b := buf[0]
			switch b {
			case 3, 4: // Ctrl+C, Ctrl+D
				fmt.Print("\r\n")
				return "", ErrAborted
			case 13: // Enter
				fmt.Print("\r\n")
				val := string(input)
				if val == "" && defaultValue != "" {
					return defaultValue, nil
				}
				return val, nil
			case 127, 8: // Backspace
				if len(input) > 0 {
					input = input[:len(input)-1]
				}
			default:
				if b >= 32 && b <= 126 {
					input = append(input, rune(b))
				}
			}
		}
	}
}

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
		if char == 3 || char == 4 { // Ctrl+C, Ctrl+D
			fmt.Print("\r\n")
			return false, ErrAborted
		}
		if char == 13 { // Enter
			fmt.Print("\r\n")
			return def, nil
		}

		charStr := strings.ToLower(string(char))
		if charStr == "y" {
			fmt.Print("y\r\n")
			return true, nil
		}
		if charStr == "n" {
			fmt.Print("n\r\n")
			return false, nil
		}
	}
}
