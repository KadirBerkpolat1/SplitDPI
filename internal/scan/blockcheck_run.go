package scan

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/execx"
)

const blockcheckIntWait = 15 * time.Second

// BlockcheckParams configures a blockcheck2 batch run.
type BlockcheckParams struct {
	Domains  []string
	IPVS     string // "4" or "46"
	HTTP3    bool
	MarkFile string // optional; fake engine writes on SIGINT (tests)
}

// BlockcheckNFTables returns nft table specs (family + name) for a blockcheck2 PID.
func BlockcheckNFTables(pid int) []string {
	if pid <= 0 {
		return nil
	}
	p := strconv.Itoa(pid)
	return []string{
		"inet blockcheck2-" + p,
		"ip blockcheck2-" + p + "_test",
	}
}

// BlockcheckEnv returns blockcheck2 batch environment entries (eight vars).
func BlockcheckEnv(p BlockcheckParams) []string {
	http3 := "0"
	if p.HTTP3 {
		http3 = "1"
	}
	return []string{
		"BATCH=1",
		"SCANLEVEL=quick",
		"DOMAINS=" + strings.Join(p.Domains, " "),
		"IPVS=" + p.IPVS,
		"ENABLE_HTTP=0",
		"ENABLE_HTTPS_TLS12=1",
		"ENABLE_HTTPS_TLS13=0",
		"ENABLE_HTTP3=" + http3,
	}
}

func blockcheckCmdEnv(e engine.Engine, p BlockcheckParams) []string {
	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/sbin:/usr/bin:/sbin:/bin"
	}
	env := []string{
		"PATH=" + path,
		"LANG=C",
		"NFQWS2=" + e.Nfqws2(),
		"MDIG=" + e.Mdig(),
	}
	env = append(env, BlockcheckEnv(p)...)
	if p.MarkFile != "" {
		env = append(env, "MARK_FILE="+p.MarkFile)
	}
	if os.Getenv("SPLITDPI_BLOCKCHECK_SIMULATE") == "1" && os.Getenv("SPLITDPI_ROOT") != "" {
		env = append(env, "SIMULATE=1")
	}
	return env
}

// RunBlockcheck runs blockcheck2.sh under the engine dir, streaming lines to progress.
// On ctx cancel it sends SIGINT to the process group, waits up to 15s, then SIGKILL,
// and deletes blockcheck2 nft tables for that PID.
func RunBlockcheck(ctx context.Context, e engine.Engine, run execx.Runner, p BlockcheckParams, progress func(line string)) (string, error) {
	script := e.Blockcheck2()
	cmd := exec.Command("sh", script)
	cmd.Dir = e.Dir
	cmd.Env = blockcheckCmdEnv(e, p)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	pid := cmd.Process.Pid
	pgid := pid

	var log strings.Builder
	var logMu sync.Mutex
	var readWg sync.WaitGroup
	stream := func(r io.Reader) {
		defer readWg.Done()
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			line := sc.Text()
			logMu.Lock()
			log.WriteString(line)
			log.WriteByte('\n')
			if progress != nil {
				progress(line)
			}
			logMu.Unlock()
		}
	}
	readWg.Add(2)
	go stream(stdout)
	go stream(stderr)

	// Wait closes the pipes, so it must not run before both are drained:
	// SUMMARY is the last thing blockcheck2 prints.
	done := make(chan error, 1)
	go func() {
		readWg.Wait()
		done <- cmd.Wait()
	}()

	var runErr error
	select {
	case <-ctx.Done():
		runErr = ctx.Err()
		signalBlockcheckCancel(p.MarkFile)
		interruptBlockcheck(cmd.Process, pgid)
		select {
		case err := <-done:
			if err != nil && !errors.Is(runErr, context.Canceled) {
				runErr = err
			}
		case <-time.After(blockcheckIntWait):
			killBlockcheckPG(pgid)
			runErr = <-done
		}
	case err := <-done:
		runErr = err
	}

	_ = cleanupBlockcheckNFT(context.Background(), run, pid)
	return log.String(), runErr
}

func cleanupBlockcheckNFT(ctx context.Context, run execx.Runner, pid int) error {
	var first error
	for _, spec := range BlockcheckNFTables(pid) {
		if err := deleteNFTTable(ctx, run, spec); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// signalBlockcheckCancel lets the fake engine exit promptly when SIGINT does not
// reach the shell (common under go test); production blockcheck2 relies on INT.
func signalBlockcheckCancel(markFile string) {
	if markFile == "" {
		return
	}
	_ = os.WriteFile(markFile+".cancel", []byte("1"), 0o644)
}

func interruptBlockcheck(proc *os.Process, pgid int) {
	if proc != nil {
		_ = proc.Signal(syscall.SIGINT)
	}
	if pgid > 0 {
		_ = syscall.Kill(-pgid, syscall.SIGINT)
	}
}

func killBlockcheckPG(pgid int) {
	if pgid > 0 {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
}

func deleteNFTTable(ctx context.Context, run execx.Runner, spec string) error {
	family, name, ok := strings.Cut(spec, " ")
	if !ok {
		return fmt.Errorf("bad table spec %q", spec)
	}
	_, err := run(ctx, "", "nft", "delete", "table", family, name)
	if err != nil && strings.Contains(err.Error(), "No such file or directory") {
		return nil
	}
	return err
}
