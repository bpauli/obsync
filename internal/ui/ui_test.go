package ui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/muesli/termenv"
)

func TestNew_InvalidColor(t *testing.T) {
	t.Parallel()

	_, err := New(Options{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Color: "nope"})
	if err == nil || !strings.Contains(err.Error(), "invalid --color") {
		t.Fatalf("expected invalid color error, got: %v", err)
	}
}

func TestPrinter_OutputAndColor(t *testing.T) {
	t.Parallel()

	var outBuf, errBuf bytes.Buffer

	out := termenv.NewOutput(&outBuf, termenv.WithProfile(termenv.Ascii))
	errOut := termenv.NewOutput(&errBuf, termenv.WithProfile(termenv.Ascii))
	pOut := newPrinter(out, termenv.TrueColor)
	pErr := newPrinter(errOut, termenv.TrueColor)

	if !pOut.ColorEnabled() {
		t.Fatalf("expected color enabled for Out")
	}
	if !pErr.ColorEnabled() {
		t.Fatalf("expected color enabled for Err")
	}

	pOut.Successf("ok %s", "now")
	pErr.Errorf("bad %d", 1)
	pOut.Printf("hello %s", "world")
	pOut.Println("line")
	pOut.Infof("info %s", "msg")

	if got := outBuf.String(); !strings.Contains(got, "ok now") {
		t.Fatalf("unexpected stdout: %q", got)
	}
	if got := outBuf.String(); !strings.Contains(got, "info msg") {
		t.Fatalf("expected info output in stdout: %q", got)
	}
	if got := errBuf.String(); !strings.Contains(got, "bad 1") {
		t.Fatalf("unexpected stderr: %q", got)
	}

	// Colored printers should emit ANSI escapes
	if !strings.Contains(outBuf.String(), "\x1b[") {
		t.Fatalf("expected ANSI escapes in stdout, got: %q", outBuf.String())
	}
	if !strings.Contains(errBuf.String(), "\x1b[") {
		t.Fatalf("expected ANSI escapes in stderr, got: %q", errBuf.String())
	}
}

func TestPrinter_NoColor(t *testing.T) {
	t.Parallel()

	var outBuf bytes.Buffer
	out := termenv.NewOutput(&outBuf, termenv.WithProfile(termenv.Ascii))
	p := newPrinter(out, termenv.Ascii)

	if p.ColorEnabled() {
		t.Fatalf("expected color disabled")
	}

	p.Successf("ok")
	p.Infof("info")

	if strings.Contains(outBuf.String(), "\x1b[") {
		t.Fatalf("did not expect ANSI escapes: %q", outBuf.String())
	}
}

func TestPrinter_Print(t *testing.T) {
	t.Parallel()

	var outBuf bytes.Buffer
	out := termenv.NewOutput(&outBuf, termenv.WithProfile(termenv.Ascii))
	p := newPrinter(out, termenv.Ascii)

	p.Print("hello")

	if got := outBuf.String(); got != "hello" {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestChooseProfile_NoColorEnv(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	if got := chooseProfile(termenv.TrueColor, "always"); got != termenv.Ascii {
		t.Fatalf("expected ascii when NO_COLOR set, got: %v", got)
	}
}

func TestChooseProfile_Modes(t *testing.T) {
	t.Setenv("NO_COLOR", "")

	if got := chooseProfile(termenv.TrueColor, "never"); got != termenv.Ascii {
		t.Fatalf("never: expected ascii, got: %v", got)
	}
	if got := chooseProfile(termenv.Ascii, "always"); got != termenv.TrueColor {
		t.Fatalf("always: expected truecolor, got: %v", got)
	}
	if got := chooseProfile(termenv.Ascii, "auto"); got != termenv.Ascii {
		t.Fatalf("auto: expected detected, got: %v", got)
	}
}

func TestWithUIFromContext(t *testing.T) {
	t.Parallel()

	u, err := New(Options{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Color: "never"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := WithUI(context.Background(), u)
	got := FromContext(ctx)

	if got == nil {
		t.Fatalf("expected ui from context")
	}
	if got.Out() == nil || got.Err() == nil {
		t.Fatalf("expected printers")
	}
	if FromContext(context.Background()) != nil {
		t.Fatalf("expected nil when absent")
	}
}
