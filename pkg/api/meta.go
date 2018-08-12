package api

import (
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/labels"

	"github.com/golang/glog"
	"github.com/sparkcorp/allspark/pkg/conf"
	// metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	defaultQPS     = float32(100.0)
	defaultBurst   = 100
	bucketMaxPorts = 1000
)

func Common() *FrpcCommon {
	return &FrpcCommon{
		Token:        os.Getenv("FRPS_TOKEN"),
		AdminAddress: "0.0.0.0",
		AdminPort:    7400,
	}

}

func NewKubernetesConfig(config *conf.Config) *rest.Config {
	kubecfgPathEnv := os.Getenv("KUBECONFIG")
	if kubecfgPathEnv != "" {
		config.KubeConfigPath = kubecfgPathEnv
	}
	config.KubeConfigPath = os.ExpandEnv(config.KubeConfigPath)
	kubecfg, err := clientcmd.BuildConfigFromFlags(config.MasterURL, config.KubeConfigPath)
	if err != nil {
		glog.Infof("Failed retrieving kube config: %v", err)
		os.Exit(1)
	}
	kubecfg.QPS = defaultQPS
	kubecfg.Burst = defaultBurst
	return kubecfg
}

func NewFRPSConfig(meta metav1.ObjectMeta, bindPort, httpPort, httpsPort int32) *FRPSCommon {
	if bindPort == 0 || httpPort == 0 || httpsPort == 0 {
		return nil
	}
	// https://github.com/fatedier/frp/blob/master/conf/frps_full.ini
	return &FRPSCommon{
		Namespace:      meta.Namespace,
		ServiceName:    meta.Name,
		BindPort:       bindPort,
		VhostHTTPPort:  httpPort,
		VhostHTTPSPort: httpsPort,
		MaxPoolCount:   5,
		LogLevel:       "info",
	}
}

func NewPortBucket(lister corelisters.ServiceNamespaceLister) *PortBucket {
	b := &PortBucket{
		serviceLister: lister,
		store:         make(map[string]map[int32]bool),
	}
	return b
}

func (b *PortBucket) Reload(ip string) error {
	services, err := b.serviceLister.List(labels.Everything())
	if err != nil {
		return fmt.Errorf("Listing services error: %v", err)
	}

	b.store[ip] = make(map[int32]bool)
	for i := 0; i <= 65535; i++ {
		// true means the port could be allocated
		b.store[ip][int32(i)] = true
	}
	for _, svc := range services {
		for _, externalIP := range svc.Spec.ExternalIPs {
			// false means the port is already allocated
			for _, p := range svc.Spec.Ports {
				b.store[externalIP][p.Port] = false
			}
		}
	}
	return nil
}

// Pop returns an allocable port, 0 indicates that there's no more ports to return
func (b *PortBucket) Pop(ip string) int32 {
	for port, free := range b.store[ip] {
		if port >= 20000 && port <= 21000 && free {
			b.store[ip][port] = false
			return port
		}
	}
	return 0
}

// PopMany returns a slice of ports based on a given length, returns nil
// if there's no more ports to return
func (b *PortBucket) PopMany(ip string, length int) (ports []int32) {
	for i := 0; i < length; i++ {
		port := b.Pop(ip)
		// no more ports available
		if port == 0 {
			return nil
		}
		ports = append(ports, port)
	}
	return ports
}
