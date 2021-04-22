package lohpi

import (
	"github.com/pkg/errors"
	"github.com/arcsecc/lohpi/core/node"
	"time"
)

var (
	errNoDataset = errors.New("Dataset is nil.")
)

// Defines functional options for the node.
type NodeOption func(*Node)

type Node struct {
	nodeCore *node.NodeCore
	conf *node.Config
}

type Dataset struct {
	DatasetURL string
	MetadataURL string
}

// Sets the HTTP port of the node that
func NodeWithHTTPSPort(port int) NodeOption {
	return func(n *Node) {
		n.conf.HTTPSPort = port
	}
}

// Sets the CA's address and port number. The default values are "127.0.0.1" and 8301, respectively.
func NodeWithLohpiCaConnectionString(addr string, port int) NodeOption {
	return func(n *Node) {
		n.conf.LohpiCaAddress = addr
		n.conf.LohpiCaPort = port
	}
}

// Sets the host:port pair of the policy store. Default value is "".
func NodeWithPolicyStoreConnectionString(addr string, port int) NodeOption {
	return func(n *Node) {
		n.conf.PolicyStoreAddress = addr
		n.conf.PolicyStoreGRPCPport = port
	}
}

// Sets the host:port pair of the directory server. Default value is "".
func NodeWithDirectoryServerConnectionString(addr string, port int) NodeOption {
	return func(n *Node) {
		n.conf.DirectoryServerAddress = addr
		n.conf.DirectoryServerGPRCPort = port
	}
}

// Sets the name of the node.
func NodeWithName(name string) NodeOption {
	return func(n *Node) {
		n.conf.Name = name
	}
}

// Sets the connection string to the database that stores the policies.
func NodeWithPostgresSQLConnectionString(s string) NodeOption {
	return func(n *Node) {
		n.conf.PostgresSQLConnectionString = s
	}
}

// Sets the backup retention time to d. At each d, the in-memory caches are flushed
// to the database. If set to 0, flushing never occurs. 
func NodeWithBackupRetentionTime(t time.Duration) NodeOption {
	return func(n *Node) {
		n.conf.DatabaseRetentionInterval = t
	}
}

// If set to true, a client can checkout a dataset multiple times. Default is false.
func NodeWithMultipleCheckouts(multiple bool) NodeOption {
	return func(n *Node) {
		n.conf.AllowMultipleCheckouts = multiple
	}
}

// If set to true, verbose logging is enabled. Default is false.
func NodeWithDebugEnabled(enabled bool) NodeOption {
	return func(n *Node) {
		n.conf.DebugEnabled = enabled
	}
}

// If set to true, TLS is enabled on the HTTP connection between the node and the clients connecting to 
// the HTTP server. Default is false.
func NodeWithTLS(enabled bool) NodeOption {
	return func(n *Node) {
		n.conf.TLSEnabled = enabled
	}
}

// Sets the hostname of this node. Default value is 127.0.1.1.
func NodeWithHostName(hostName string) NodeOption {
	return func(n *Node) {
		n.conf.HostName = hostName
	}
}

// Applies the options to the node.
// NOTE: no locking is performed. Beware of undefined behaviour. Check that previous connections are still valid.
// SHOULD NOT be called.
func (n *Node) ApplyConfigurations(opts ...NodeOption) {
	for _, opt := range opts {
		opt(n)
	}
}

func NewNode(opts ...NodeOption) (*Node, error) {
	const (
		defaultHTTPSPort = 8091
		defaultPolicyStoreAddress = "127.0.1.1"
		defaultPolicyStoreGRPCPport = 8084
		defaultDirectoryServerAddress = "127.0.1.1"
		defaultDirectoryServerGPRCPort = 8081
		defaultLohpiCaAddress = "127.0.1.1"
		defaultLohpiCaPort = 8301
		defaultName = "Node identifier"
		defaultPostgresSQLConnectionString = ""
		defaultDatabaseRetentionInterval = time.Duration(0)	// A LOT MORE TO DO HERE
		defaultAllowMultipleCheckouts = false
		defaultHostName = "127.0.1.1"
	)

	// Default configuration
	conf := &node.Config{
		HostName: defaultHostName,
		HTTPSPort: defaultHTTPSPort,
		PolicyStoreAddress: defaultPolicyStoreAddress,
		PolicyStoreGRPCPport: defaultPolicyStoreGRPCPport,
		DirectoryServerAddress: defaultDirectoryServerAddress,
		DirectoryServerGPRCPort: defaultDirectoryServerGPRCPort,
		LohpiCaAddress: defaultLohpiCaAddress,
		LohpiCaPort: defaultLohpiCaPort,
		Name: defaultName,
		PostgresSQLConnectionString: defaultPostgresSQLConnectionString,
		DatabaseRetentionInterval: defaultDatabaseRetentionInterval,
		AllowMultipleCheckouts: defaultAllowMultipleCheckouts,
	}

	n := &Node{
		conf: conf,
	}

	// Apply the configuration to the higher-level node
	for _, opt := range opts {
		opt(n)
	}

	nCore, err := node.NewNodeCore(conf)
	if err != nil {
		return nil, err
	}

	// Connect the lower-level node to this node
	n.nodeCore = nCore

	return n, nil
}

// IndexDataset requests a policy from policy store. The policies are stored in this node and can be retrieved 
// (see func (n *Node) GetPolicy(id string) bool). The policies are eventually available the API when the policy store
// has processsed the request. 
func (n *Node) IndexDataset(datasetId string, d *Dataset) error {
	if d == nil {
		return errNoDataset
	}

	return n.nodeCore.IndexDataset(datasetId, d.DatasetURL, d.MetadataURL)
}

// Removes the dataset policy from the node. The dataset will no longer be available to clients.
func (n *Node) RemoveDataset(id string) error {
	if id == "" {
		return errNoDataset
	}
	
	return n.nodeCore.RemoveDataset(id)
}

// Shuts down the node
func (n *Node) Shutdown() {
	n.nodeCore.Shutdown()
}

// Joins the network by starting the underlying Ifrit node. It performs handshakes
// with the policy store and multiplexer at known addresses. In addition, the HTTP server will start as well.
func (n *Node) JoinNetwork() error {
	return n.nodeCore.JoinNetwork()
}

// Returns the underlying Ifrit address.
func (n *Node) IfritAddress() string {
	return n.nodeCore.IfritAddress()
}

// Returns the string representation of the node.
func (n *Node) String() string {
	return ""
}