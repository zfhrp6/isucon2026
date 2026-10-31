//go:build diagnostic_pprof

package isureal

import (
	"log"
	"net"
	"net/http"
	"net/http/pprof"
)

func init() {
	listener, err := net.Listen("tcp", "127.0.0.1:6060")
	if err != nil {
		log.Fatalf("start diagnostic pprof listener: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	for _, name := range []string{"allocs", "block", "goroutine", "heap", "mutex", "threadcreate"} {
		mux.Handle("/debug/pprof/"+name, pprof.Handler(name))
	}

	go func() {
		if err := http.Serve(listener, mux); err != nil {
			log.Fatalf("diagnostic pprof server stopped: %v", err)
		}
	}()
}
