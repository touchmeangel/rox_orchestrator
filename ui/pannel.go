package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	lgtable "github.com/charmbracelet/lipgloss/table"
)

var panelStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(lipgloss.Color("240")).
	Padding(0, 2)

func Panel(body string) string {
	return panelStyle.Render(strings.TrimRight(body, "\n"))
}

type Table struct {
	Headers []string
	Rows    [][]string
}

func (t Table) Render() string {
	tbl := lgtable.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(dimStyle).
		Headers(t.Headers...).
		Rows(t.Rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == lgtable.HeaderRow {
				return dimStyle.Bold(true)
			}
			return lipgloss.NewStyle().Padding(0, 1)
		})
	return tbl.Render()
}

func (t Table) Print() {
	fmt.Println(t.Render())
}
