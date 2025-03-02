package datasetmanager

import (
	"database/sql"
	"fmt"
	pb "github.com/arcsecc/lohpi/protobuf"
	"github.com/go-redis/redis"
	log "github.com/sirupsen/logrus"
	"github.com/golang/protobuf/proto"
)

// Configuration struct for the dataset manager
type DatasetLookupServiceConfig struct {
	// The database connection string used to back the in-memory data structures.
	// If this is left empty, the in-memory data structures will not be backed by persistent storage.
	// TODO: use timeouts to flush the data structures to the db at regular intervals :))
	// TODO: configure SQL connection pools and timeouts. The timeout values must be sane
	SQLConnectionString string

	RedisClientOptions *redis.Options
}

type DatasetLookupService struct {
	redisClient *redis.Client
	datasetLookupDB *sql.DB
	config *DatasetLookupServiceConfig
	datasetLookupSchema string
	datasetLookupTable string
}

// Returns a new DatasetIndexerService, given the configuration
func NewDatasetLookupService(id string, config *DatasetLookupServiceConfig) (*DatasetLookupService, error) {
	if config == nil {
		return nil, errNilConfig
	}

	if config.SQLConnectionString == "" {
		return nil, errNoConnectionString
	}

	if id == "" {
		id = "default"
	}

	d := &DatasetLookupService{
		config: config,
		datasetLookupSchema: id + "_dataset_lookup_schema",
		datasetLookupTable: id + "_dataset_lookup_table",
	}

	if err := d.createSchema(config.SQLConnectionString); err != nil {
		return nil, err
	}

	if err := d.createDatasetLookupTable(config.SQLConnectionString); err != nil {
		return nil, err
	}

	// Initialize Redis cache. Use Redis only locally
	if config.RedisClientOptions != nil {
		d.redisClient = redis.NewClient(config.RedisClientOptions)
		pong, err := d.redisClient.Ping().Result()
		if err != nil {
			return nil, err
		}
		
		if pong != "PONG" {
			return nil, fmt.Errorf("Value of Redis pong was wrong")
		}

		if err := d.flushAll(); err != nil {
			return nil, err
		}

		//errc := d.reloadRedis()
		/*if err := <-errc; err != nil {
			return nil, err
		}*/
	}

	return d, nil
}

func (d *DatasetLookupService) DatasetNode(datasetId string) *pb.Node {
	if d.redisClient != nil {
		node, err := d.cacheDatasetNode(datasetId)
		if node != nil && err == nil {
			return node
		}

		if err != nil {
			log.Error(err.Error())
		}
	}
	return d.dbSelectDatasetNode(datasetId)
}

func (d *DatasetLookupService) cacheDatasetNode(datasetId string) (*pb.Node, error) {
	cmd := d.redisClient.MGet(datasetId)
	if cmd.Err() != nil {
		return nil, cmd.Err()
	}

	nodeBytes, err := cmd.Result()
	if err != nil {
		return nil, err
	}
	
	if len(nodeBytes) > 0 {
		if nodeBytes[0] != nil {
			node := &pb.Node{}
			if err := proto.Unmarshal([]byte(nodeBytes[0].(string)), node); err != nil {
				return nil, err
			}
			return node, nil
		}
	}
	
	return nil, fmt.Errorf("Node was not found in cache")
}

func (d *DatasetLookupService) InsertDatasetNode(datasetId string, node *pb.Node) error {
	if d.redisClient != nil {
		if err := d.cacheInsertDatasetNode(datasetId, node); err != nil {
			log.Error(err.Error())
		}
	}

	return d.dbInsertDatasetNode(datasetId, node)
}

func (d *DatasetLookupService) cacheInsertDatasetNode(datasetId string, node *pb.Node) error {
	log.Println("Inserting into cache!")
	nodeBytes, err := proto.Marshal(node)
	if err != nil {
		return err
	}
	
	return d.redisClient.Set(datasetId, nodeBytes, 0).Err()
}

// TODO: return errors from db interface as well
func (d *DatasetLookupService) DatasetNodeExists(datasetId string) bool {
	if datasetId == "" {
		err := fmt.Errorf("Dataset identifier must not be empty")
		log.Error(err.Error())
		return false
	}

	if d.redisClient != nil {
		exists, err := d.cacheDatasetNodeExists(datasetId)
		if exists && err == nil {
			return exists
		}

		if err != nil {
			log.Error(err.Error())
		}
	}

	// If there was a cache miss but a hit in PSQL, insert it into Redis
	exists := d.dbDatasetNodeExists(datasetId)
	if exists && d.redisClient != nil {
		node := d.dbSelectDatasetNode(datasetId)
		if node != nil {
			nodeBytes, err := proto.Marshal(node)
			if err != nil {
				log.Error(err.Error())
				return exists
			}

			if err := d.redisClient.Set(datasetId, nodeBytes, 0).Err(); err != nil {
				log.Error(err.Error())
				return exists
			}
		}
	}

	return exists
}

func (d *DatasetLookupService) cacheDatasetNodeExists(datasetId string) (bool, error) {
	cmd := d.redisClient.Exists(datasetId)
	if cmd.Err() != nil {
		return false, cmd.Err()
	}

	r, err := cmd.Result()
	if err != nil {
		return false, cmd.Err()
	}

	if r == 1 {
		return true, nil
	} else {
		return false, nil
	}
}

func (d *DatasetLookupService) RemoveDatasetNode(datasetId string) error {
	if d.redisClient != nil {
		if err := d.cacheRemoveDatasetNode(datasetId); err != nil {
			log.Error(err.Error())
		}
	}

	return d.dbRemoveDatasetNode(datasetId)
}

func (d *DatasetLookupService) cacheRemoveDatasetNode(datasetId string) error {
	cmd := d.redisClient.Del(datasetId)
	if cmd.Err() != nil {
		return cmd.Err()
	}

	r, err := cmd.Result()
	if err != nil {
		return cmd.Err()
	}

	if r == 1 {
		return nil
	} else {
		return fmt.Errorf("Dataset node with identifier '%s' was not found", datasetId)
	}

	return nil
}

// TODO: add ranges
func (d *DatasetLookupService) cacheDatasetIdentifiers() ([]string, error) {
	ids := make([]string, 0)
	iter := d.redisClient.Scan(0, "*", 0).Iterator()
	for iter.Next() {
		ids = append(ids, iter.Val())
	}
	
	if err := iter.Err(); err != nil {
	    return nil, err
	}
	
	return ids, nil
}

func (d *DatasetLookupService) DatasetIdentifiers() []string {
	if d.redisClient != nil {
		ids, err := d.cacheDatasetIdentifiers()
		if ids != nil && err == nil {
			return ids
		}

		if err != nil {
			log.Error(err.Error)
		}
	}

	return d.dbSelectDatasetIdentifiers()
}

func (d *DatasetLookupService) flushAll() error {
	return d.redisClient.FlushAll().Err()
}

func (d *DatasetLookupService) reloadRedis() chan error {
	errc := make(chan error, 1)
	
	go func() {
		// TODO: don't load everyting into memory!
		maps, err := d.dbGetAllDatasetNodes()
		if err != nil {
			errc <- err
			return
		}

		ifaces := make([]interface{}, 0)
		pipe := d.redisClient.TxPipeline()
		for k, v := range maps {
			marshalled, err := proto.Marshal(v)
			if err != nil {
				log.Error(err.Error())
				continue
			}

			ifaces = append(ifaces, k, marshalled)
		}

		if len(ifaces) > 0 {
			if err := d.redisClient.MSet(ifaces...).Err(); err != nil {
				errc <- err
				return
			}
		
			if _, err := pipe.Exec(); err != nil {
				errc <- err
				return
			}
		}

		errc <- nil
	}()

	return errc
}
