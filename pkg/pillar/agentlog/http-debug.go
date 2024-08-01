package agentlog

import (
	"fmt"
	"io"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime"
	"sort"
	"sync/atomic"
	"time"

	"github.com/lf-edge/eve/pkg/pillar/base"
)

var listenDebugRunning atomic.Bool

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

func listenDebug(log *base.LogObject, stacksDumpFileName, memDumpFileName string) {
	if listenDebugRunning.Swap(true) {
		return
	}

	mux := http.NewServeMux()

	server := &http.Server{
		Addr:              "localhost:6543",
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	info := `
		This server exposes the net/http/pprof API.</br>
		For examples on how to use it, see: <a href="https://pkg.go.dev/net/http/pprof">https://pkg.go.dev/net/http/pprof</a></br>
	    <a href="debug/pprof/">pprof methods</a></br></br>
	    To create a flamegraph, do: go tool pprof -raw -output=cpu.txt 'http://localhost:6543/debug/pprof/profile?seconds=5';</br>
	    stackcollapse-go.pl cpu.txt | flamegraph.pl --width 4096 > flame.svg</br>
	    (both scripts can be found <a href="https://github.com/brendangregg/FlameGraph">here</a>)
		`

	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		writeOrLog(log, w, info)
	}))
	mux.Handle("/index.html", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		writeOrLog(log, w, info)
	}))

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
