package metrics

import (
	"context"
	"github.com/medik8s/poison-pill/pkg/utils"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	v1 "k8s.io/api/core/v1"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

//TODO- move this const to a package and use in main too
const NodeNameEnvVar = "MY_NODE_NAME"

var (
	// TODO - CHANGE DESCRIPTION NodeHealthCheckOldRemediationCR is a Prometheus metric, which reports the number of old Remediation CRs.
	// It is an indication for remediation that is pending for a long while, which might indicate a problem with the external remediation mechanism.
	isNodeRebootCapable = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "is_node_reboot_capable",
			Help: "Is the node abel to reboot",
		})
)

func InitializePoisonPillMetrics() {
	metrics.Registry.MustRegister(
		isNodeRebootCapable,
	)
}

func ObserveIsNodeRebootCapableAnnotation(c client.Client) error {
	myNodeName := os.Getenv(NodeNameEnvVar)
	if myNodeName == "" {
		errors.Wrapf(errors.New("failed to get own node name"), "node name was empty. env var name: %s",
			NodeNameEnvVar)
	}
	//TODO create a node file in package an put there both the const of nodename and the function for getting node
	// and function for getting node name
	node := &v1.Node{}
	key := client.ObjectKey{
		Name: myNodeName,
	}

	if err := c.Get(context.Background(), key, node); err != nil {
		return errors.Wrapf(err, "failed to retrieve my node: "+myNodeName)
	}

	if node.Annotations == nil || node.Annotations[utils.IsRebootCapableAnnotation] == "false" {
		isNodeRebootCapable.Set(0)
	} else {
		isNodeRebootCapable.Set(1)
	}

	return nil
}