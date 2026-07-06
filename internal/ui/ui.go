// Package ui wraps interactive prompts (charmbracelet/huh) behind TTY
// checks. Every prompt in skillx has a flag equivalent; when stdin is not a
// terminal, callers must fail with a message naming that flag instead of
// prompting. NonInteractiveErr builds that failure.
package ui

import (
	"errors"
	"fmt"
	"os"

	"github.com/charmbracelet/huh"
	"golang.org/x/term"
)

// IsInteractive reports whether we may prompt: stdin and stderr are TTYs.
func IsInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
}

// NonInteractiveErr is returned instead of prompting when stdin is not a TTY.
func NonInteractiveErr(what, flag string) error {
	return fmt.Errorf("%s is required and stdin is not a terminal; pass %s", what, flag)
}

// IsAbort reports whether err came from the user cancelling a prompt
// (Ctrl-C / Esc). Callers should exit cleanly without partial work.
func IsAbort(err error) bool {
	return errors.Is(err, huh.ErrUserAborted)
}

// Option is a labeled value for select prompts.
type Option struct {
	Label    string
	Value    string
	Selected bool
}

// Input prompts for a single line of text.
func Input(title, placeholder string) (string, error) {
	var out string
	in := huh.NewInput().Title(title).Placeholder(placeholder).Value(&out)
	if err := huh.NewForm(huh.NewGroup(in)).Run(); err != nil {
		return "", err
	}
	return out, nil
}

// Confirm asks a yes/no question, defaulting to defaultYes.
func Confirm(title string, defaultYes bool) (bool, error) {
	out := defaultYes
	c := huh.NewConfirm().Title(title).Value(&out)
	if err := huh.NewForm(huh.NewGroup(c)).Run(); err != nil {
		return false, err
	}
	return out, nil
}

// MultiSelect prompts for zero or more values.
func MultiSelect(title string, options []Option) ([]string, error) {
	opts := make([]huh.Option[string], 0, len(options))
	var preselected []string
	for _, o := range options {
		opts = append(opts, huh.NewOption(o.Label, o.Value))
		if o.Selected {
			preselected = append(preselected, o.Value)
		}
	}
	selected := preselected
	ms := huh.NewMultiSelect[string]().Title(title).Options(opts...).Value(&selected)
	if err := huh.NewForm(huh.NewGroup(ms)).Run(); err != nil {
		return nil, err
	}
	return selected, nil
}
