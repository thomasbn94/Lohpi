package node

import (
	"bytes"
	"bufio"
	"io"
	"strings"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/arcsecc/lohpi/core/comm"
	"github.com/arcsecc/lohpi/core/util"
	"github.com/gorilla/mux"
	"github.com/rs/cors"
	log "github.com/sirupsen/logrus"
	"net/http"
	"strconv"
	"time"
)

const PROJECTS_DIRECTORY = "projects"

func (n *NodeCore) startHTTPServer(addr string) error {
	router := mux.NewRouter()
	log.Infof("%s: Started HTTP server on port %d\n", n.config().Name, n.config().HTTPPort)

	dRouter := router.PathPrefix("/dataset").Schemes("HTTP").Subrouter()
	dRouter.HandleFunc("/ids", n.getDatasetIdentifiers).Methods("GET")
	dRouter.HandleFunc("/info/{id:.*}", n.getDatasetSummary).Methods("GET")
	dRouter.HandleFunc("/new_policy/{id:.*}", n.setDatasetPolicy).Methods("PUT")
	dRouter.HandleFunc("/data/{id:.*}", n.getDataset).Methods("GET")
	dRouter.HandleFunc("/metadata/{id:.*}", n.getMetadata).Methods("GET")

	// Middlewares used for validation
	//dRouter.Use(n.middlewareValidateTokenSignature)
	//dRouter.Use(n.middlewareValidateTokenClaims)

	handler := cors.AllowAll().Handler(router)

	n.httpServer = &http.Server{
		Addr:         addr,
		Handler:      handler,
		WriteTimeout: time.Second * 30,
		ReadTimeout:  time.Second * 30,
		IdleTimeout:  time.Second * 60,
		TLSConfig:    comm.ServerConfig(n.cu.Certificate(), n.cu.CaCertificate(), n.cu.Priv()),
	}

	/*if err := m.setPublicKeyCache(); err != nil {
		log.Errorln(err.Error())
		return err
	}*/

	return n.httpServer.ListenAndServe()
}

// TODO redirect HTTP to HTTPS
func redirectTLS(w http.ResponseWriter, r *http.Request) {
    //http.Redirect(w, r, "https://IPAddr:443"+r.RequestURI, http.StatusMovedPermanently)
}

/*func (n *NodeCore) setPublicKeyCache() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.ar = jwk.NewAutoRefresh(ctx)
	const msCerts = "https://login.microsoftonline.com/common/discovery/v2.0/keys" // TODO: config me

	m.ar.Configure(msCerts, jwk.WithRefreshInterval(time.Minute * 5))

	// Keep the cache warm
	_, err := m.ar.Refresh(ctx, msCerts)
	if err != nil {
		log.Println("Failed to refresh Microsoft Azure JWKS")
		return err
	}
	return nil
}*/

func (n *NodeCore) getMetadata(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	datasetId := strings.Split(r.URL.Path, "/dataset/metadata/")[1]

	if !n.dbDatasetExists(datasetId) {
		err := fmt.Errorf("Dataset '%s' is not indexed by the server", datasetId)
		log.Infoln(err.Error())
		http.Error(w, http.StatusText(http.StatusNotFound)+": "+err.Error(), http.StatusNotFound)
		return
	}

	dataset := n.getDatasetMap()[datasetId]
	if dataset == nil {
		err := errors.New("Dataset is nil")
		http.Error(w, http.StatusText(http.StatusBadRequest)+": "+err.Error(), http.StatusBadRequest)
		return
	}

	if dataset.GetMetadataURL() == "" {
		err := errors.New("Could not fetch metadata URL")
		log.Error(err.Error())
		http.Error(w, http.StatusText(http.StatusBadRequest)+": "+err.Error(), http.StatusBadRequest)
		return
	}

	request, err := http.NewRequest("GET", dataset.GetMetadataURL(), nil)
	if err != nil {
		log.Error(err.Error())
		http.Error(w, http.StatusText(http.StatusBadRequest)+": "+err.Error(), http.StatusBadRequest)
		return
	}

	httpClient := &http.Client{
		Timeout: time.Duration(20 * time.Second),
	}

	response, err := httpClient.Do(request)
	if err != nil {
		log.Error(err.Error())
		http.Error(w, http.StatusText(http.StatusBadRequest)+": "+err.Error(), http.StatusBadRequest)
		return
	}

	defer response.Body.Close()
	
	if response.StatusCode != http.StatusOK {
		log.Errorf("Response from remote data repository\n")
		http.Error(w, http.StatusText(http.StatusInternalServerError) + ": " + "Could not fetch metadata from host.", http.StatusInternalServerError)
		return
	}

	bufferedReader := bufio.NewReader(response.Body)
    buffer := make([]byte, 4 * 1024)

	m := copyHeaders(response.Header)
	setHeaders(m, w.Header())
	w.WriteHeader(response.StatusCode)

	for {
    	len, err := bufferedReader.Read(buffer)
        if len > 0 {	
			_, err = w.Write(buffer[:len])
			if err != nil {
				log.Error(err.Error())
			}
		}

        if err != nil {
            if err == io.EOF {
                log.Infoln(err.Error())
            } else {
				log.Error(err.Error())	
				http.Error(w, http.StatusText(http.StatusInternalServerError) + ": " + err.Error(), http.StatusInternalServerError)
				return
			}
            break
        }
    }
}

func copyHeaders(h map[string][]string) map[string][]string {
	m := make(map[string][]string)
	for key, val := range h {
		m[key] = val
	}
	return m
}

func setHeaders(src, dest map[string][]string) {
	for k, v := range src {
		dest[k] = v
	}
}

func getBearerToken(r *http.Request) ([]byte, error) {
	authHeader := r.Header.Get("Authorization")
	authHeaderContent := strings.Split(authHeader, " ")
	if len(authHeaderContent) != 2 {
		return nil, errors.New("Token not provided or malformed")
	}
	return []byte(authHeaderContent[1]), nil
}

// TODO use context!
func (n *NodeCore) getDataset(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	// proofcheck me
	datasetId := strings.Split(r.URL.Path, "/dataset/data/")[1]

	if !n.dbDatasetExists(datasetId) {
		err := fmt.Errorf("Dataset '%s' is not indexed by the server", datasetId)
		log.Infoln(err.Error())
		http.Error(w, http.StatusText(http.StatusNotFound)+": "+err.Error(), http.StatusNotFound)
		return
	}

	dataset := n.getDatasetMap()[datasetId]
	if dataset == nil {
		err := errors.New("Dataset is nil")
		log.Infoln(err.Error())
		http.Error(w, http.StatusText(http.StatusBadRequest)+": "+err.Error(), http.StatusBadRequest)
		return
	}

	if !n.dbDatasetIsAvailable(datasetId) {
		err := errors.New("You do not have permission to access this dataset")
		log.Infoln(err.Error())
		http.Error(w, http.StatusText(http.StatusUnauthorized)+": "+err.Error(), http.StatusUnauthorized)
		return
	}

	// if client is allowed... 401 if not
	// If multiple checkouts are allowed, check if the client has checked it out already
	if !n.config().AllowMultipleCheckouts && n.dbDatasetIsCheckedOutByClient(datasetId) {
		err := errors.New("You have already checked out this dataset")
		log.Infoln(err.Error())
		http.Error(w, http.StatusText(http.StatusUnauthorized)+": "+err.Error(), http.StatusUnauthorized)
		return
	}

	token, err := getBearerToken(r)
	if err != nil {
		log.Infoln(err.Error())
		http.Error(w, http.StatusText(http.StatusBadRequest)+": "+err.Error(), http.StatusBadRequest)
		return
	}

	// Add dataset checkout to database. Rollback checkout if anything fails
	if err := n.dbCheckoutDataset(string(token), datasetId); err != nil {
		log.Error(err.Error())
		http.Error(w, http.StatusText(http.StatusInternalServerError)+": "+err.Error(), http.StatusInternalServerError)
		return
	}

	if dataset.GetDatasetURL() == "" {
		err := errors.New("Could not fetch dataset URL")
		log.Error(err.Error())
		http.Error(w, http.StatusText(http.StatusBadRequest)+": "+err.Error(), http.StatusBadRequest)
		return
	}

	request, err := http.NewRequest("GET", dataset.GetDatasetURL(), nil)
	if err != nil {
		log.Error(err.Error())
		http.Error(w, http.StatusText(http.StatusBadRequest)+": "+err.Error(), http.StatusBadRequest)
		return
	}

	httpClient := &http.Client{
		Timeout: time.Duration(20 * time.Second),
	}

	response, err := httpClient.Do(request)
	if err != nil {
		log.Error(err.Error())
		http.Error(w, http.StatusText(http.StatusBadRequest)+": "+err.Error(), http.StatusBadRequest)
		return
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		log.Errorf("Response from remote data repository\n")
		http.Error(w, http.StatusText(http.StatusInternalServerError) + ": " + "Could not fetch dataset from host.", http.StatusInternalServerError)
		return
	}

	bufferedReader := bufio.NewReader(response.Body)
    buffer := make([]byte, 4 * 1024)

	m := copyHeaders(response.Header)
	setHeaders(m, w.Header())
	w.WriteHeader(response.StatusCode)

	for {
    	len, err := bufferedReader.Read(buffer)
        if len > 0 {	
			_, err = w.Write(buffer[:len])
			if err != nil {
				log.Error(err.Error())
			}
		}

        if err != nil {
            if err == io.EOF {
                log.Infoln(err.Error())
            } else {
				log.Error(err.Error())	
				http.Error(w, http.StatusText(http.StatusInternalServerError) + ": " + err.Error(), http.StatusInternalServerError)
				return
			}
            break
        }
    }
}

// Returns the dataset identifiers stored at this node
func (n *NodeCore) getDatasetIdentifiers(w http.ResponseWriter, r *http.Request) {
	log.Infoln("Got request to", r.URL.String())
	defer r.Body.Close()

	ids := n.datasetIdentifiers()
	b := new(bytes.Buffer)
	if err := json.NewEncoder(b).Encode(ids); err != nil {
		log.Errorln(err.Error())
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Header().Add("Content-Type", "application/json")
	w.Header().Add("Content-Length", strconv.Itoa(len(ids)))

	_, err := w.Write(b.Bytes())
	if err != nil {
		log.Errorln(err.Error())
		http.Error(w, http.StatusText(http.StatusInternalServerError)+": "+err.Error(), http.StatusInternalServerError)
		return
	}
}

// Returns a JSON object containing the metadata assoicated with a dataset
func (n *NodeCore) getDatasetSummary(w http.ResponseWriter, r *http.Request) {
	log.Infoln("Got request to", r.URL.String())
	defer r.Body.Close()

	dataset := mux.Vars(r)["id"]
	if dataset == "" {
		err := errors.New("Missing dataset identifier")
		log.Infoln(err.Error())
		http.Error(w, http.StatusText(http.StatusBadRequest)+": "+err.Error(), http.StatusBadRequest)
		return
	}

	if !n.dbDatasetExists(dataset) {
		err := fmt.Errorf("Dataset '%s' is not indexed by the server", dataset)
		log.Infoln(err.Error())
		http.Error(w, http.StatusText(http.StatusNotFound)+": "+err.Error(), http.StatusNotFound)
		return
	}

	// Fetch the policy
	policy, err := n.dbGetObjectPolicy(dataset)
	if err != nil {
		log.Errorln(err.Error())
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	// Fetch the clients that have checked out the dataset
	checkouts, err := n.dbGetCheckoutList(dataset)
	if err != nil {
		log.Errorln(err.Error())
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	// Destination struct
	resp := struct {
		Dataset   string
		Policy    string
		Checkouts []CheckoutInfo
	}{
		Dataset:   dataset,
		Policy:    policy,
		Checkouts: checkouts,
	}

	b := new(bytes.Buffer)
	if err := json.NewEncoder(b).Encode(resp); err != nil {
		log.Errorln(err.Error())
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	r.Header.Add("Content-Length", strconv.Itoa(len(b.Bytes())))

	_, err = w.Write(b.Bytes())
	if err != nil {
		log.Errorln(err.Error())
		http.Error(w, http.StatusText(http.StatusInternalServerError)+": "+err.Error(), http.StatusInternalServerError)
		return
	}
}

/* Assigns a new policy to the dataset. The request body must be a JSON object with a string similar to this:
 *{
 *	"policy": true/false
 *}
 */
// TODO: restrict this function only to be used to set the initial dataset policy.
// SHOULD NOT BE USED
func (n *NodeCore) setDatasetPolicy(w http.ResponseWriter, r *http.Request) {
	log.Infoln("Got request to", r.URL.String())
	defer r.Body.Close()

	// Enforce application/json MIME type
	if r.Header.Get("Content-Type") != "application/json" {
		msg := "Content-Type header is not application/json"
		http.Error(w, http.StatusText(http.StatusUnsupportedMediaType)+": "+msg, http.StatusUnsupportedMediaType)
		return
	}

	// Fetch the dataset if
	dataset := mux.Vars(r)["id"]
	if dataset == "" {
		err := errors.New("Missing dataset identifier")
		log.Infoln(err.Error())
		http.Error(w, http.StatusText(http.StatusBadRequest)+": "+err.Error(), http.StatusBadRequest)
		return
	}

	// Check if dataset exists in the external repository
	if !n.datasetExists(dataset) {
		err := fmt.Errorf("Dataset '%s' is not stored in this node", dataset)
		log.Infoln(err.Error())
		http.Error(w, http.StatusText(http.StatusNotFound)+": "+err.Error(), http.StatusNotFound)
		return
	}

	// Destination struct
	reqBody := struct {
		Policy string
	}{}

	if err := util.DecodeJSONBody(w, r, "application/json", &reqBody); err != nil {
		var e *util.MalformedParserReponse
		if errors.As(err, &e) {
			log.Errorln(err.Error())
			http.Error(w, e.Msg, e.Status)
		} else {
			log.Errorln(err.Error())
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
		return
	}

	if err := n.dbSetObjectPolicy(dataset, reqBody.Policy); err != nil {
		log.Warnln(err.Error())
	}

	respMsg := "Successfully set a new policy for " + dataset + "\n"
	w.WriteHeader(http.StatusCreated)
	w.Header().Set("Content-Type", "application/text")
	w.Header().Set("Content-Length", strconv.Itoa(len(respMsg)))
	w.Write([]byte(respMsg))
}
