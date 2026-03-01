// Package setup implements the interactive first-run wizard that guides users
// through configuring, installing, and starting ReminderRelay.
package setup

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Prompter provides reusable terminal prompts backed by an io.Reader/Writer
// pair. In production these are os.Stdin and os.Stdout; tests can inject
// buffers for deterministic input.
type Prompter struct {
	scanner *bufio.Scanner
	w       io.Writer
}

// NewPrompter creates a Prompter wired to the given reader and writer.
func NewPrompter(r io.Reader, w io.Writer) *Prompter {
	return &Prompter{scanner: bufio.NewScanner(r), w: w}
}

// String prompts the user for a text value. If the user presses Enter without
// typing anything, defaultVal is returned. An empty defaultVal means the field
// is required and the prompt repeats until a non-empty value is given.
func (p *Prompter) String(label, defaultVal string) string {
	for {
		if defaultVal != "" {
			_, _ = fmt.Fprintf(p.w, "  %s [%s]: ", label, defaultVal)
		} else {
			_, _ = fmt.Fprintf(p.w, "  %s: ", label)
		}

		if !p.scanner.Scan() {
			return defaultVal
		}

		val := strings.TrimSpace(p.scanner.Text())
		if val == "" {
			if defaultVal != "" {
				return defaultVal
			}
			_, _ = fmt.Fprintf(p.w, "  (required — please enter a value)\n")
			continue
		}
		return val
	}
}

// Secret prompts for a sensitive value (like a token). The value is not
// masked (terminal raw mode would require a dependency), but is marked as
// sensitive in the prompt label.
func (p *Prompter) Secret(label string) string {
	for {
		_, _ = fmt.Fprintf(p.w, "  %s: ", label)

		if !p.scanner.Scan() {
			return ""
		}

		val := strings.TrimSpace(p.scanner.Text())
		if val == "" {
			_, _ = fmt.Fprintf(p.w, "  (required — please enter a value)\n")
			continue
		}
		return val
	}
}

// Confirm asks a yes/no question. defaultYes controls what happens when the
// user presses Enter without typing: true → yes, false → no.
func (p *Prompter) Confirm(label string, defaultYes bool) bool {
	hint := "[y/N]"
	if defaultYes {
		hint = "[Y/n]"
	}

	_, _ = fmt.Fprintf(p.w, "  %s %s: ", label, hint)

	if !p.scanner.Scan() {
		return defaultYes
	}

	answer := strings.TrimSpace(strings.ToLower(p.scanner.Text()))
	if answer == "" {
		return defaultYes
	}
	return answer == "y" || answer == "yes"
}

// Select presents a numbered list and asks the user to pick one. Returns the
// zero-based index of the chosen option.
func (p *Prompter) Select(label string, options []string) (int, error) {
	if len(options) == 0 {
		return -1, fmt.Errorf("no options to select from")
	}

	_, _ = fmt.Fprintf(p.w, "  %s:\n", label)
	for i, opt := range options {
		_, _ = fmt.Fprintf(p.w, "    %d) %s\n", i+1, opt)
	}

	for {
		_, _ = fmt.Fprintf(p.w, "  Choice [1-%d]: ", len(options))

		if !p.scanner.Scan() {
			return -1, fmt.Errorf("no input")
		}

		val := strings.TrimSpace(p.scanner.Text())
		n, err := strconv.Atoi(val)
		if err != nil || n < 1 || n > len(options) {
			_, _ = fmt.Fprintf(p.w, "  (enter a number between 1 and %d)\n", len(options))
			continue
		}
		return n - 1, nil
	}
}

// MultiSelect presents a numbered list and asks the user to pick one or more,
// separated by commas (e.g. "1,3,5"). Returns the zero-based indices.
func (p *Prompter) MultiSelect(label string, options []string) ([]int, error) {
	if len(options) == 0 {
		return nil, fmt.Errorf("no options to select from")
	}

	_, _ = fmt.Fprintf(p.w, "  %s:\n", label)
	for i, opt := range options {
		_, _ = fmt.Fprintf(p.w, "    %d) %s\n", i+1, opt)
	}

	for {
		_, _ = fmt.Fprintf(p.w, "  Choices (comma-separated, e.g. 1,3): ")

		if !p.scanner.Scan() {
			return nil, fmt.Errorf("no input")
		}

		parts := strings.Split(p.scanner.Text(), ",")
		var indices []int
		valid := true

		for _, part := range parts {
			n, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil || n < 1 || n > len(options) {
				_, _ = fmt.Fprintf(p.w, "  (enter numbers between 1 and %d, separated by commas)\n", len(options))
				valid = false
				break
			}
			indices = append(indices, n-1)
		}

		if valid && len(indices) > 0 {
			return indices, nil
		}
	}
}
