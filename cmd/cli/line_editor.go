package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/chzyer/readline"
	"github.com/peterh/liner"
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
	MentionFunc func(string, int) ([]string, error)
	Prompt      string // ANSI-colored prompt string, default "> "
}

func newLineEditor(cfg lineEditorConfig) (lineEditor, error) {
	if isTTY(os.Stdin) && isTTY(os.Stdout) {
		// On macOS, prefer liner for better wide-character cursor positioning.
		if runtime.GOOS == "darwin" {
			ln, err := newLinerEditor(cfg)
			if err == nil {
				return ln, nil
			}
		}
		rl, err := newReadlineEditor(cfg)
		if err == nil {
			return rl, nil
		}
		// Fallback for non-macOS where readline init fails.
		if runtime.GOOS != "darwin" {
			ln, lnErr := newLinerEditor(cfg)
			if lnErr == nil {
				return ln, nil
			}
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

type linerEditor struct {
	state       *liner.State
	historyFile string
}

func newLinerEditor(cfg lineEditorConfig) (*linerEditor, error) {
	historyFile := strings.TrimSpace(cfg.HistoryFile)
	if historyFile != "" {
		if err := os.MkdirAll(filepath.Dir(historyFile), 0o755); err != nil {
			return nil, fmt.Errorf("cli: create history dir: %w", err)
		}
	}
	state := liner.NewLiner()
	state.SetCtrlCAborts(true)
	state.SetMultiLineMode(true)
	state.SetCompleter(buildLineCompleter(cfg.Commands, cfg.MentionFunc))

	if historyFile != "" {
		f, err := os.Open(historyFile)
		if err == nil {
			_, _ = state.ReadHistory(f)
			_ = f.Close()
		} else if !errors.Is(err, os.ErrNotExist) {
			state.Close()
			return nil, fmt.Errorf("cli: read history: %w", err)
		}
	}
	return &linerEditor{
		state:       state,
		historyFile: historyFile,
	}, nil
}

func buildLineCompleter(commands []string, mentionFunc func(string, int) ([]string, error)) func(string) []string {
	candidates := make([]string, 0, len(commands))
	for _, cmd := range commands {
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			continue
		}
		candidates = append(candidates, "/"+cmd)
	}
	return func(line string) []string {
		prefix := strings.TrimSpace(line)
		if prefix != "" && !strings.Contains(prefix, " ") && strings.HasPrefix(prefix, "/") {
			out := make([]string, 0, len(candidates))
			for _, candidate := range candidates {
				if strings.HasPrefix(candidate, prefix) {
					out = append(out, candidate)
				}
			}
			return out
		}
		if mentionFunc == nil {
			return nil
		}
		inputRunes := []rune(line)
		start, end, query, ok := mentionQueryAtCursor(inputRunes, len(inputRunes))
		if !ok {
			return nil
		}
		mentionCandidates, err := mentionFunc(query, 8)
		if err != nil || len(mentionCandidates) == 0 {
			return nil
		}
		out := make([]string, 0, len(mentionCandidates))
		for _, one := range mentionCandidates {
			one = strings.TrimSpace(one)
			if one == "" {
				continue
			}
			replaced, _ := replaceRuneSpan(inputRunes, start, end, "@"+one)
			out = append(out, string(replaced))
		}
		return out
	}
}

func newReadlineEditor(cfg lineEditorConfig) (*readlineEditor, error) {
	historyFile := strings.TrimSpace(cfg.HistoryFile)
	if historyFile != "" {
		if err := os.MkdirAll(filepath.Dir(historyFile), 0o755); err != nil {
			return nil, fmt.Errorf("cli: create history dir: %w", err)
		}
	}

	prompt := cfg.Prompt
	if strings.TrimSpace(prompt) == "" {
		prompt = "> "
	}
	autoComplete := &mixedAutoCompleter{
		commands:    append([]string(nil), cfg.Commands...),
		mentionFunc: cfg.MentionFunc,
	}
	rl, err := readline.NewEx(&readline.Config{
		Prompt:            prompt,
		HistoryFile:       historyFile,
		AutoComplete:      autoComplete,
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

func (l *linerEditor) ReadLine(prompt string) (string, error) {
	if l == nil || l.state == nil {
		return "", io.EOF
	}
	line, err := l.state.Prompt(prompt)
	if err == nil {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			l.state.AppendHistory(trimmed)
		}
		return trimmed, nil
	}
	if errors.Is(err, liner.ErrPromptAborted) {
		return "", errInputInterrupt
	}
	if errors.Is(err, io.EOF) {
		return "", errInputEOF
	}
	return "", err
}

func (l *linerEditor) ReadSecret(prompt string) (string, error) {
	if l == nil || l.state == nil {
		return "", io.EOF
	}
	line, err := l.state.PasswordPrompt(prompt)
	if err == nil {
		return strings.TrimSpace(line), nil
	}
	if errors.Is(err, liner.ErrPromptAborted) {
		return "", errInputInterrupt
	}
	if errors.Is(err, io.EOF) {
		return "", errInputEOF
	}
	return "", err
}

func (l *linerEditor) Output() io.Writer {
	return os.Stdout
}

func (l *linerEditor) Close() error {
	if l == nil || l.state == nil {
		return nil
	}
	var retErr error
	if l.historyFile != "" {
		f, err := os.OpenFile(l.historyFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			retErr = fmt.Errorf("cli: write history: %w", err)
		} else {
			if _, err := l.state.WriteHistory(f); err != nil && retErr == nil {
				retErr = fmt.Errorf("cli: write history: %w", err)
			}
			if err := f.Close(); err != nil && retErr == nil {
				retErr = fmt.Errorf("cli: close history: %w", err)
			}
		}
	}
	if err := l.state.Close(); err != nil && retErr == nil {
		retErr = err
	}
	return retErr
}

type mixedAutoCompleter struct {
	commands    []string
	mentionFunc func(string, int) ([]string, error)
}

func (c *mixedAutoCompleter) Do(line []rune, pos int) ([][]rune, int) {
	if c == nil {
		return nil, 0
	}
	if pos < 0 {
		pos = 0
	}
	if pos > len(line) {
		pos = len(line)
	}
	prefix := strings.TrimSpace(string(line[:pos]))
	if prefix != "" && !strings.Contains(prefix, " ") && strings.HasPrefix(prefix, "/") {
		out := make([][]rune, 0, len(c.commands))
		for _, cmd := range c.commands {
			cmd = strings.TrimSpace(cmd)
			if cmd == "" {
				continue
			}
			candidate := "/" + cmd
			if strings.HasPrefix(candidate, prefix) {
				out = append(out, []rune(candidate))
			}
		}
		return out, len([]rune(prefix))
	}

	if c.mentionFunc == nil {
		return nil, 0
	}
	start, _, query, ok := mentionQueryAtCursor(line, pos)
	if !ok {
		return nil, 0
	}
	candidates, err := c.mentionFunc(query, 8)
	if err != nil || len(candidates) == 0 {
		return nil, 0
	}
	out := make([][]rune, 0, len(candidates))
	for _, one := range candidates {
		one = strings.TrimSpace(one)
		if one == "" {
			continue
		}
		out = append(out, []rune("@"+one))
	}
	return out, pos - start
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
