package main

import (
	"context"
	"time"
	"net/url"
	"fmt"
	"bufio"
	"net/http"
	"os"
	"flag"
	"runtime"
	"os/signal"
	"syscall"

	log "github.com/sirupsen/logrus"
	"github.com/jinzhu/configor"
	"github.com/arcsecc/lohpi"
	"github.com/arcsecc/lohpi/core/util"

	"github.com/Azure/azure-storage-blob-go/azblob"
	"github.com/Azure/azure-pipeline-go/pipeline"
)

var config = struct {
	HTTPPort			int 		`default:"9000"`
	PolicyStoreAddr 	string 		`default:"127.0.1.1:8084"`
	MuxAddr				string		`default:"127.0.1.1:8081"`
	LohpiCaAddr    		string 		`default:"127.0.1.1:8301"`
	RemoteBaseURL		string 		`required:"true"`
	RemotePort			string 		`required:"true"`
	AzureKeyVaultName 	string 		`required:"true"`
	AzureKeyVaultSecret	string		`required:"true"`
	AzureClientSecret	string 		`required:"true"`
	AzureClientID		string		`required:"true"`
	AzureKeyVaultBaseURL string		`required:"true"`
	AzureTenantID		string		`required:"true"`
	AzureStorageAccountName	string	`required:"true"`
	AzureStorageAccountKey string	`required:"true"`
}{}

type StorageNode struct {
	node *lohpi.Node
}

func main() {
	var configFile string
	var createNew bool
	var nodeName string

	runtime.GOMAXPROCS(runtime.NumCPU())

	// Logfile and name flags
	args := flag.NewFlagSet("args", flag.ExitOnError)
	args.StringVar(&nodeName, "name", "", "Human-readable identifier of node.")
	args.StringVar(&configFile, "c", "", `Configuration file for the node.`)
	args.BoolVar(&createNew, "new", false, "Initialize new Lohpi node.")
	args.Parse(os.Args[1:])

	configor.New(&configor.Config{Debug: false, ENVPrefix: "PS_NODE"}).Load(&config, configFile)

	if configFile == "" {
		log.Errorln("Configuration file must not be empty. Exiting.")
		os.Exit(2)
	}

	// Require node identifier
	if nodeName == "" {
		log.Errorln("Missing node identifier. Exiting.")
		os.Exit(2)
	}

	var sn *StorageNode
	var err error

	if createNew {
		sn, err = newNodeStorage(nodeName)
		if err != nil {
			log.Errorln(err.Error())
			os.Exit(1)
		}
	} else {
		log.Errorln("Need to set the 'new' flag to true. Exiting.")
		os.Exit(1)
	}
	
	go sn.Start()

	// Wait for SIGTERM signal from the environment
	channel := make(chan os.Signal, 2)
	signal.Notify(channel, os.Interrupt, syscall.SIGTERM)
	<-channel

	// Clean-up
	sn.Shutdown()
	os.Exit(0)
}

func newNodeStorage(name string) (*StorageNode, error) {
	opts, err := getNodeConfiguration(name)
	if err != nil {
		return nil, err
	}

	n, err := lohpi.NewNode(opts...)
	if err != nil {
		panic(err)
		return nil, err
	}

	sn := &StorageNode {
		node: n,
	}

	// TODO: revise the call stack starting from here
	if err := sn.node.JoinNetwork(); err != nil {
		panic(err)
		return nil, err
	}

	return sn, nil
}

// Returns the identifiers of the blobs in the storage account
func getBlobIdentifiers() ([]string, error) {
	ids := make([]string, 0)
	// Create a default request pipeline using your storage account name and account key.
	credential, err := azblob.NewSharedKeyCredential(config.AzureStorageAccountName, config.AzureStorageAccountKey)
	if err != nil {
		return nil, err
	}

	p := azblob.NewPipeline(credential, azblob.PipelineOptions{})

	// From the Azure portal, get your storage account blob service URL endpoint.
	azureURL, _ := url.Parse(fmt.Sprintf("https://%s.blob.core.windows.net", config.AzureStorageAccountName))

	// Create a ContainerURL object that wraps the container URL and a request
	// pipeline to make requests.
	serviceURL := azblob.NewServiceURL(*azureURL, p)
	ctx := context.Background() // This uses a never-expiring context

	// List the container(s)
	for containerMarker := (azblob.Marker{}); containerMarker.NotDone(); {
		listContainer, _ := serviceURL.ListContainersSegment(ctx, containerMarker, azblob.ListContainersSegmentOptions{})

		for _, containerObject := range listContainer.ContainerItems {
			containerName := containerObject.Name
			containerURL, _ := url.Parse(fmt.Sprintf("https://%s.blob.core.windows.net/%s", config.AzureStorageAccountName, containerName))
			containerServiceURL := azblob.NewContainerURL(*containerURL, p)

			// List the blobs in the container
			for blobMarker := (azblob.Marker{}); blobMarker.NotDone(); {
				// Get a result segment starting with the blob indicated by the current Marker.
				listBlob, err := containerServiceURL.ListBlobsFlatSegment(ctx, blobMarker, azblob.ListBlobsSegmentOptions{})
				if err != nil {
					log.Errorln(err.Error())
					continue
				}
				
				// ListBlobs returns the start of the next segment; you MUST use this to get
				// the next segment (after processing the current result segment).
				blobMarker = listBlob.NextMarker

				// Process the blobs returned in this result segment (if the segment is empty, the loop body won't execute)
				for _, blobInfo := range listBlob.Segment.BlobItems {
          		
					ids = append(ids, blobInfo.Name)
				}
			}
		}
		containerMarker = listContainer.NextMarker
	}
	return ids, nil
}

// Implements downloading of data from Azure blob storage.
// TODO: download speed from azure is very slow. We should investigate why this is the case.
func dataHandler(id string, w http.ResponseWriter, r *http.Request) {
	credential, err := azblob.NewSharedKeyCredential(config.AzureStorageAccountName, config.AzureStorageAccountKey)
	if err != nil {
		log.Fatal(err)
	}
	p := azblob.NewPipeline(credential, azblob.PipelineOptions{
		Retry: azblob.RetryOptions{
			TryTimeout:    time.Hour * 3,        // Maximum time allowed for any single try
			MaxTries: 3,
			Policy: azblob.RetryPolicyExponential,
		},
	})

	cURL, err := url.Parse(fmt.Sprintf("https://%s.blob.core.windows.net/lohpicontainer", config.AzureStorageAccountName))
	if err != nil {
		log.Error(err.Error())
		http.Error(w, http.StatusText(http.StatusBadRequest)+": "+err.Error(), http.StatusBadRequest)
		return
	}

	ctx := context.Background() // This example uses a never-expiring context

	// Create an ServiceURL object that wraps the service URL and a request pipeline to making requests.
	containerURL := azblob.NewContainerURL(*cURL, p)

	blobURL := containerURL.NewBlockBlobURL(id)

	// Here's how to read the blob's data with progress reporting:
	get, err := blobURL.Download(ctx, 0, 0, azblob.BlobAccessConditions{}, false, azblob.ClientProvidedKeyOptions{})
	if err != nil {
		log.Fatal(err)
	}

	// Wrap the response body in a ResponseBodyProgress and pass a callback function for progress reporting.
	responseBody := pipeline.NewResponseBodyProgress(get.Body(azblob.RetryReaderOptions{}),
		func(bytesTransferred int64) {
			//fmt.Printf("Read %d of %d bytes.", bytesTransferred, get.ContentLength())
		})

	reader := bufio.NewReader(responseBody)
	defer responseBody.Close() // The client must close the response body when finished with it
	
	// Stream from response to client
	if err := util.StreamToResponseWriter(reader, w, 1000 * 1024); err != nil {
		log.Errorln(err.Error())
		http.Error(w, http.StatusText(http.StatusInternalServerError)+": "+err.Error(), http.StatusInternalServerError)
		return
	}
}

func getNodeConfiguration(name string) ([]lohpi.NodeOption, error) {
	var opts []lohpi.NodeOption

	dbConn, err := getDatabaseConnectionString()
	if err != nil {
		return nil, err
	}

	env := os.Getenv("LOHPI_ENV")
	if env == "" {
		log.Errorln("LOHPI_ENV must be set. Exiting.")
		os.Exit(1)
	} else if env == "production" {
		log.Infoln("Production environment set")
		opts = []lohpi.NodeOption{
			lohpi.NodeWithPostgresSQLConnectionString(dbConn), 
			lohpi.NodeWithMultipleCheckouts(true), 
			lohpi.NodeWithHostName("test.lohpi.cs.uit.no"),
			lohpi.NodeWithHTTPPort(config.HTTPPort),
		}
	} else if env == "development" {
		log.Infoln("Development environment set")
		opts = []lohpi.NodeOption{
			lohpi.NodeWithPostgresSQLConnectionString(dbConn), 
			lohpi.NodeWithMultipleCheckouts(true),
			lohpi.NodeWithHostName("iad09.cs.uit.no"),
			lohpi.NodeWithHTTPPort(config.HTTPPort),
		}
	} else {
		log.Errorln("Unknown value for environment variable LOHPI_ENV:" + env + ". Exiting.")
		os.Exit(1)
	}
	
	// Set name from command line
	opts = append(opts, lohpi.NodeWithName(name))
	
	return opts, nil
}

func getDatabaseConnectionString() (string, error) {
	kvClient, err := newAzureKeyVaultClient()
	if err != nil {
		return "", err
	}

	resp, err := kvClient.GetSecret(config.AzureKeyVaultBaseURL, config.AzureKeyVaultSecret)
	if err != nil {
		return "", err
	}

	return resp.Value, nil
}

func newAzureKeyVaultClient() (*lohpi.AzureKeyVaultClient, error) {
	c := &lohpi.AzureKeyVaultClientConfig{
		AzureKeyVaultClientID:     config.AzureClientID,
		AzureKeyVaultClientSecret: config.AzureClientSecret,
		AzureKeyVaultTenantID:     config.AzureTenantID,
	}

	return lohpi.NewAzureKeyVaultClient(c)
}

func (sn *StorageNode) Start() {
	if err := sn.indexDataset(); err != nil {
		panic(err)
	}

	sn.node.RegisterDatasetHandler(dataHandler)
}

func (sn *StorageNode) indexDataset() error {
	ids, err := getBlobIdentifiers()
	if err != nil {
		return err
	}

	for _, id := range ids {
		if err := sn.node.IndexDataset(id); err != nil {
			return err
		}
	}
	return nil
}

func (s *StorageNode) Shutdown() {

}