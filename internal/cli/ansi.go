package cli

import "os"

var (
	reset  = ""
	bold   = ""
	red    = ""
	green  = ""
	yellow = ""
	cyan   = ""
	gray   = ""
)

func initColors() {
	if isTerminal(os.Stdout.Fd()) {
		reset = "\033[0m"
		bold = "\033[1m"
		red = "\033[31m"
		green = "\033[32m"
		yellow = "\033[33m"
		cyan = "\033[36m"
		gray = "\033[90m"
	}
}

func init() {
	initColors()
}
