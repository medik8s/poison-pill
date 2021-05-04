package peers

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/go-logr/logr"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	nodeNameEnvVar    = "MY_NODE_NAME"
	hostnameLabelName = "kubernetes.io/hostname"
	// TODO make this configurable?
	nrOfPeers = 15
)

var (
	myNodeName = os.Getenv(nodeNameEnvVar)
)

type Peers struct {
	client.Client
	log          logr.Logger
	peerList     *[]v1.Node
	peerSelector labels.Selector
	mutex        sync.Mutex
}

func New(peerUpdateInterval time.Duration, c client.Client, log logr.Logger) (*Peers, error) {

	p := &Peers{
		Client: c,
		log:    log,
		mutex:  sync.Mutex{},
	}

	// get own hostname label value and create a label selector from it
	// will be used for updating the peer list and skipping ourself
	myNode := &v1.Node{}
	key := client.ObjectKey{
		Name: myNodeName,
	}
	if err := p.Get(context.Background(), key, myNode); err != nil {
		log.Error(err, "failed to get own node")
		return nil, err
	}
	if hostname, ok := myNode.Labels[hostnameLabelName]; !ok {
		err := fmt.Errorf("%s label not set on own node", hostnameLabelName)
		log.Error(err, "failed to get own hostname")
		return nil, err
	} else {
		req, _ := labels.NewRequirement(hostnameLabelName, selection.NotEquals, []string{hostname})
		p.peerSelector = labels.NewSelector().Add(*req)
	}

	// get initial peer list
	p.UpdatePeers()

	// start loop for updating peer list regularly
	ticker := time.NewTicker(peerUpdateInterval)
	go func() {
		for {
			select {
			case <-ticker.C:
				p.UpdatePeers()
			}
		}
	}()

	return p, nil
}

func (p *Peers) UpdatePeers() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	nodes := &v1.NodeList{}
	// get some nodes, but not ourself
	if err := p.List(context.Background(), nodes, client.Limit(nrOfPeers), client.MatchingLabelsSelector{Selector: p.peerSelector}); err != nil {
		if errors.IsNotFound(err) {
			// we are the only node at the moment... reset peerList
			p.peerList = &[]v1.Node{}
		}
		p.log.Error(err, "failed to update peer list")
		// TODO handle API error... ask peers now? Maybe not, and another healthcheck loop makes sense,
		// because we don't want and need to update the node list very frequently
		return
	}
	p.peerList = &nodes.Items
}

func (p *Peers) GetPeers() *[]v1.Node {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	return p.peerList
}
