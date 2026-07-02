package ui

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

var reader = bufio.NewReader(os.Stdin)

var ErrAborted = fmt.Errorf("cancelled by user")

func readLine() (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", ErrAborted
	}
	return strings.TrimSpace(line), nil
}

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
