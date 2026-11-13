// Package kube holds the small subset of the Kubernetes API that kubectl-why
// reads, and a thin wrapper around the kubectl binary to fetch it.
//
// The plugin deliberately has no dependency on client-go: it asks kubectl for
// JSON, so it honours the user's kubeconfig, context, auth plugins and proxy
// settings exactly the way kubectl itself does.
package kube

import "time"

type ObjectMeta struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace"`
	Labels            map[string]string `json:"labels,omitempty"`
	CreationTimestamp time.Time         `json:"creationTimestamp"`
	DeletionTimestamp *time.Time        `json:"deletionTimestamp,omitempty"`
	Finalizers        []string          `json:"finalizers,omitempty"`
	OwnerReferences   []OwnerReference  `json:"ownerReferences,omitempty"`
}

type OwnerReference struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type Pod struct {
	Metadata ObjectMeta `json:"metadata"`
	Spec     PodSpec    `json:"spec"`
	Status   PodStatus  `json:"status"`
}

type PodSpec struct {
	NodeName       string      `json:"nodeName,omitempty"`
	InitContainers []Container `json:"initContainers,omitempty"`
	Containers     []Container `json:"containers"`
	Volumes        []Volume    `json:"volumes,omitempty"`
}

type Container struct {
	Name           string               `json:"name"`
	Image          string               `json:"image"`
	Resources      ResourceRequirements `json:"resources"`
	LivenessProbe  *Probe               `json:"livenessProbe,omitempty"`
	ReadinessProbe *Probe               `json:"readinessProbe,omitempty"`
	StartupProbe   *Probe               `json:"startupProbe,omitempty"`
}

type ResourceRequirements struct {
	Limits   map[string]string `json:"limits,omitempty"`
	Requests map[string]string `json:"requests,omitempty"`
}

type Probe struct {
	HTTPGet *struct {
		Path string `json:"path"`
		Port any    `json:"port"`
	} `json:"httpGet,omitempty"`
	TCPSocket *struct {
		Port any `json:"port"`
	} `json:"tcpSocket,omitempty"`
	Exec *struct {
		Command []string `json:"command"`
	} `json:"exec,omitempty"`
	InitialDelaySeconds int `json:"initialDelaySeconds,omitempty"`
	PeriodSeconds       int `json:"periodSeconds,omitempty"`
	FailureThreshold    int `json:"failureThreshold,omitempty"`
}

type Volume struct {
	Name                  string `json:"name"`
	PersistentVolumeClaim *struct {
		ClaimName string `json:"claimName"`
	} `json:"persistentVolumeClaim,omitempty"`
	ConfigMap *struct {
		Name string `json:"name"`
	} `json:"configMap,omitempty"`
	Secret *struct {
		SecretName string `json:"secretName"`
	} `json:"secret,omitempty"`
}

type PodStatus struct {
	Phase                 string            `json:"phase"`
	Reason                string            `json:"reason,omitempty"`
	Message               string            `json:"message,omitempty"`
	Conditions            []Condition       `json:"conditions,omitempty"`
	InitContainerStatuses []ContainerStatus `json:"initContainerStatuses,omitempty"`
	ContainerStatuses     []ContainerStatus `json:"containerStatuses,omitempty"`
	StartTime             *time.Time        `json:"startTime,omitempty"`
}

type Condition struct {
	Type               string    `json:"type"`
	Status             string    `json:"status"`
	Reason             string    `json:"reason,omitempty"`
	Message            string    `json:"message,omitempty"`
	LastTransitionTime time.Time `json:"lastTransitionTime"`
}

type ContainerStatus struct {
	Name         string         `json:"name"`
	Ready        bool           `json:"ready"`
	Started      *bool          `json:"started,omitempty"`
	RestartCount int            `json:"restartCount"`
	Image        string         `json:"image"`
	State        ContainerState `json:"state"`
	LastState    ContainerState `json:"lastState"`
}

type ContainerState struct {
	Waiting *struct {
		Reason  string `json:"reason"`
		Message string `json:"message,omitempty"`
	} `json:"waiting,omitempty"`
	Running *struct {
		StartedAt time.Time `json:"startedAt"`
	} `json:"running,omitempty"`
	Terminated *Terminated `json:"terminated,omitempty"`
}

type Terminated struct {
	ExitCode   int       `json:"exitCode"`
	Signal     int       `json:"signal,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	Message    string    `json:"message,omitempty"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
}

type Event struct {
	Metadata       ObjectMeta `json:"metadata"`
	InvolvedObject struct {
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
		FieldPath string `json:"fieldPath,omitempty"`
	} `json:"involvedObject"`
	Type           string    `json:"type"`
	Reason         string    `json:"reason"`
	Message        string    `json:"message"`
	Count          int       `json:"count,omitempty"`
	LastTimestamp  time.Time `json:"lastTimestamp,omitempty"`
	EventTime      time.Time `json:"eventTime,omitempty"`
	FirstTimestamp time.Time `json:"firstTimestamp,omitempty"`
}

// When returns the most meaningful timestamp of an event.
func (e Event) When() time.Time {
	switch {
	case !e.LastTimestamp.IsZero():
		return e.LastTimestamp
	case !e.EventTime.IsZero():
		return e.EventTime
	case !e.FirstTimestamp.IsZero():
		return e.FirstTimestamp
	}
	return e.Metadata.CreationTimestamp
}

type Deployment struct {
	Metadata ObjectMeta `json:"metadata"`
	Spec     struct {
		Replicas *int `json:"replicas,omitempty"`
		Selector struct {
			MatchLabels map[string]string `json:"matchLabels,omitempty"`
		} `json:"selector"`
	} `json:"spec"`
	Status struct {
		Replicas            int         `json:"replicas"`
		UpdatedReplicas     int         `json:"updatedReplicas"`
		ReadyReplicas       int         `json:"readyReplicas"`
		AvailableReplicas   int         `json:"availableReplicas"`
		UnavailableReplicas int         `json:"unavailableReplicas"`
		Conditions          []Condition `json:"conditions,omitempty"`
	} `json:"status"`
}

type List[T any] struct {
	Items []T `json:"items"`
}
