package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/golang/glog"
	"github.com/gorilla/mux"
	"github.com/sparkcorp/allspark/pkg/api"
	"github.com/sparkcorp/allspark/pkg/conf"
	"github.com/sparkcorp/allspark/pkg/controller"
	"github.com/sparkcorp/allspark/pkg/handlers"
	"github.com/sparkcorp/allspark/pkg/version"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

var (
	showVersionAndExit bool
	cfg                conf.Config
)

func init() {
	flag.CommandLine.Parse([]string{})
}

func cmd() *cobra.Command {
	c := cobra.Command{
		Use:   "allspark",
		Short: "All Spark Operator Controller",
		Long:  "",
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

			sharedInformers := informers.NewSharedInformerFactoryWithOptions(
				kubecli,
				time.Second*30,
				informers.WithNamespace(cfg.WatchNamespace),
			)

			stopc := wait.NeverStop
			asc := controller.NewASController(
				kubecli,
				sharedInformers.Extensions().V1beta1().Ingresses(),
				sharedInformers.Core().V1().Namespaces(),
				sharedInformers.Core().V1().Services(),
				sharedInformers.Core().V1().Nodes(),
				&cfg,
			)
			go asc.Run(1, stopc)

			sharedInformers.Start(stopc)
			if !cache.WaitForCacheSync(stopc, asc.IngressHasSynced, asc.ServiceHasSynced) {
				glog.Fatalf("Receive shutdown on cache sync.")
			}
			common := &api.FrpcCommon{
				AdminAddress:  "0.0.0.0",
				AdminPort:     7400,
				ServerAddress: cfg.FRPSAddress,
				// ServerPort:    cfg.FRPSPort,
			}
			h := handlers.New(asc, common)
			r := mux.NewRouter()
			r.HandleFunc("/v1/namespaces/{namespace}/ingress/{name}", h.IngressToIni)

			glog.Info("Listening to port :3500")
			return http.ListenAndServe(":3500", r)
		},
	}
	c.Flags().BoolVar(&showVersionAndExit, "version", false, "Print version and exit.")
	c.Flags().StringVar(&cfg.KubeConfigPath, "kubeconfig", "", "Path to kubeconfig file.")
	c.Flags().StringVar(&cfg.PublicMasterURL, "public-master-url", "", "The public address of the master url used by syncer.")
	c.Flags().StringVar(&cfg.FRPSAddress, "frps-address", "", "The address of the FRP Server.")
	// c.Flags().Int32Var(&cfg.FRPSPort, "frps-port", 7000, "The port of the FRP Server.")
	c.Flags().StringVar(&cfg.FRPSToken, "frps-token", "", "FRPS token to establish trust with client.")
	c.Flags().StringVar(&cfg.ContainerImage, "image", "quay.io/sandromello/frp:v0.20.0", "The FRP image used by this controller.")
	c.Flags().StringVar(&cfg.FRPSNodeIP, "node-ip", "", "The IP of the node to expose FRPS ports.")
	c.Flags().StringVar(&cfg.WatchNamespace, "watch-namespace", corev1.NamespaceAll, "Namespace to watch for Ingress. Default is to watch all namespaces")
	c.PersistentFlags().AddGoFlagSet(flag.CommandLine)
	return &c
}

func main() {
	if err := cmd().Execute(); err != nil {
		fmt.Println(err.Error())
	}
}
