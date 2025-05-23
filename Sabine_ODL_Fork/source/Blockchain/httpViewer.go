package Blockchain

import (
	"encoding/json"
	"github.com/gorilla/mux"
	"github.com/rs/zerolog/log"
	"io"
	"net/http"
	"time"
)

func HttpViewer(mux http.Handler, port string) *http.Server {
	s := &http.Server{
		Addr:           ":" + port,
		Handler:        mux,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	channel := make(chan struct{})
	finish := false
	go func() {
		if err := s.ListenAndServe(); err != nil && !finish {
			log.Info().Msg("Port already open")
			channel <- struct{}{}
		}
	}()
	select {
	case <-channel:
		return nil
	case <-time.After(10 * time.Millisecond):
		close(channel)
		log.Info().Msgf("Start the HTTP server on port %s", s.Addr)
		finish = true
		return s
	}
}

func HttpBlockchainViewer(blockchain *Blockchain, port string) *http.Server {
	muxRouter := makeMuxBlockchainRouter(blockchain)
	return HttpViewer(muxRouter, port)
}

func HttpMetricViewer(handler *MetricHandler, port string) *http.Server {
	muxRouter := makeMuxMetricRouter(handler)
	return HttpViewer(muxRouter, port)
}

func makeMuxBlockchainRouter(blockchain *Blockchain) http.Handler {
	muxRouter := mux.NewRouter()
	muxRouter.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		bytes, err := json.MarshalIndent(*blockchain.GetBlocs(), "", "  ")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, err = io.WriteString(w, string(bytes))
		check(err)
	}).Methods("GET")
	return muxRouter
}

func makeMuxMetricRouter(handler *MetricHandler) http.Handler {
	muxRouter := mux.NewRouter()
	muxRouter.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		metric := handler.GetMetrics()
		bytes, err := json.MarshalIndent(metric, "", "  ")
		if err != nil {
			log.Error().Msgf("Error with the Marshal : %s", err.Error())
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		_, err = io.WriteString(writer, string(bytes))
		check(err)
	})
	return muxRouter
}
