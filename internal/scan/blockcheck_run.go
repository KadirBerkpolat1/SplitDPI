package scan

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/KadirBerkpolat1/SplitDPI/internal/domain"
	"github.com/KadirBerkpolat1/SplitDPI/internal/engine"
	"github.com/KadirBerkpolat1/SplitDPI/internal/execx"
	"github.com/KadirBerkpolat1/SplitDPI/internal/nft"
)

const blockcheckIntWait = 15 * time.Second

// BlockcheckParams configures a blockcheck2 batch run.
type BlockcheckParams struct {
	Domains []string
	IPVS    string // "4" or "46"
	HTTP3   bool
	// DoH makes blockcheck2 resolve through these DoH URLs instead of the
	// system resolver; empty leaves it to blockcheck2's own spoofing check.
	DoH []string
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

// BlockcheckEnv returns blockcheck2 batch environment entries (eight vars,
// ten with DoH). blockcheck2 evals each domain as root, so any domain that is
// not a valid hostname is an error.
func BlockcheckEnv(p BlockcheckParams) ([]string, error) {
	for _, d := range p.Domains {
		if !domain.Valid(d) {
			return nil, domain.InvalidError{Input: d}
		}
	}
	http3 := "0"
	if p.HTTP3 {
		http3 = "1"
	}
	env := []string{
		"BATCH=1",
		"SCANLEVEL=quick",
		"DOMAINS=" + strings.Join(p.Domains, " "),
		"IPVS=" + p.IPVS,
		"ENABLE_HTTP=0",
		"ENABLE_HTTPS_TLS12=1",
		"ENABLE_HTTPS_TLS13=0",
		"ENABLE_HTTP3=" + http3,
	}
	if len(p.DoH) > 0 {
		// blockcheck2 then picks the first of DOH_SERVERS that resolves.
		env = append(env, "SECURE_DNS=1", "DOH_SERVERS="+strings.Join(p.DoH, " "))
	}
	return env, nil
}

func blockcheckCmdEnv(e engine.Engine, p BlockcheckParams) ([]string, error) {
	bc, err := BlockcheckEnv(p)
	if err != nil {
		return nil, err
	}
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
	env = append(env, bc...)
	if os.Getenv("SPLITDPI_BLOCKCHECK_SIMULATE") == "1" && os.Getenv("SPLITDPI_ROOT") != "" {
		env = append(env, "SIMULATE=1")
	}
	return env, nil
}

// RunBlockcheck runs blockcheck2.sh under the engine dir, streaming lines to progress.
// On ctx cancel it sends SIGINT to the process group, waits up to 15s, then SIGKILL,
// and deletes blockcheck2 nft tables for that PID.
func RunBlockcheck(ctx context.Context, e engine.Engine, run execx.Runner, p BlockcheckParams, progress func(line string)) (string, error) {
	env, err := blockcheckCmdEnv(e, p)
	if err != nil {
		return "", err
	}
	script := e.Blockcheck2()
	cmd := exec.Command("sh", script)
	cmd.Dir = e.Dir
	cmd.Env = env
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
		family, name, _ := strings.Cut(spec, " ")
		if err := nft.DeleteTable(ctx, run, family, name); err != nil && first == nil {
			first = err
		}
	}
	return first
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
