package utils

import (
	"context"
	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strconv"
)

const (
	isRebootCapableLabel = "is-reboot-capable"
)

// updateLabel updates the pod's label (key) to the given value
func updateLabel(labelKey string, labelValue bool, pod *v1.Pod, c client.Client) error {
	if pod.Labels == nil {
		pod.Labels = map[string]string{}
	}
	pod.Labels[labelKey] = strconv.FormatBool(labelValue)
	//update in yaml
	err := c.Update(context.Background(), pod)
	return err
}

// UpdatePodIsRebootCapableLabel updates the is-reboot-capable label to be true if any kind
// of reboot is enabled and false if there isn't watchdog and software reboot is disabled
//TODO - change the function to only get manager instead of client and reader
func UpdatePodIsRebootCapableLabel(watchdogInitiated bool, nodeName string, client client.Client, setupLog logr.Logger, reader client.Reader) error {
	//get pod in order to update it's label
	pod, err := GetPoisonPillAgentPod(nodeName, reader)
	if err != nil {
		setupLog.Error(err, "failed to list poison pill agent pods")
		return err
	}

	softwareRebootEnabledEnv := os.Getenv("IS_SOFTWARE_REBOOT_ENABLED")
	softwareRebootEnabled, err := strconv.ParseBool(softwareRebootEnabledEnv)
	if err != nil {
		setupLog.Error(err, "failed to convert env variable value to boolean",
			"IS_SOFTWARE_REBOOT_ENABLED value", softwareRebootEnabledEnv)
		return err
	}
	if watchdogInitiated || softwareRebootEnabled {
		err = updateLabel(isRebootCapableLabel, true, pod, client)
		return err
	}

	err = updateLabel(isRebootCapableLabel, false, pod, client)
	return err
}
