package mux

import (
	"context"
	"fmt"
	"strconv"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
)

func (m *Mux) startHttpServer() error {
	r := mux.NewRouter()
	log.Printf("MUX: Started HTTP server on port %d\n", m.config.MuxHttpPort)

	// Main dataset router
	dRouter := r.PathPrefix("/dataset").Subrouter().SkipClean(true)
	dRouter.HandleFunc("/ids", m.getNetworkDatasetIdentifiers).Methods("GET")
	dRouter.HandleFunc("/metadata/{id:.*}", m.getDatasetMetadata).Methods("GET")
	dRouter.HandleFunc("/data/{id:.*}", m.getDataset).Methods("GET")

	m.httpServer = &http.Server{
		Handler:      r,
		WriteTimeout: time.Second * 30,
		ReadTimeout:  time.Second * 30,
		IdleTimeout:  time.Second * 60,
	}

	err := m.httpServer.Serve(m.httpListener)
	if err != nil {
		log.Errorln(err)
		return err
	}
	return nil
}

func (m *Mux) shutdownHttpServer() {
	// The duration for which the server wait for open connections to finish
	wait := time.Minute * 1
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()

	// Doesn't block if no connections, but will otherwise wait
	// until the timeout deadline.
	m.httpServer.Shutdown(ctx)

	// Optionally, you could run srv.Shutdown in a goroutine and block on
	// <-ctx.Done() if your application should wait for other services
	// to finalize based on context cancellation.

	log.Println("Gracefully shutting down HTTP server")
}

// Lazily fetch objects from all the nodes
func (m *Mux) getNetworkDatasetIdentifiers(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	ctx, cancel := context.WithDeadline(r.Context(), time.Now().Add(time.Second * 10))
	defer cancel()

	errChan := make(chan error)
	setsChan := make(chan []byte)
	defer close(setsChan)

	go func() {
		defer close(errChan)
		r = r.WithContext(ctx)
		sets, err := m.datasetIdentifiers(ctx)
		if err != nil {
			errChan <-err
			return
		}

		setsChan <-sets
	}()

	select {
	case <-ctx.Done():
		http.Error(w, http.StatusText(http.StatusRequestTimeout), http.StatusRequestTimeout)
		return
	case err := <-errChan:
		log.Errorln(err)
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	case sets := <-setsChan:
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		w.Write(sets)
		return
	}
}

// Fetches the information about a dataset
func (m *Mux) getDatasetMetadata(w http.ResponseWriter, req *http.Request) {
	dataset := mux.Vars(req)["id"]
	if dataset == "" {
		errMsg := fmt.Errorf("Missing project identifier")
		http.Error(w, http.StatusText(http.StatusBadRequest)+": " + errMsg.Error(), http.StatusBadRequest)
		return
	}
	
/*	ctx, cancel := context.WithDeadline(req.Context(), time.Now().Add(time.Second * 5))
	defer cancel()
	req = req.WithContext(ctx)*/

	md, err := m.datasetMetadata(w, req, dataset, nil)
	if err != nil {
		log.Errorln(err)
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
	}

	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	w.Write(md)
}

// Handler used to fetch an entire dataset. Writes a zip file to the client
func (m *Mux) getDataset(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	
	dataset := mux.Vars(req)["id"]
	if dataset == "" {
		errMsg := fmt.Errorf("Missing storage identifier.")
		http.Error(w, http.StatusText(http.StatusBadRequest)+": " + errMsg.Error(), http.StatusBadRequest)
		return
	}

	//ctx, cancel := context.WithDeadline(req.Context(), time.Now().Add(time.Second * 5))
	/*defer cancel()
	req = req.WithContext(ctx)*/

	archiveBytes, err := m.dataset(w, req, dataset, nil)
	if err != nil {
		log.Println(err.Error())
	}

	w.WriteHeader(http.StatusOK)
	req.Header.Add("Content-Length", strconv.Itoa(len(archiveBytes)))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.zip\"", dataset))
	w.Write(archiveBytes)
}
