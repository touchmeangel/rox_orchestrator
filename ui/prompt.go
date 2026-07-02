package ui

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// reader is shared so prompts compose in a sequence without losing buffered
// input between calls.
var reader = bufio.NewReader(os.Stdin)

// ErrAborted is returned when the user sends EOF (Ctrl-D) mid-prompt —
// callers should treat it like the old questionary "ask() -> None" abort.
var ErrAborted = fmt.Errorf("cancelled by user")

func readLine() (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", ErrAborted
	}
	return strings.TrimSpace(line), nil
}

// Select prints a numbered menu and returns the chosen index. This trades
// questionary's arrow-key nav for something that needs zero dependencies —
// type a number, hit enter. Swap in charmbracelet/huh later if you want the
// fancier UI back; nothing else in this package depends on how Select is
// implemented internally.
func Select(label string, choices []string, defaultIdx int) (int, error) {
	fmt.Println("  " + Bold(label))
	for i, c := range choices {
		marker := " "
		if i == defaultIdx {
			marker = Cyan("›")
		}
		fmt.Printf("   %s %d) %s\n", marker, i+1, c)
	}
	for {
		fmt.Printf("  %s ", Dim(fmt.Sprintf("[%d]:", defaultIdx+1)))
		line, err := readLine()
		if err != nil {
			return 0, err
		}
		if line == "" {
			return defaultIdx, nil
		}
		n, err := strconv.Atoi(line)
		if err != nil || n < 1 || n > len(choices) {
			fmt.Println("  " + Red(fmt.Sprintf("enter a number 1-%d", len(choices))))
			continue
		}
		return n - 1, nil
	}
}

// Text prompts for a free-text value, returning def if the user hits enter.
func Text(label, def string) (string, error) {
	suffix := ""
	if def != "" {
		suffix = " " + Dim("["+def+"]")
	}
	fmt.Printf("  %s%s: ", Bold(label), suffix)
	line, err := readLine()
	if err != nil {
		return "", err
	}
	if line == "" {
		return def, nil
	}
	return line, nil
}

// Password prompts for a secret. NOTE: this is plaintext-visible input —
// real masking needs raw terminal mode (golang.org/x/term), a dependency
// this package deliberately skips. `go get golang.org/x/term` and swap the
// body of this function if that matters for your setup.
func Password(label string) (string, error) {
	fmt.Println("  " + Yellow("⚠") + Dim("  input below is not masked"))
	fmt.Printf("  %s: ", Bold(label))
	line, err := readLine()
	if err != nil {
		return "", err
	}
	return line, nil
}

func Confirm(label string, def bool) (bool, error) {
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	fmt.Printf("  %s %s: ", Bold(label), Dim("["+hint+"]"))
	line, err := readLine()
	if err != nil {
		return false, err
	}
	line = strings.ToLower(line)
	switch line {
	case "":
		return def, nil
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		fmt.Println("  " + Red("enter y or n"))
		return Confirm(label, def)
	}
}
