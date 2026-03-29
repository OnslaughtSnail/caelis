//go:build linux

package execenv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	stdruntime "runtime"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const internalHelperCommand = "__caelis_execenv_helper__"

type landlockSandboxFactory struct{}

func (f landlockSandboxFactory) Type() string {
	return landlockSandboxType
}

func (f landlockSandboxFactory) Build(cfg Config) (CommandRunner, error) {
	return newLandlockRunner(cfg.SandboxPolicy, cfg.SandboxHelperPath), nil
}

type landlockRunner struct {
	execCommand    func(context.Context, string, ...string) *exec.Cmd
	executablePath func() (string, error)
	helperPath     string
	probe          func() error
	goos           string
	policy         SandboxPolicy
	sessionManager *SessionManager
}

func newLandlockRunner(policy SandboxPolicy, helperPath string) CommandRunner {
	return &landlockRunner{
		execCommand:    exec.CommandContext,
		executablePath: os.Executable,
		helperPath:     strings.TrimSpace(helperPath),
		probe:          probeLandlockSupport,
		goos:           stdruntime.GOOS,
		policy:         policy,
		sessionManager: NewSessionManager(DefaultSessionManagerConfig()),
	}
}

func (l *landlockRunner) Probe(ctx context.Context) error {
	if l.goos != "linux" {
		return fmt.Errorf("landlock sandbox is only supported on linux (current=%s)", l.goos)
	}
	if l.probe == nil {
		return l.probeHelper(ctx)
	}
	if err := l.probe(); err != nil {
		return fmt.Errorf("landlock sandbox unavailable: %w", err)
	}
	if err := l.probeHelper(ctx); err != nil {
		return fmt.Errorf("landlock sandbox unavailable: %w", err)
	}
	return nil
}

func (l *landlockRunner) Run(ctx context.Context, req CommandRequest) (CommandResult, error) {
	runCtx := ctx
	cancel := func() {}
	if req.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, req.Timeout)
	}
	defer cancel()

	policyCWD, err := resolveHostWorkDir(req.Dir)
	if err != nil {
		return CommandResult{}, fmt.Errorf("tool: resolve landlock workdir failed: %w", err)
	}
	effectivePolicy := sandboxPolicyForCommand(l.policy, req)
	exePath, err := l.resolveHelperPath()
	if err != nil {
		return CommandResult{}, fmt.Errorf("tool: resolve landlock helper path failed: %w", err)
	}
	helperArgs, err := buildLandlockHelperArgs(effectivePolicy, policyCWD, policyCWD, req.Command)
	if err != nil {
		return CommandResult{}, fmt.Errorf("tool: build landlock helper args failed: %w", err)
	}

	cmd := l.execCommand(runCtx, exePath, helperArgs...)
	applyNonInteractiveCommandDefaults(cmd)
	cmd.Env = mergeCommandEnv(req.EnvOverrides)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	lastOutput := atomic.Int64{}
	lastOutput.Store(time.Now().UnixNano())
	cmd.Stdout = &activityWriter{buffer: &stdout, lastOutput: &lastOutput, stream: "stdout", onOutput: req.OnOutput}
	cmd.Stderr = &activityWriter{buffer: &stderr, lastOutput: &lastOutput, stream: "stderr", onOutput: req.OnOutput}

	if err := cmd.Start(); err != nil {
		return CommandResult{}, fmt.Errorf("tool: landlock sandbox command start failed: %w", err)
	}
	waitErr := waitWithIdleTimeout(runCtx, cmd, req.IdleTimeout, &lastOutput)

	result := CommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}
	if waitErr == nil {
		return result, nil
	}
	result.ExitCode = resolveExitCode(waitErr)
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) || errors.Is(waitErr, context.DeadlineExceeded) {
		label := "context deadline"
		if req.Timeout > 0 {
			label = req.Timeout.String()
		}
		return result, WrapCodedError(
			ErrorCodeSandboxCommandTimeout,
			waitErr,
			"tool: landlock sandbox command timed out after %s; %s",
			label,
			commandOutputSummary(result),
		)
	}
	if errors.Is(waitErr, errIdleTimeout) {
		label := "idle limit"
		if req.IdleTimeout > 0 {
			label = req.IdleTimeout.String()
		}
		return result, NewCodedError(
			ErrorCodeSandboxIdleTimeout,
			"tool: landlock sandbox command produced no output for %s and was terminated (likely interactive or long-running); %s",
			label,
			commandOutputSummary(result),
		)
	}
	return result, fmt.Errorf("tool: landlock sandbox command failed: %w; %s", waitErr, commandOutputSummary(result))
}

func (l *landlockRunner) StartAsync(_ context.Context, req CommandRequest) (string, error) {
	if req.TTY {
		return "", fmt.Errorf("tool: landlock async tty is not supported")
	}
	policyCWD, err := resolveHostWorkDir(req.Dir)
	if err != nil {
		return "", fmt.Errorf("tool: resolve landlock workdir failed: %w", err)
	}
	effectivePolicy := sandboxPolicyForCommand(l.policy, req)
	exePath, err := l.resolveHelperPath()
	if err != nil {
		return "", fmt.Errorf("tool: resolve landlock helper path failed: %w", err)
	}
	session, err := l.sessionManager.StartSession(AsyncSessionConfig{
		Command:         req.Command,
		Dir:             req.Dir,
		Env:             mergeCommandEnv(req.EnvOverrides),
		OutputBufferCap: 256 * 1024,
		Timeout:         req.Timeout,
		IdleTimeout:     req.IdleTimeout,
		BuildCommand: func(ctx context.Context, cfg AsyncSessionConfig) (*exec.Cmd, error) {
			helperArgs, err := buildLandlockHelperArgs(effectivePolicy, policyCWD, policyCWD, cfg.Command)
			if err != nil {
				return nil, err
			}
			cmd := l.execCommand(ctx, exePath, helperArgs...)
			cmd.Env = append([]string(nil), cfg.Env...)
			return cmd, nil
		},
	})
	if err != nil {
		return "", err
	}
	return session.ID, nil
}

func (l *landlockRunner) WriteInput(sessionID string, input []byte) error {
	return l.sessionManager.WriteInput(sessionID, input)
}

func (l *landlockRunner) ReadOutput(sessionID string, stdoutMarker, stderrMarker int64) (stdout, stderr []byte, newStdoutMarker, newStderrMarker int64, err error) {
	return l.sessionManager.ReadOutput(sessionID, stdoutMarker, stderrMarker)
}

func (l *landlockRunner) GetSessionStatus(sessionID string) (SessionStatus, error) {
	return l.sessionManager.GetSessionStatus(sessionID)
}

func (l *landlockRunner) WaitSession(ctx context.Context, sessionID string, timeout time.Duration) (CommandResult, error) {
	waitCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	exitCode, err := l.sessionManager.WaitSession(waitCtx, sessionID)
	if err != nil {
		return CommandResult{}, err
	}
	result, err := l.sessionManager.GetResult(sessionID)
	if err != nil {
		return CommandResult{ExitCode: exitCode}, nil
	}
	return result, nil
}

func (l *landlockRunner) TerminateSession(sessionID string) error {
	return l.sessionManager.TerminateSession(sessionID)
}

func (l *landlockRunner) ListSessions() []SessionInfo {
	return l.sessionManager.ListSessions()
}

func (l *landlockRunner) Close() error {
	if l.sessionManager != nil {
		return l.sessionManager.Close()
	}
	return nil
}

func buildLandlockHelperArgs(policy SandboxPolicy, policyCWD, commandCWD, command string) ([]string, error) {
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		return nil, err
	}
	args := []string{
		internalHelperCommand,
		"--policy-json", string(policyJSON),
		"--policy-cwd", policyCWD,
		"--command-cwd", commandCWD,
		"--command", command,
	}
	return args, nil
}

func (l *landlockRunner) resolveHelperPath() (string, error) {
	exePath := strings.TrimSpace(l.helperPath)
	if exePath != "" {
		return exePath, nil
	}
	return l.executablePath()
}

func (l *landlockRunner) probeHelper(ctx context.Context) error {
	helperPath, err := l.resolveHelperPath()
	if err != nil {
		return fmt.Errorf("resolve landlock helper path: %w", err)
	}
	if ctx == nil {
		return fmt.Errorf("landlock helper probe requires context")
	}
	cmd := l.execCommand(ctx, helperPath, internalHelperCommand, "--probe")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			return fmt.Errorf("landlock helper probe failed via %s: %w", helperPath, err)
		}
		return fmt.Errorf("landlock helper probe failed via %s: %w; stderr=%s", helperPath, err, message)
	}
	return nil
}

func probeLandlockSupport() error {
	abi, err := landlockABI()
	if err == nil {
		if abi <= 0 {
			return errors.New("landlock returned invalid ABI version")
		}
		return nil
	}
	if errors.Is(err, unix.ENOSYS) {
		return errors.New("landlock syscalls are unavailable on this kernel")
	}
	if errors.Is(err, unix.EOPNOTSUPP) {
		return errors.New("landlock is disabled or unsupported by this kernel")
	}
	return err
}

func MaybeRunInternalHelper(args []string) bool {
	if len(args) == 0 || args[0] != internalHelperCommand {
		return false
	}
	if err := runInternalHelper(args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "internal sandbox helper failed: %v\n", err)
		os.Exit(1)
	}
	return true
}

type internalHelperConfig struct {
	Probe      bool
	PolicyJSON string
	PolicyCWD  string
	CommandCWD string
	Command    string
}

func runInternalHelper(args []string) error {
	stdruntime.LockOSThread()

	fs := flag.NewFlagSet(internalHelperCommand, flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var cfg internalHelperConfig
	fs.BoolVar(&cfg.Probe, "probe", false, "helper availability probe")
	fs.StringVar(&cfg.PolicyJSON, "policy-json", "", "sandbox policy json")
	fs.StringVar(&cfg.PolicyCWD, "policy-cwd", "", "sandbox policy cwd")
	fs.StringVar(&cfg.CommandCWD, "command-cwd", "", "command cwd")
	fs.StringVar(&cfg.Command, "command", "", "command to execute")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if cfg.Probe {
		return nil
	}
	if strings.TrimSpace(cfg.PolicyJSON) == "" {
		return errors.New("missing --policy-json")
	}
	if strings.TrimSpace(cfg.Command) == "" {
		return errors.New("missing --command")
	}

	var policy SandboxPolicy
	if err := json.Unmarshal([]byte(cfg.PolicyJSON), &policy); err != nil {
		return fmt.Errorf("decode policy: %w", err)
	}

	needFSRestrictions := policy.Type != SandboxPolicyDangerFull && policy.Type != SandboxPolicyExternal
	if needFSRestrictions || !policy.NetworkAccess {
		if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
			return fmt.Errorf("set no_new_privs: %w", err)
		}
	}
	if !policy.NetworkAccess {
		if err := installRestrictedNetworkSeccomp(); err != nil {
			return fmt.Errorf("install seccomp: %w", err)
		}
	}
	if needFSRestrictions {
		if err := applyLandlockFilesystemPolicy(policy, cfg.PolicyCWD); err != nil {
			return err
		}
	}
	if strings.TrimSpace(cfg.CommandCWD) != "" {
		if err := os.Chdir(cfg.CommandCWD); err != nil {
			return fmt.Errorf("chdir: %w", err)
		}
	}

	shellPath, err := exec.LookPath("bash")
	if err != nil {
		return fmt.Errorf("resolve bash: %w", err)
	}
	return unix.Exec(shellPath, []string{"bash", "-lc", cfg.Command}, os.Environ())
}

func applyLandlockFilesystemPolicy(policy SandboxPolicy, policyCWD string) error {
	abi, err := landlockABI()
	if err != nil {
		return err
	}
	attr := unix.LandlockRulesetAttr{
		Access_fs: landlockReadWriteMaskForABI(abi),
	}
	rulesetFD, err := landlockCreateRuleset(&attr, 0)
	if err != nil {
		return fmt.Errorf("create landlock ruleset: %w", err)
	}
	defer unix.Close(rulesetFD)

	if err := landlockAddPathRule(rulesetFD, "/", landlockReadOnlyMaskForABI(abi)); err != nil {
		return fmt.Errorf("allow read-only root: %w", err)
	}
	if err := landlockAddPathRule(rulesetFD, "/dev/null", landlockFileReadWriteMaskForABI(abi)); err != nil {
		return fmt.Errorf("allow /dev/null writes: %w", err)
	}
	for _, root := range landlockWritableRoots(policy, policyCWD) {
		if err := landlockAddPathRule(rulesetFD, root, landlockReadWriteMaskForABI(abi)); err != nil {
			return fmt.Errorf("allow writable root %s: %w", root, err)
		}
	}
	if err := landlockRestrictSelf(rulesetFD); err != nil {
		return fmt.Errorf("restrict self with landlock: %w", err)
	}
	return nil
}

func landlockWritableRoots(policy SandboxPolicy, workDir string) []string {
	if policy.Type == SandboxPolicyReadOnly {
		return nil
	}
	roots := make([]string, 0, len(policy.WritableRoots)+8)
	for _, one := range policy.WritableRoots {
		resolved := resolveBwrapPath(workDir, one)
		if resolved != "" {
			roots = append(roots, resolved)
		}
	}
	roots = append(roots, "/tmp", "/var/tmp")
	home, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(home) != "" {
		roots = append(roots, filepath.Join(home, ".cache"))
	}
	return filterExistingPaths(normalizeStringList(roots))
}

func landlockABI() (int, error) {
	r1, _, errno := unix.Syscall(
		unix.SYS_LANDLOCK_CREATE_RULESET,
		0,
		0,
		uintptr(unix.LANDLOCK_CREATE_RULESET_VERSION),
	)
	if errno != 0 {
		return 0, errno
	}
	return int(r1), nil
}

func landlockCreateRuleset(attr *unix.LandlockRulesetAttr, flags uintptr) (int, error) {
	var ptr uintptr
	var size uintptr
	if attr != nil {
		ptr = uintptr(unsafe.Pointer(attr))
		size = unsafe.Sizeof(*attr)
	}
	r1, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, ptr, size, flags)
	if errno != 0 {
		return 0, errno
	}
	return int(r1), nil
}

func landlockAddPathRule(rulesetFD int, path string, access uint64) error {
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)

	attr := unix.LandlockPathBeneathAttr{
		Allowed_access: access,
		Parent_fd:      int32(fd),
	}
	_, _, errno := unix.Syscall6(
		unix.SYS_LANDLOCK_ADD_RULE,
		uintptr(rulesetFD),
		uintptr(unix.LANDLOCK_RULE_PATH_BENEATH),
		uintptr(unsafe.Pointer(&attr)),
		0,
		0,
		0,
	)
	if errno != 0 {
		return errno
	}
	return nil
}

func landlockRestrictSelf(rulesetFD int) error {
	_, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(rulesetFD), 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func landlockReadOnlyMaskForABI(abi int) uint64 {
	mask := uint64(unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_DIR |
		unix.LANDLOCK_ACCESS_FS_EXECUTE)
	if abi >= 5 {
		mask |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	return mask
}

func landlockReadWriteMaskForABI(abi int) uint64 {
	mask := landlockReadOnlyMaskForABI(abi) |
		unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR |
		unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG |
		unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_FIFO |
		unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM
	if abi >= 2 {
		mask |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		mask |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	return mask
}

func landlockFileReadWriteMaskForABI(abi int) uint64 {
	mask := uint64(unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_WRITE_FILE)
	if abi >= 3 {
		mask |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	if abi >= 5 {
		mask |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	return mask
}

func installRestrictedNetworkSeccomp() error {
	prog, err := buildRestrictedNetworkSeccompProgram()
	if err != nil {
		return err
	}
	if err := unix.Prctl(unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER, uintptr(unsafe.Pointer(&prog)), 0, 0); err != nil {
		return err
	}
	return nil
}

func buildRestrictedNetworkSeccompProgram() (unix.SockFprog, error) {
	deny := uint32(unix.SECCOMP_RET_ERRNO | (unix.EPERM & unix.SECCOMP_RET_DATA))
	allow := uint32(unix.SECCOMP_RET_ALLOW)
	kill := uint32(unix.SECCOMP_RET_KILL_PROCESS)

	filters := []unix.SockFilter{
		bpfStmt(unix.BPF_LD|unix.BPF_W|unix.BPF_ABS, seccompDataOffsetArch),
		bpfJump(unix.BPF_JMP|unix.BPF_JEQ|unix.BPF_K, seccompAuditArch(), 1, 0),
		bpfStmt(unix.BPF_RET|unix.BPF_K, kill),
		bpfStmt(unix.BPF_LD|unix.BPF_W|unix.BPF_ABS, seccompDataOffsetNR),
	}

	for _, nr := range []int{
		unix.SYS_CONNECT,
		unix.SYS_ACCEPT,
		unix.SYS_ACCEPT4,
		unix.SYS_BIND,
		unix.SYS_LISTEN,
		unix.SYS_GETPEERNAME,
		unix.SYS_GETSOCKNAME,
		unix.SYS_SHUTDOWN,
		unix.SYS_SENDTO,
		unix.SYS_SENDMMSG,
		unix.SYS_RECVMMSG,
		unix.SYS_GETSOCKOPT,
		unix.SYS_SETSOCKOPT,
	} {
		filters = append(filters,
			bpfJump(unix.BPF_JMP|unix.BPF_JEQ|unix.BPF_K, uint32(nr), 0, 1),
			bpfStmt(unix.BPF_RET|unix.BPF_K, deny),
		)
	}

	filters = append(filters,
		bpfJump(unix.BPF_JMP|unix.BPF_JEQ|unix.BPF_K, uint32(unix.SYS_SOCKET), 0, 4),
		bpfStmt(unix.BPF_LD|unix.BPF_W|unix.BPF_ABS, seccompDataOffsetArg0),
		bpfJump(unix.BPF_JMP|unix.BPF_JEQ|unix.BPF_K, unix.AF_UNIX, 1, 0),
		bpfStmt(unix.BPF_RET|unix.BPF_K, deny),
		bpfStmt(unix.BPF_RET|unix.BPF_K, allow),
		bpfJump(unix.BPF_JMP|unix.BPF_JEQ|unix.BPF_K, uint32(unix.SYS_SOCKETPAIR), 0, 4),
		bpfStmt(unix.BPF_LD|unix.BPF_W|unix.BPF_ABS, seccompDataOffsetArg0),
		bpfJump(unix.BPF_JMP|unix.BPF_JEQ|unix.BPF_K, unix.AF_UNIX, 1, 0),
		bpfStmt(unix.BPF_RET|unix.BPF_K, deny),
		bpfStmt(unix.BPF_RET|unix.BPF_K, allow),
		bpfStmt(unix.BPF_RET|unix.BPF_K, allow),
	)

	return unix.SockFprog{
		Len:    uint16(len(filters)),
		Filter: &filters[0],
	}, nil
}

func seccompAuditArch() uint32 {
	switch stdruntime.GOARCH {
	case "amd64":
		return unix.AUDIT_ARCH_X86_64
	case "arm64":
		return unix.AUDIT_ARCH_AARCH64
	default:
		panic(fmt.Sprintf("unsupported architecture for seccomp filter: %s", stdruntime.GOARCH))
	}
}

func bpfStmt(code uint16, k uint32) unix.SockFilter {
	return unix.SockFilter{Code: code, K: k}
}

func bpfJump(code uint16, k uint32, jt, jf uint8) unix.SockFilter {
	return unix.SockFilter{Code: code, Jt: jt, Jf: jf, K: k}
}

const (
	seccompDataOffsetNR   = 0
	seccompDataOffsetArch = 4
	seccompDataOffsetArg0 = 16
)

func init() {
	if err := RegisterSandboxFactory(landlockSandboxFactory{}); err != nil {
		panic(err)
	}
}
