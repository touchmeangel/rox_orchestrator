package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	cyanStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	boldStyle     = lipgloss.NewStyle().Bold(true)
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	redStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	greenStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	yellowStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	boldCyanStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
)

func Cyan(s string) string     { return cyanStyle.Render(s) }
func Bold(s string) string     { return boldStyle.Render(s) }
func Dim(s string) string      { return dimStyle.Render(s) }
func Red(s string) string      { return redStyle.Render(s) }
func Green(s string) string    { return greenStyle.Render(s) }
func Yellow(s string) string   { return yellowStyle.Render(s) }
func BoldCyan(s string) string { return boldCyanStyle.Render(s) }

func Rule(label string) string {
	const width = 60
	if label == "" {
		return dimStyle.Render(strings.Repeat("─", width))
	}
	pad := width - lipgloss.Width(label) - 4
	if pad < 2 {
		pad = 2
	}
	return dimStyle.Render("── " + label + " " + strings.Repeat("─", pad))
}
