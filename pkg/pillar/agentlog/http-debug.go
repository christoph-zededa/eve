package agentlog

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	ctrdd "github.com/containerd/containerd"
	"github.com/lf-edge/eve/pkg/pillar/base"
	"github.com/lf-edge/eve/pkg/pillar/containerd"
	"github.com/opencontainers/runtime-spec/specs-go"
)

var listenDebugRunning atomic.Bool

var listenAddress = "localhost:6543"

func roundToMb(b uint64) uint64 {
	kb := (b + 512) / 1024
	mb := (kb + 512) / 1024
	return mb
}

func writeOrLog(log *base.LogObject, w io.Writer, msg string) {
	if _, err := w.Write([]byte(msg)); err != nil {
		log.Errorf("Could not write to %+v: %+v", w, err)
	}
}

func writeAndLog(log *base.LogObject, w io.Writer, msg string) {
	_, err := w.Write([]byte(msg))
	if err != nil {
		log.Errorf("Could not write to %+v: %+v", w, err)
	}

	log.Warn(msg)
}

func logMemUsage(log *base.LogObject, file *os.File) {
	var m runtime.MemStats

	runtime.ReadMemStats(&m)
	log.Functionf("Alloc %d Mb, TotalAlloc %d Mb, Sys %d Mb, NumGC %d",
		roundToMb(m.Alloc), roundToMb(m.TotalAlloc), roundToMb(m.Sys), m.NumGC)

	if file != nil {
		// This goes to /persist/agentdebug/<agentname>/sigusr2 file
		// And there in not much difference from the above log except the CRNL at the end.
		statString := fmt.Sprintf("Alloc %d Mb, TotalAlloc %d Mb, Sys %d Mb, NumGC %d\n",
			roundToMb(m.Alloc), roundToMb(m.TotalAlloc), roundToMb(m.Sys), m.NumGC)
		file.WriteString(statString)
	}
}

// Print in sorted order based on top bytes
func logMemAllocationSites(log *base.LogObject, file *os.File) {
	reportZeroInUse := false
	numSites, sites := GetMemAllocationSites(reportZeroInUse)
	log.Warnf("alloc %d sites len %d", numSites, len(sites))
	sort.Slice(sites,
		func(i, j int) bool {
			return sites[i].InUseBytes > sites[j].InUseBytes ||
				(sites[i].InUseBytes == sites[j].InUseBytes &&
					sites[i].AllocBytes > sites[j].AllocBytes)
		})
	for _, site := range sites {
		log.Warnf("alloc %d bytes %d objects total %d/%d at:\n%s",
			site.InUseBytes, site.InUseObjects, site.AllocBytes,
			site.AllocObjects, site.PrintedStack)

		if file != nil {
			// This goes to /persist/agentdebug/<agentname>/sigusr2 file
			// And there in not much difference from the above log except the CRNL at the end.
			statString := fmt.Sprintf("alloc %d bytes %d objects total %d/%d at:\n%s\n",
				site.InUseBytes, site.InUseObjects, site.AllocBytes,
				site.AllocObjects, site.PrintedStack)
			file.WriteString(statString)
		}
	}
}

func dumpMemoryInfo(log *base.LogObject, fileName string) {
	log.Warnf("SIGUSR2 triggered memory info:\n")
	sigUsr2File, err := os.OpenFile(fileName,
		os.O_WRONLY|os.O_CREATE|os.O_SYNC|os.O_TRUNC, 0755)
	if err != nil {
		log.Errorf("handleSignals: Error opening file %s with: %s", fileName, err)
	} else {
		// This goes to /persist/agentdebug/<agentname>/sigusr2 file
		_, err := sigUsr2File.WriteString("SIGUSR2 triggered memory info:\n")
		if err != nil {
			log.Errorf("could not write to %s: %+v", fileName, err)
		}
	}

	logMemUsage(log, sigUsr2File)
	logMemAllocationSites(log, sigUsr2File)
	if sigUsr2File != nil {
		sigUsr2File.Close()
	}
}

type mutexWriter struct {
	w     io.Writer
	mutex *sync.Mutex
	done  chan struct{}
}

func (m mutexWriter) Write(p []byte) (n int, err error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	// seems containerd sometimes wants to write something into it,
	// but it is already too late
	if m.w == nil {
		return 0, syscall.ENOENT
	}
	n, err = m.w.Write(p)
	if err != nil {
		m.done <- struct{}{}
	}

	return n, err
}

type bpftraceHandler struct {
	log *base.LogObject
}

func (b bpftraceHandler) runInDebugContainer(w io.Writer, args []string, timeout time.Duration) error {
	ctrd, err := containerd.NewContainerdClient(false)
	if err != nil {
		return fmt.Errorf("could not initialize containerd client: %+v\n", err)
	}

	ctx, done := ctrd.CtrNewSystemServicesCtx()
	defer done()

	container, err := ctrd.CtrLoadContainer(ctx, "debug")
	if err != nil {
		return fmt.Errorf("loading container failed: %+v", err)
	}

	task, err := container.Task(ctx, nil)
	if err != nil {
		return fmt.Errorf("getting debug container task failed: %+v", err)
	}

	pspec := specs.Process{
		Args: args,
		Cwd:  "/",
		Scheduler: &specs.Scheduler{
			Deadline: uint64(time.Now().Add(timeout).Unix()),
		},
	}
	taskId := fmt.Sprintf("bpftrace-%d", rand.Int()) // TODO: avoid collision
	stderrBuf := bytes.Buffer{}

	writingDone := make(chan struct{})
	defer close(writingDone)
	mutexWriter := mutexWriter{
		w:     w,
		mutex: &sync.Mutex{},
		done:  writingDone,
	}
	stdcio := ctrd.CtrWriterCreator(mutexWriter, &stderrBuf)

	process, err := task.Exec(ctx, taskId, &pspec, stdcio)
	if err != nil {
		return fmt.Errorf("executing in task failed: %+v", err)
	}
	waiter, err := process.Wait(ctx)
	if err != nil {
		return fmt.Errorf("process wait failed: %+v", err)
	}
	err = process.Start(ctx)
	if err != nil {
		return fmt.Errorf("process start failed: %+v", err)
	}

	exitStatus := struct {
		exitCode        uint32
		killedByTimeout bool
	}{
		exitCode:        0,
		killedByTimeout: false,
	}

	timeoutTimer := time.NewTimer(timeout)
	select {
	case <-writingDone:
		exitStatus.killedByTimeout = true
		err := b.killProcess(ctx, process)
		if err != nil {
			b.log.Warnf("writer closed - killing process %+v failed: %v", args, err)
		}
	case <-timeoutTimer.C:
		exitStatus.killedByTimeout = true
		err := b.killProcess(ctx, process)
		if err != nil {
			b.log.Warnf("timeout - killing process %+v failed: %v", args, err)
		}
	case containerExitStatus := <-waiter:
		exitStatus.exitCode = containerExitStatus.ExitCode()
	}
	timeoutTimer.Stop()

	if !exitStatus.killedByTimeout {
		st, err := process.Status(ctx)
		if err != nil {
			return fmt.Errorf("process status failed: %+v", err)
		}
		b.log.Noticef("process status is: %+v", st)

		status, err := process.Delete(ctx)
		if err != nil {
			return fmt.Errorf("process delete (%+v) failed: %+v", status, err)
		}
	}

	stderrBytes, err := io.ReadAll(&stderrBuf)
	if len(stderrBytes) > 0 {
		return fmt.Errorf("Stderr output was: %s", string(stderrBytes))
	}

	mutexWriter.w = nil

	return nil
}

func (b bpftraceHandler) killProcess(ctx context.Context, process ctrdd.Process) error {
	err := process.Kill(ctx, syscall.SIGTERM)
	if err != nil {
		return fmt.Errorf("timeout reached, killing of process failed: %w", err)
	}
	time.Sleep(time.Second)
	st, err := process.Status(ctx)
	if err != nil {
		return fmt.Errorf("timeout reached, retrieving status of process failed: %w", err)
	}
	if st.Status == ctrdd.Stopped {
		return nil
	}
	err = process.Kill(ctx, syscall.SIGKILL)
	if err != nil {
		return fmt.Errorf("timeout reached, killing of process (SIGKILL) failed: %w", err)
	}

	return nil
}

func (b bpftraceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		fmt.Fprintf(w, `
				<html>
				<form method="post" enctype="multipart/form-data">
                <label>Please choose the aot bpftrace file:
                <input name="aot" type="file" accept="binary/*">
                and the timeout:
                <input name="timeout" min="0" max="3600" value="5" step="5" accept="binary/*">
                </label>
                <br/>
                <button>Start</button>
                </form>
				</html>
			`)
	} else if r.Method == http.MethodPost {
		file, err := os.CreateTemp("/persist/tmp", "bpftrace-aot")
		if err != nil {
			b.log.Warnf("could not create temp dir: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		filename := file.Name()
		defer os.Remove(filename)

		timeoutString := r.FormValue("timeout")
		if timeoutString == "" {
			timeoutString = "5"
		}
		fmt.Fprintf(os.Stderr, "AAAAA timeoutString: %s\n", timeoutString)
		timeoutSeconds, err := strconv.ParseUint(timeoutString, 10, 16)
		if err != nil {
			writeAndLog(b.log, w, fmt.Sprintf("Error happened, could not parse timeout: %s\n", err.Error()))
			w.WriteHeader(http.StatusBadRequest)
		}
		aotForm, _, err := r.FormFile("aot")
		if err != nil {
			writeAndLog(b.log, w, fmt.Sprintf("could not retrieve form file: %s", err))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, err = io.Copy(file, aotForm)
		if err != nil {
			writeAndLog(b.log, w, fmt.Sprintf("could not copy form file: %s", err))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		err = aotForm.Close()
		if err != nil {
			writeAndLog(b.log, w, fmt.Sprintf("could not close form file: %s", err))
		}
		err = file.Close()
		if err != nil {
			writeAndLog(b.log, w, fmt.Sprintf("could not close file: %s", err))
		}

		args := []string{"/usr/bin/bpftrace-aotrt", "-f", "json", filename}
		err = b.runInDebugContainer(w, args, time.Duration(timeoutSeconds)*time.Second)
		if err != nil {
			fmt.Fprintf(w, "Error happened:\n%s\n", err.Error())
			return
		}
	} else {
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func archHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	fmt.Fprint(w, runtime.GOARCH)
}

func linuxkitYmlHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	http.ServeFile(w, r, "/hostfs/etc/linuxkit-eve-config.yml")
}

func listenDebug(log *base.LogObject, stacksDumpFileName, memDumpFileName string) {
	if listenDebugRunning.Swap(true) {
		return
	}

	mux := http.NewServeMux()

	server := &http.Server{
		Addr:              listenAddress,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	info := `
		This server exposes the net/http/pprof API.</br>
		For examples on how to use it, see: <a href="https://pkg.go.dev/net/http/pprof">https://pkg.go.dev/net/http/pprof</a></br>
	    <a href="/debug/pprof/">pprof methods</a></br></br>
	    To create a flamegraph, do: go tool pprof -raw -output=cpu.txt 'http://localhost:6543/debug/pprof/profile?seconds=5';</br>
	    stackcollapse-go.pl cpu.txt | flamegraph.pl --width 4096 > flame.svg</br>
	    (both scripts can be found <a href="https://github.com/brendangregg/FlameGraph">here</a>)<br/><hr/>
	    <a href="/debug/info/arch">architecture info</a></br>
	    <a href="/debug/info/linuxkit.yml">linuxkit yml</a></br></br>
		`

	//	info += fmt.Sprintf("<br/><br/>%s", outputBuffer)

	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		writeOrLog(log, w, info)
	}))
	mux.Handle("/index.html", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		writeOrLog(log, w, info)
	}))

	bpfHandler := bpftraceHandler{
		log: log.Clone(),
	}

	mux.Handle("/debug/info/arch", http.HandlerFunc(archHandler))
	mux.Handle("/debug/info/linuxkit.yml", http.HandlerFunc(linuxkitYmlHandler))
	mux.Handle("/debug/bpftrace", bpfHandler)
	mux.Handle("/debug/pprof/", http.HandlerFunc(pprof.Index))
	mux.Handle("/debug/pprof/cmdline", http.HandlerFunc(pprof.Cmdline))
	mux.Handle("/debug/pprof/profile", http.HandlerFunc(pprof.Profile))
	mux.Handle("/debug/pprof/symbol", http.HandlerFunc(pprof.Symbol))
	mux.Handle("/debug/pprof/trace", http.HandlerFunc(pprof.Trace))
	mux.Handle("/stop", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			server.Close()
			listenDebugRunning.Swap(false)
		} else {
			http.Error(w, "Did you want to use POST method?", http.StatusMethodNotAllowed)
			return
		}
	}))
	mux.Handle("/dump/stacks", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			dumpStacks(log, stacksDumpFileName)
			response := fmt.Sprintf("Stacks can be found in logread or %s\n", stacksDumpFileName)
			writeOrLog(log, w, response)
		} else {
			http.Error(w, "Did you want to use POST method?", http.StatusMethodNotAllowed)
			return
		}
	}))
	mux.Handle("/dump/memory", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			dumpMemoryInfo(log, memDumpFileName)
			response := fmt.Sprintf("Stacks can be found in logread or %s\n", memDumpFileName)
			writeOrLog(log, w, response)
		} else {
			http.Error(w, "Did you want to use POST method?", http.StatusMethodNotAllowed)
			return
		}
	}))

	if err := server.ListenAndServe(); err != nil {
		log.Errorf("Listening failed: %+v", err)
	}
}
