package membershipmanager

import (
	"errors"
	"context"
	pb "github.com/arcsecc/lohpi/protobuf"
	"time"
	"fmt"
	"github.com/jackc/pgx/v4"
	pbtime "google.golang.org/protobuf/types/known/timestamppb"
	"github.com/sirupsen/logrus"
)

var (
	errNoNodeId = errors.New("Node's name is not set")
	errNilNode = errors.New("Node is nil")
	errNoRegexString = errors.New("Regex string is empty")
)

var dbLogFields = logrus.Fields{
	"package": "membershipmanager",
	"action": "database client",
}

var regexStringLogFields = logrus.Fields{
	"package": "membershipmanager",
	"action": "parsing regular expressions",
}

func (m *MembershipManagerUnit) dbInsertNetworkNode(nodeId string, node *pb.Node) error {
	if nodeId == "" {
		return errNoNodeId
	}

	if node == nil {
		return errNilNode
	}

	boottime := node.GetBootTime().AsTime().Format(time.RFC1123Z)

	q := `INSERT INTO ` + m.storageNodeSchema + `.` + m.storageNodeTable + `
	(node_name, ip_address, public_id, https_address, port, boottime) VALUES ($1, $2, $3, $4, $5, $6)
	ON CONFLICT (node_name) DO UPDATE
	SET
		node_name = $1, 
		ip_address = $2, 
		public_id = $3, 
		https_address = $4, 
		port = $5, 
		boottime = $6
	WHERE ` + m.storageNodeSchema + `.` + m.storageNodeTable + `.node_name = $1;`
	_, err := m.pool.Exec(context.Background(), q, 
		nodeId,
		node.GetIfritAddress(), 
		node.GetId(),
		node.GetHttpsAddress(), 
		node.GetPort(), 
		boottime)
	if err != nil {
		log.WithFields(dbLogFields).Error(err.Error())
		return err
	}

	log.WithFields(dbLogFields).Infof("Succsessfully inserted node with id '%s'\n'", nodeId)

	return nil
}

func (m *MembershipManagerUnit) dbDeleteNetworkNode(nodeId string) error {
	if nodeId == "" {
		return fmt.Errorf("Node id is empty")
	}

	q := `DELETE FROM ` + m.storageNodeSchema + `.` + m.storageNodeTable + ` WHERE node_name = $1;`
	commangTag, err := m.pool.Exec(context.Background(), q, nodeId)
	if err != nil {
		log.WithFields(dbLogFields).Error(err.Error())
		return err
	}

	if commangTag.RowsAffected() != 1 {
		err := fmt.Errorf("Could not delete node with id '%s'", nodeId)
		log.WithFields(dbLogFields).Error(err)
		return err		
	}

	log.WithFields(dbLogFields).Infof("Succsessfully deleted node with id '%s'\n'", nodeId)

	return nil
}

func (m *MembershipManagerUnit) dbSelectAllNetworkNodes() (map[string]*pb.Node, error) {
	rows, err := m.pool.Query(context.Background(), `SELECT * FROM ` + m.storageNodeSchema + `.` + m.storageNodeTable + `;`)
    if err != nil {
		log.WithFields(dbLogFields).Error(err.Error())
        return nil, err
    }

    defer rows.Close()
    
	nodes := make(map[string]*pb.Node)

    for rows.Next() {
        var nodeName, ipAddress, httpsAddress, boottime string
		var id, port int32
		var publicId []byte
        if err := rows.Scan(&id, &nodeName, &ipAddress, &publicId, &httpsAddress, &port, &boottime); err != nil {
			log.WithFields(dbLogFields).Errorln(err.Error())
			continue
        }

		bTime, err := time.Parse(time.RFC1123Z, boottime)
		if err != nil {
			log.WithFields(dbLogFields).Errorln(err.Error())
			return nil, err
		}

		node := &pb.Node{
			Name: nodeName,
			IfritAddress: ipAddress,
			Id: []byte(publicId),
			HttpsAddress: httpsAddress,
			Port: port,
			BootTime: pbtime.New(bTime),
		}

		nodes[nodeName] = node
    } 

	return nodes, nil
}

func (m *MembershipManagerUnit) dbSelectNetworkNode(nodeId string) (*pb.Node, error) {
	if nodeId == "" {
		return nil, fmt.Errorf("Node id is empty")
	}

	q := `SELECT * FROM ` + m.storageNodeSchema + `.` + m.storageNodeTable + ` WHERE node_name = $1;`
	var nodeName, ipAddress, httpsAddress, boottime string
	var id, port int32
	var publicId []byte	
	err := m.pool.QueryRow(context.Background(), q, nodeId).Scan(&id, &nodeName, &ipAddress, &publicId, &httpsAddress, &port, &boottime)
	switch err {
	case pgx.ErrNoRows:
		log.WithFields(dbLogFields).
		WithField("database query", fmt.Sprintf("could not find '%s' in database", nodeId)).
		Errorln(err.Error())
		return nil, nil
	case nil:
	default:
		return nil, err
	}
	
	bTime, err := time.Parse(time.RFC1123Z, boottime)
	if err != nil {
		log.WithFields(dbLogFields).Errorln(err.Error())
		return nil, err
	}

	log.WithFields(dbLogFields).Infof("Succsessfully selected node with id '%s'\n'", nodeId)

	return &pb.Node{
		Name: nodeName,
		IfritAddress: ipAddress,
		Id: publicId,
		HttpsAddress: httpsAddress,
		Port: port,
		BootTime: pbtime.New(bTime),
	}, nil
}

func (m *MembershipManagerUnit) dbNetworkNodeExists(nodeId string) (bool, error) {
	if nodeId == "" {
		return false, errNoNodeId
	}

	var exists bool
	q := `SELECT EXISTS ( SELECT 1 FROM ` + m.storageNodeSchema + `.` + m.storageNodeTable + ` WHERE node_name = $1);`
	err := m.pool.QueryRow(context.Background(), q, nodeId).Scan(&exists)
	if err != nil {
		log.WithFields(dbLogFields).
			WithField("database query", fmt.Sprintf("could not find '%s' in database", nodeId)).
			Errorln(err.Error())
		return false, err
	}
	return exists, nil
}