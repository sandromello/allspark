package main

import (
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/ini.v1"

	"github.com/golang/glog"

	"github.com/sparkcorp/allspark/pkg/api"
	"github.com/sparkcorp/allspark/pkg/conf"
	"github.com/sparkcorp/allspark/pkg/request"
	"github.com/sparkcorp/allspark/pkg/version"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/client-go/kubernetes"
)

const (
	defaultIngressServer = "http://frpc-ingress-server.$NAMESPACE.svc.cluster.local"
	defaultIniPath       = "/etc/frpc/config.ini"
	publicNamespace      = "kube-public"
)

var (
	showVersionAndExit bool
	cfg                conf.Config
	syncType           string
)

type Config struct {
	KubeConfigPath string
}

func init() {
	flag.CommandLine.Parse([]string{})
}

func cmd() *cobra.Command {
	c := cobra.Command{
		Use:   "ini-sync",
		Short: "ini-sync will resync the frpc.ini periodically.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersionAndExit {
				version.PrintAndExit()
			}
			version.Print()

			kubecli := kubernetes.NewForConfigOrDie(api.NewKubernetesConfig(&cfg))
			v, err := kubecli.Discovery().ServerVersion()
			if err != nil {
				log.Fatalf("Failed discovering Kubernetes version: %v", err)
			}
			glog.Infof("Running in Kubernetes Cluster version v%v.%v (%v) - git (%v) commit %v - platform %v",
				v.Major, v.Minor, v.GitVersion, v.GitTreeState, v.GitCommit, v.Platform)

			sleepTime := time.Second * time.Duration(cfg.DefaultIniResync)
			switch conf.SyncType(syncType) {
			case conf.SyncIngress:
				iniServerURL, err := discoverIniServer(kubecli)
				if err != nil {
					glog.Fatalf("failed discovering ini server: %v", err)
				}
				for {
					glog.Infof("sync started!")
					err := syncFrpcIngress(iniServerURL, cfg.FRPCIniFile)
					if err != nil {
						glog.Warningf(err.Error())
						glog.Warningf("sync failed")
					} else {
						glog.Infof("sync with success!")
					}
					glog.Infof("resync after %d second(s)", int(sleepTime.Seconds()))
					time.Sleep(sleepTime)
				}
			case conf.SyncKubelet:
				for {
					glog.Infof("sync started!")

					err := syncFrpcKubelet(kubecli, cfg.FRPCIniFile)
					if err != nil {
						glog.Warningf(err.Error())
						glog.Warningf("sync failed")
					} else {
						glog.Infof("synced with success!")
					}
					glog.Infof("resync after %d second(s)", int(sleepTime.Seconds()))
					time.Sleep(sleepTime)
				}
			default:
				glog.Fatalf("Sync type not found %q", syncType)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&showVersionAndExit, "version", false, "Print version and exit.")
	c.Flags().StringVar(&cfg.KubeConfigPath, "kubeconfig", "", "Path to kubeconfig file.")
	c.Flags().StringVar(&cfg.MasterURL, "master-url", "", "Customize the address of the api server.")
	c.Flags().StringVar(&syncType, "sync", string(conf.SyncIngress), "Which component to sync, 'Kubelet' or 'Ingress'.")
	c.Flags().Int64Var(&cfg.DefaultIniResync, "resync", 120, "Path to kubeconfig file.")
	c.Flags().StringVar(&cfg.FRPCIniFile, "frpc-ini", defaultIniPath, "Path to write frpc ini config.")
	c.Flags().StringVar(&cfg.FRPCIniServer, "frpc-ini-server", defaultIngressServer, "The server to fetch the FRPC ini rules.")
	c.PersistentFlags().AddGoFlagSet(flag.CommandLine)
	return &c
}

func discoverIniServer(kubecli kubernetes.Interface) (*url.URL, error) {
	svc, err := kubecli.Core().Services(publicNamespace).Get("ini-server", metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	proto := "http"
	var port int32
	for _, p := range svc.Spec.Ports {
		if p.Port == 443 {
			proto = "https"
		}
		port = p.Port
		break
	}
	rawURL := fmt.Sprintf("%s://%s", proto, svc.Spec.ExternalName)
	if port != 0 {
		rawURL = fmt.Sprintf("%s:%d", rawURL, port)
	}
	return url.Parse(rawURL)
}

func syncFrpcKubelet(kubecli kubernetes.Interface, iniPath string) error {
	// Discover the gates of Valhala (FRPS address)
	svc, err := kubecli.Core().Services(publicNamespace).Get("valhala", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed discovering service %s/valhala: %v", publicNamespace, err)
	}
	frpsAddress := svc.Spec.ExternalName
	if frpsAddress == "" {
		glog.Infof("external name is empty for service %q", svc.Name)
	}
	glog.Infof("Found the road to valhala: %q, searching for the door ...", frpsAddress)
	// Get the kubelet service name (same as the host) to
	// discover the tenant and retrieve the FRPS port
	namespace, nodeName := os.Getenv("POD_NAMESPACE"), os.Getenv("POD_NODE_NAME")
	serviceName := strings.Split(nodeName, ".")[0]
	svc, err = kubecli.Core().Services(namespace).Get(serviceName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed getting kubelet service: %v", err)
	}
	tenantName := svc.Spec.Selector["tenant"]
	if tenantName == "" {
		return fmt.Errorf("Tenant selector is empty!")
	}
	svc, err = kubecli.Core().Services(namespace).Get(tenantName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed getting tenant %q: %v", tenantName, err)
	}
	var frpsPort int32
	for _, port := range svc.Spec.Ports {
		if port.Name == "frps" {
			frpsPort = port.Port
		}
	}
	if frpsPort == 0 {
		return fmt.Errorf("failed discovering frps port for service %q", svc.Name)
	}
	glog.Infof("Found port %d for tenant %q", frpsPort, tenantName)

	frpcini := ini.Empty()
	common := frpcini.Section("common")
	common.Key("server_addr").SetValue(frpsAddress)
	common.Key("server_port").SetValue(strconv.Itoa(int(frpsPort)))
	kubelet := frpcini.Section(serviceName)
	kubelet.Key("type").SetValue("https")
	kubelet.Key("local_ip").SetValue(os.Getenv("POD_HOST_IP"))
	kubelet.Key("local_port").SetValue("10250")
	kubelet.Key("custom_domains").SetValue(nodeName)

	isFirstSync := false
	if _, err := os.Stat(iniPath); os.IsNotExist(err) {
		isFirstSync = true
	}
	if err := frpcini.SaveTo(iniPath); err != nil {
		return fmt.Errorf("failed saving to %q. %v", iniPath, err)
	}
	glog.Infof("frpc ini %q wrote with success!", iniPath)
	printSections(frpcini.Sections())
	if _, err := reloadFrpc(7400); err != nil && !isFirstSync {
		glog.Warningf("failed reloading frpc config: %v", err)
	}
	return nil
}

func reloadFrpc(adminPort int) ([]byte, error) {
	addr, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", adminPort))
	return request.New(nil, addr).
		Resource("/api/reload").
		Do().Raw()
}

func syncFrpcIngress(ingressServer *url.URL, iniPath string) error {
	ingressName := os.Getenv("INGRESS_NAME")
	namespace := os.Getenv("POD_NAMESPACE")
	resource := fmt.Sprintf("/v1/namespaces/%s/ingress/%s", namespace, ingressName)

	glog.Infof("Requesting %s", ingressServer.String())
	rawIni, err := request.New(nil, ingressServer).Resource(resource).Do().Raw()
	if err != nil {
		return fmt.Errorf("failed fetching ini: %v", err)
	}
	frpcini, err := ini.Load(rawIni)
	if err != nil {
		return fmt.Errorf("failed loading ini: %v", err)
	}
	if err := frpcini.SaveTo(iniPath); err != nil {
		return fmt.Errorf("failed saving to %q. %v", iniPath, err)
	}
	glog.Infof("frpc ini %q wrote with success!", iniPath)
	adminPort, _ := frpcini.Section("common").Key("admin_port").Int()
	if adminPort == 0 {
		adminPort = 7400
	}
	printSections(frpcini.Sections())
	data, err := reloadFrpc(adminPort)
	if err != nil {
		glog.Warningf("failed reloading frpc config: %v", err)
		return nil
	}
	glog.Infof("reloaded %v/%v: %v", namespace, ingressName, string(data))
	return nil
}

func printSections(sections []*ini.Section) {
	text := "section=%s, type=%s, local_ip=%s, local_port=%s, custom_domains=%s, locations=%s"
	for _, sec := range sections {
		if sec.Key("type").String() == "" {
			continue
		}
		glog.Infof(
			text,
			sec.Name(),
			sec.Key("type").String(),
			sec.Key("local_ip").String(),
			sec.Key("local_port").String(),
			sec.Key("custom_domains").String(),
			sec.Key("locations").String(),
		)
	}
}

func main() {
	if err := cmd().Execute(); err != nil {
		log.Fatalf("Failed starting frpc server: %v", err)
	}
}
