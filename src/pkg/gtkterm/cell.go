package gtkterm

// Cell represents a single character cell in the terminal
type Cell struct {
	Char       rune
	Foreground Color
	Background Color
	Bold       bool
	Italic     bool
	Underline  bool
	Reverse    bool
}

// EmptyCell returns an empty cell with default attributes
func EmptyCell() Cell {
	return Cell{
		Char:       ' ',
		Foreground: DefaultForeground,
		Background: DefaultBackground,
	}
}

// EmptyCellWithColors returns an empty cell with specified colors
func EmptyCellWithColors(fg, bg Color) Cell {
	return Cell{
		Char:       ' ',
		Foreground: fg,
		Background: bg,
	}
}
