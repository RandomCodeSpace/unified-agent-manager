package session

import "fmt"

func validatedScrollbackLines(lines int) (int, error) {
	if lines == 0 {
		return historyLines, nil
	}
	if lines < minHistoryLines || lines > maxHistoryLines {
		return 0, fmt.Errorf("scrollback lines %d outside %d..%d", lines, minHistoryLines, maxHistoryLines)
	}
	return lines, nil
}
