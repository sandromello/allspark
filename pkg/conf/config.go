package conf

type SyncType string

const (
	// SyncKubelet will sync an ini file based on the discovery of frps services
	SyncKubelet SyncType = "Kubelet"
	// SyncIngress will sync an ini file based on the result of the ini-server
	SyncIngress SyncType = "Ingress"
)

type Config struct {
	KubeConfigPath   string
	MasterURL        string
	PublicMasterURL  string
	FRPCIniFile      string
	FRPSAddress      string
	FRPSPort         int32
	WatchNamespace   string
	ContainerImage   string
	FRPSToken        string
	FRPCIniServer    string
	FRPSNodeIP       string
	DefaultIniResync int64
}
