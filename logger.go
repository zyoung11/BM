package main

import (
	"fmt"
	"os"
	"strings"
)

const (
	prefixPrompt  = "[?] "
	prefixInput   = "[>] "
	prefixInfo    = "[i] "
	prefixSuccess = "[+] "
	prefixWarn    = "[!] "
	prefixError   = "[-] "

	colorBlue   = "\x1b[38;2;138;173;244m"
	colorGreen  = "\x1b[38;2;166;218;149m"
	colorYellow = "\x1b[38;2;238;212;159m"
	colorRed    = "\x1b[38;2;237;135;150m"
	colorReset  = "\x1b[0m"
)

type Logger struct {
	out *os.File
}

func newLogger() *Logger {
	return &Logger{out: os.Stderr}
}

// writeLine splits msg on blank lines (\n\n) and prefixes each non-empty
// part with the colored tag, so bilingual messages render with one prefix
// per language instead of a single shared prefix.
func (l *Logger) writeLine(prefix, color, msg string) {
	for part := range strings.SplitSeq(msg, "\n\n") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		fmt.Fprintln(l.out, color+prefix+colorReset+" "+part)
	}
}

func (l *Logger) writeNoNewline(prefix, color, msg string) {
	fmt.Fprint(l.out, color+prefix+colorReset+" "+msg)
}

func (l *Logger) Prompt(msg string)  { l.writeLine(prefixPrompt, colorBlue, msg) }
func (l *Logger) Input(msg string)   { l.writeNoNewline(prefixInput, colorBlue, msg) }
func (l *Logger) Info(msg string)    { l.writeLine(prefixInfo, colorBlue, msg) }
func (l *Logger) Success(msg string) { l.writeLine(prefixSuccess, colorGreen, msg) }
func (l *Logger) Warn(msg string)    { l.writeLine(prefixWarn, colorYellow, msg) }
func (l *Logger) Error(msg string)   { l.writeLine(prefixError, colorRed, msg) }

func (l *Logger) Warnf(format string, args ...any) {
	l.Warn(fmt.Sprintf(format, args...))
}

func (l *Logger) Errorf(format string, args ...any) {
	l.Error(fmt.Sprintf(format, args...))
}

func (l *Logger) Fatalf(format string, args ...any) {
	l.Errorf(format, args...)
	os.Exit(1)
}

var l = newLogger()