package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/chzyer/readline"
)

var (
	errInputInterrupt = errors.New("cli: input interrupted")
	errInputEOF       = errors.New("cli: input eof")
)

type lineEditor interface {
	ReadLine(prompt string) (string, error)
	ReadSecret(prompt string) (string, error)
	Output() io.Writer
	Close() error
}

type lineEditorConfig struct {
	HistoryFile string
	Commands    []string
}

func newLineEditor(cfg lineEditorConfig) (lineEditor, error) {
	if isTTY(os.Stdin) && isTTY(os.Stdout) {
		rl, err := newReadlineEditor(cfg)
		if err == nil {
			return rl, nil
		}
	}
	return &stdioEditor{
		reader: bufio.NewReader(os.Stdin),
		out:    os.Stdout,
	}, nil
}

func isTTY(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

type readlineEditor struct {
	rl *readline.Instance
}

func newReadlineEditor(cfg lineEditorConfig) (*readlineEditor, error) {
	historyFile := strings.TrimSpace(cfg.HistoryFile)
	if historyFile != "" {
		if err := os.MkdirAll(filepath.Dir(historyFile), 0o755); err != nil {
			return nil, fmt.Errorf("cli: create history dir: %w", err)
		}
	}

	completerItems := make([]readline.PrefixCompleterInterface, 0, len(cfg.Commands))
	for _, cmd := range cfg.Commands {
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			continue
		}
		completerItems = append(completerItems, readline.PcItem("/"+cmd))
	}
	rl, err := readline.NewEx(&readline.Config{
		Prompt:            "> ",
		HistoryFile:       historyFile,
		AutoComplete:      readline.NewPrefixCompleter(completerItems...),
		InterruptPrompt:   "^C",
		EOFPrompt:         "exit",
		HistorySearchFold: true,
	})
	if err != nil {
		return nil, err
	}
	return &readlineEditor{rl: rl}, nil
}

func (r *readlineEditor) ReadLine(prompt string) (string, error) {
	if r == nil || r.rl == nil {
		return "", io.EOF
	}
	r.rl.SetPrompt(prompt)
	line, err := r.rl.Readline()
	if err == nil {
		return strings.TrimSpace(line), nil
	}
	if errors.Is(err, readline.ErrInterrupt) {
		return "", errInputInterrupt
	}
	if errors.Is(err, io.EOF) {
		return "", errInputEOF
	}
	return "", err
}

func (r *readlineEditor) ReadSecret(prompt string) (string, error) {
	if r == nil || r.rl == nil {
		return "", io.EOF
	}
	text, err := r.rl.ReadPassword(prompt)
	if err == nil {
		return strings.TrimSpace(string(text)), nil
	}
	if errors.Is(err, readline.ErrInterrupt) {
		return "", errInputInterrupt
	}
	if errors.Is(err, io.EOF) {
		return "", errInputEOF
	}
	return "", err
}

func (r *readlineEditor) Output() io.Writer {
	if r == nil || r.rl == nil {
		return os.Stdout
	}
	return r.rl.Stdout()
}

func (r *readlineEditor) Close() error {
	if r == nil || r.rl == nil {
		return nil
	}
	return r.rl.Close()
}

type stdioEditor struct {
	reader *bufio.Reader
	out    io.Writer
}

func (s *stdioEditor) ReadLine(prompt string) (string, error) {
	if s == nil || s.reader == nil {
		return "", io.EOF
	}
	fmt.Fprint(s.out, prompt)
	line, err := s.reader.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			return "", errInputEOF
		}
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (s *stdioEditor) ReadSecret(prompt string) (string, error) {
	return s.ReadLine(prompt)
}

func (s *stdioEditor) Output() io.Writer {
	if s == nil || s.out == nil {
		return os.Stdout
	}
	return s.out
}

func (s *stdioEditor) Close() error {
	return nil
}
