package ui

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/muesli/termenv"
)

// Options configures UI output writers and color mode.
type Options struct {
	Stdout io.Writer
	Stderr io.Writer
	Color  string // auto|always|never
}

const colorNever = "never"

// UI provides formatted terminal output with optional color support.
type UI struct {
	out *Printer
	err *Printer
}

// ParseError indicates an invalid option was provided.
type ParseError struct{ msg string }

func (e *ParseError) Error() string { return e.msg }

// New creates a UI with the given options.
func New(opts Options) (*UI, error) {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}

	colorMode := strings.ToLower(strings.TrimSpace(opts.Color))
	if colorMode == "" {
		colorMode = "auto"
	}
	if colorMode != "auto" && colorMode != "always" && colorMode != colorNever {
		return nil, &ParseError{msg: "invalid --color (expected auto|always|never)"}
	}

	out := termenv.NewOutput(opts.Stdout, termenv.WithProfile(termenv.EnvColorProfile()))
	errOut := termenv.NewOutput(opts.Stderr, termenv.WithProfile(termenv.EnvColorProfile()))

	outProfile := chooseProfile(out.Profile, colorMode)
	errProfile := chooseProfile(errOut.Profile, colorMode)

	return &UI{
		out: newPrinter(out, outProfile),
		err: newPrinter(errOut, errProfile),
	}, nil
}

func chooseProfile(detected termenv.Profile, mode string) termenv.Profile {
	if termenv.EnvNoColor() {
		return termenv.Ascii
	}
	switch mode {
	case colorNever:
		return termenv.Ascii
	case "always":
		return termenv.TrueColor
	default:
		return detected
	}
}

// Out returns the stdout printer.
func (u *UI) Out() *Printer { return u.out }

// Err returns the stderr printer.
func (u *UI) Err() *Printer { return u.err }

// Printer writes formatted and optionally colored output.
type Printer struct {
	o       *termenv.Output
	profile termenv.Profile
}

func newPrinter(o *termenv.Output, profile termenv.Profile) *Printer {
	return &Printer{o: o, profile: profile}
}

// ColorEnabled returns true if color output is active.
func (p *Printer) ColorEnabled() bool { return p.profile != termenv.Ascii }

func (p *Printer) line(s string) {
	_, _ = io.WriteString(p.o, s+"\n")
}

// Successf prints a green-colored formatted message.
func (p *Printer) Successf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if p.ColorEnabled() {
		msg = termenv.String(msg).Foreground(p.profile.Color("#22c55e")).String()
	}
	p.line(msg)
}

// Errorf prints a red-colored formatted message.
func (p *Printer) Errorf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if p.ColorEnabled() {
		msg = termenv.String(msg).Foreground(p.profile.Color("#ef4444")).String()
	}
	p.line(msg)
}

// Infof prints a blue-colored formatted message.
func (p *Printer) Infof(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if p.ColorEnabled() {
		msg = termenv.String(msg).Foreground(p.profile.Color("#3b82f6")).String()
	}
	p.line(msg)
}

// Printf prints a plain formatted message.
func (p *Printer) Printf(format string, args ...any) {
	p.line(fmt.Sprintf(format, args...))
}

// Println prints a plain message with a trailing newline.
func (p *Printer) Println(msg string) { p.line(msg) }

// Print writes a message without a trailing newline.
func (p *Printer) Print(msg string) {
	_, _ = io.WriteString(p.o, msg)
}

// Writer returns the underlying io.Writer for use with tabwriter, json.Encoder, etc.
func (p *Printer) Writer() io.Writer { return p.o }

type ctxKey struct{}

// WithUI stores a UI in the context.
func WithUI(ctx context.Context, u *UI) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

// FromContext retrieves the UI from the context, or nil if absent.
func FromContext(ctx context.Context) *UI {
	v := ctx.Value(ctxKey{})
	if v == nil {
		return nil
	}
	u, _ := v.(*UI)
	return u
}
