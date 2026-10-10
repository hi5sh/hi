# Bubble Tea v1.3.6, without its startup query

A copy of github.com/charmbracelet/bubbletea v1.3.6 (MIT, see LICENSE) with
`tea_init.go` removed. That file asks the terminal for its background
colour when the program starts, in every hi command. Terminals that answer
late leave the reply (such as `^[[34;1R`) in the shell after hi exits. hi
sets the background itself in main (`setTerminalBackground`), so nothing
needs to ask. Upstream says the query goes away in v2; drop this copy then.
