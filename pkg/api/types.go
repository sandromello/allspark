package api

import (
	corelisters "k8s.io/client-go/listers/core/v1"
)

type ServerInfo struct {
	Version            string      `json:"version"`
	BindPort           int         `json:"bind_port"`
	BindUDPPort        int         `json:"bind_udp_port"`
	VhostHttpPort      int         `json:"vhost_http_port"`
	VhostHttpsPort     int         `json:"vhost_https_port"`
	KCPBindPort        int         `json:"kcp_bind_port"`
	AuthTimeout        int64       `json:"auth_timeout"`
	SubdomainHost      string      `json:"subdomain_host"`
	MaxPoolCount       int         `json:"max_pool_count"`
	MaxPortsPerClient  int         `json:"max_ports_per_client"`
	HeartBeatTimeout   int         `json:"heart_beat_timeout"`
	TotalTrafficIn     int64       `json:"total_traffic_in"`
	TotalTrafficOut    int64       `json:"total_traffic_out"`
	CurrentConnections int64       `json:"cur_conns"`
	ClientCounts       int64       `json:"client_counts"`
	ProxyTypeCount     interface{} `json:"proxy_type_count"`
}

type FprcHTTP struct {
	Section           string `ini:"-"`
	Type              string `ini:"type"`
	LocalIP           string `ini:"local_ip"`
	LocalPort         int32  `ini:"local_port"`
	UseEncryption     bool   `ini:"use_encryption,omitempty"`
	UseCompression    bool   `ini:"use_compression,omitempty"`
	CustomDomains     string `ini:"custom_domains"`
	Locations         string `ini:"locations,omitempty"`
	HostHeaderRewrite string `ini:"host_header_rewrite,omitempty"`
}

type FrpcCommon struct {
	ServerAddress string `ini:"server_addr"`
	ServerPort    int32  `ini:"server_port"`
	LogLevel      string `ini:"log_level,omitempty"`
	Token         string `ini:"token,omitempty"`
	AdminAddress  string `ini:"admin_addr,omitempty"`
	AdminPort     int32  `ini:"admin_port,omitempty"`
	PoolCount     int32  `ini:"pool_count,omitempty"`
}

type FrpcResponse struct {
	Code    int    `json:"code"`
	Message string `json:"msg"`
}

type FRPSCommon struct {
	Namespace   string `ini:"-"`
	ServiceName string `ini:"-"`

	BindPort       int32  `ini:"bind_port"`
	VhostHTTPPort  int32  `ini:"vhost_http_port"`
	VhostHTTPSPort int32  `ini:"vhost_https_port"`
	LogLevel       string `ini:"log_level"`
	Token          string `ini:"token"`
	MaxPoolCount   int    `ini:"max_pool_count"`
}

// PortBucket keeps track of allocated ports for a given ip address
type PortBucket struct {
	serviceLister corelisters.ServiceNamespaceLister
	namespace     string
	store         map[string]map[int32]bool
}
