package controller

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/sparkcorp/allspark/pkg/api"
	"github.com/sparkcorp/allspark/pkg/conf"

	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	extensions "k8s.io/api/extensions/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coreinformer "k8s.io/client-go/informers/core/v1"
	extinformer "k8s.io/client-go/informers/extensions/v1beta1"
	corelister "k8s.io/client-go/listers/core/v1"
	extlister "k8s.io/client-go/listers/extensions/v1beta1"
)

const (
	// MessageResourceExists is the message used for Events when a resource
	// fails to sync due to a Pod already existing
	MessageMaxPortsReached = "Max ports allocation reached for IP %q"
	MessageResourceExists  = "Resource %q already exists and is not managed by Ingress"
	LabelPrefix            = "allspark.sh"
)

type ASController struct {
	kubecli kubernetes.Interface

	IngressLister    extlister.IngressLister
	IngressHasSynced cache.InformerSynced

	NamespaceLister    corelister.NamespaceLister
	NamespaceHasSynced cache.InformerSynced

	NodeLister    corelister.NodeLister
	NodeHasSynced cache.InformerSynced

	// Used by IngressToIni Handler
	ServiceLister    corelister.ServiceLister
	ServiceHasSynced cache.InformerSynced

	ingQueue  *TaskQueue
	nsQueue   *TaskQueue
	nodeQueue *TaskQueue

	portBucket *api.PortBucket
	cfg        *conf.Config
	// recorder *Recorder
}

// TODO: if the controller has a distinct token, recreate all pods
// TODO: reload when the frps service name changes or when the controller is initializing

func NewASController(
	cli kubernetes.Interface,
	ingInf extinformer.IngressInformer,
	nsInf coreinformer.NamespaceInformer,
	svcInf coreinformer.ServiceInformer,
	nodeInf coreinformer.NodeInformer,
	cfg *conf.Config,
) *ASController {
	c := &ASController{
		kubecli:            cli,
		IngressLister:      ingInf.Lister(),
		IngressHasSynced:   ingInf.Informer().HasSynced,
		NamespaceLister:    nsInf.Lister(),
		NamespaceHasSynced: nsInf.Informer().HasSynced,
		NodeLister:         nodeInf.Lister(),
		NodeHasSynced:      nodeInf.Informer().HasSynced,
		ServiceLister:      svcInf.Lister(),
		ServiceHasSynced:   svcInf.Informer().HasSynced,
		portBucket:         api.NewPortBucket(svcInf.Lister().Services(os.Getenv("POD_NAMESPACE"))),

		cfg: cfg,
	}
	c.ingQueue = NewTaskQueue("frpc-operator", c.syncIngress)
	c.nsQueue = NewTaskQueue("frps-operator", c.syncNamespaces)
	c.nodeQueue = NewTaskQueue("node-operator", c.syncNodes)

	ingInf.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if isFrpIngress(obj.(*extensions.Ingress)) {
				c.ingQueue.Add(obj)
			}
		},
		UpdateFunc: func(o, n interface{}) {
			// Resync periodically all resources
			if isFrpIngress(n.(*extensions.Ingress)) {
				c.ingQueue.Add(n)
			}
		},
	})

	nsInf.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if isAllSparkResource(obj.(*v1.Namespace)) {
				c.nsQueue.Add(obj)
			}
		},
		UpdateFunc: func(o, n interface{}) {
			if isAllSparkResource(n.(*v1.Namespace)) {
				c.nsQueue.Add(n)
			}
		},
	})

	nodeInf.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if isAllSparkResource(obj.(*v1.Node)) {
				c.nodeQueue.Add(obj)
			}
		},
		UpdateFunc: func(o, n interface{}) {
			old := o.(*v1.Node)
			new := n.(*v1.Node)
			if old.ResourceVersion != new.ResourceVersion && isAllSparkResource(new) {
				c.nodeQueue.Add(new)
			}
		},
	})
	return c
}

func (c *ASController) Run(threadiness int, stopCh <-chan struct{}) {
	defer runtime.HandleCrash()
	defer c.ingQueue.Shutdown()
	defer c.nsQueue.Shutdown()
	defer c.nodeQueue.Shutdown()

	if !cache.WaitForCacheSync(stopCh, c.IngressHasSynced, c.NamespaceHasSynced, c.NodeHasSynced) {
		return
	}

	glog.Infof("Starting allspark controller manager ...")
	for i := 0; i < threadiness; i++ {
		go c.ingQueue.run(time.Second, stopCh)
		go c.nsQueue.run(time.Second, stopCh)
		go c.nodeQueue.run(time.Second, stopCh)
	}
	<-stopCh
	glog.Infof("Shutting down allspark controller manager ...")
}

func (c *ASController) syncNamespaces(key string) error {
	ns, err := c.NamespaceLister.Get(key)
	if err != nil {
		if errors.IsNotFound(err) {
			glog.Infof("namespace '%s' in work queue no longer exists", key)
			return nil
		}
		return err
	}
	systemNamespace := os.Getenv("POD_NAMESPACE")
	tenant := ns.Labels["allspark.sh/tenant"]
	if err := c.portBucket.Reload(c.cfg.FRPSNodeIP); err != nil {
		return fmt.Errorf("Failed reloading port bucket: %v", err)
	}
	ports := c.portBucket.PopMany(c.cfg.FRPSNodeIP, 3)
	if ports == nil {
		glog.Warningf(MessageMaxPortsReached, c.cfg.FRPSNodeIP)
		return nil
	}

	// Sync FRPS Service
	newService := newFRPSService(ns, tenant, c.cfg.FRPSNodeIP, ports[0], ports[1], ports[2])
	svc, err := c.kubecli.Core().Services(systemNamespace).Get(tenant, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		s, err := c.kubecli.Core().Services(systemNamespace).Create(newService)
		if err != nil {
			return fmt.Errorf("Creating FRPS service error: %v", err)
		}
		glog.Infof("Created tenant service %q", s.Name)
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("Failed retrieving service %q: %v", tenant, err)
	}
	if err == nil && !reflect.DeepEqual(newService.Spec.Ports, svc.Spec.Ports) {
		// glog.Warningf("Ports for frps service %q has been changed", svc.Name)
		// TODO: UPDATE the service, the ports has been changed
	}

	// Sync Pod FRPS
	newPod := c.newFRPSPod(ns, tenant)
	pod, err := c.kubecli.Core().Pods(systemNamespace).Get(tenant, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		p, err := c.kubecli.Core().Pods(systemNamespace).Create(newPod)
		if err != nil {
			return fmt.Errorf("Creating FRPS pod error: %v", err)
		}
		glog.Infof("Created tenant pod %q", p.Name)
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("Failed retrieving FPRS service %q: %v", tenant, err)
	}
	if pod.Status.Phase != v1.PodRunning {
		glog.Warningf("The FRPS pod should be running, got status %q", pod.Status.Phase)
	}

	// TODO: update the pod if the config has a distinct version
	return nil
}

func (c *ASController) syncIngress(key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		glog.Infof("invalid resource key: %s", key)
		return nil
	}
	ns, err := c.NamespaceLister.Get(namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			glog.Infof("namespace '%s' in work queue no longer exists", key)
			return nil
		}
		return err
	}

	var tenant string
	if ns.Labels != nil {
		tenant = ns.Labels["allspark.sh/tenant"]
	}
	if tenant == "" {
		glog.V(2).Infof("Tenant not found for namespace %q, no-op", namespace)
		return nil
	}
	glog.V(2).Infof("%s - Found tenant %q", namespace, tenant)
	ing, err := c.IngressLister.Ingresses(namespace).Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			glog.Infof("ingress '%s' in work queue no longer exists", key)
			return nil
		}
		return err
	}

	pod, err := c.kubecli.Core().Pods(namespace).Get(ing.Name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		glog.Infof("failed getting pods: %v", err)
		return nil
	}

	if apierrors.IsNotFound(err) {
		if _, err := c.kubecli.Core().Pods(namespace).Create(c.newFRPCPod(ing)); err != nil {
			return err
		}
		// _, err := c.kubecli.Core().Services(namespace).Create(c.newService(ing))
		// if err != nil && !apierrors.IsAlreadyExists(err) {
		// 	return err
		// }
		glog.Infof("ingress synced: %s", key)
		return nil
	}

	// The pod is not controlled by the given ingress
	if !metav1.IsControlledBy(pod, ing) {
		return fmt.Errorf(fmt.Sprintf(MessageResourceExists, ing.Name))
	}

	glog.Infof("Synced %s with success", key)
	return nil
}

// TODO: sync when a service is deleted or updated
func (c *ASController) syncNodes(key string) error {
	node, err := c.NodeLister.Get(key)
	if err != nil {
		if errors.IsNotFound(err) {
			glog.Infof("node %q in work queue no longer exists", key)
			return nil
		}
		return err
	}
	podNamespace, tenantName := os.Getenv("POD_NAMESPACE"), node.Labels["allspark.sh/tenant"]
	// expect a cluster service dns. E.g.: node.svc.cluster.local
	// the first segment corresponds to a service on the namespace
	serviceName := strings.Split(node.Name, ".")[0]
	_, err = c.kubecli.Core().Services(podNamespace).Get(serviceName, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed getting service %q: %v", serviceName, err)
	}

	if apierrors.IsNotFound(err) {
		kubelet := newKubeletService(serviceName, node)
		_, err := c.kubecli.Core().Services(podNamespace).Create(kubelet)
		if err != nil {
			return fmt.Errorf("failed creating kubelet service: %v", err)
		}
		return nil
	}
	payload := fmt.Sprintf(`{"spec": {"selector": "%s"}}`, tenantName)
	_, err = c.kubecli.Core().Services(podNamespace).Patch(serviceName, types.MergePatchType, []byte(payload))
	if err != nil {
		return fmt.Errorf("failed patching service: %v", err)
	}
	return nil
}

func newService(ing *extensions.Ingress) *v1.Service {
	return &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ing.Name,
			Namespace: ing.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(ing, schema.GroupVersionKind{
					Group:   extensions.SchemeGroupVersion.Group,
					Version: extensions.SchemeGroupVersion.Version,
					Kind:    "Ingress",
				}),
			},
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{
				Name:       "frpcserver",
				Protocol:   v1.ProtocolTCP,
				Port:       80,
				TargetPort: intstr.FromString("frpcserver"),
			}},
			// ClusterIP: "None",
			Selector: map[string]string{"app": ing.Name},
		},
	}
}

func newKubeletService(serviceName string, node *v1.Node) *v1.Service {
	return &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: os.Getenv("POD_NAMESPACE"),
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(node, schema.GroupVersionKind{
					Group:   v1.SchemeGroupVersion.Group,
					Version: v1.SchemeGroupVersion.Version,
					Kind:    "Node",
				}),
			},
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{
				Name:       "kubelet",
				Protocol:   v1.ProtocolTCP,
				Port:       10250,
				TargetPort: intstr.FromString("https"),
			}},
			Selector: map[string]string{"tenant": node.Labels["allspark.sh/tenant"]},
		},
	}
}

func newFRPSService(refNS *v1.Namespace, tenant, nodeIP string, frps, http, https int32) *v1.Service {
	return &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tenant,
			Namespace: os.Getenv("POD_NAMESPACE"),
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(refNS, schema.GroupVersionKind{
					Group:   v1.SchemeGroupVersion.Group,
					Version: v1.SchemeGroupVersion.Version,
					Kind:    "Namespace",
				}),
			},
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{
					Name:       "frps",
					Protocol:   v1.ProtocolTCP,
					Port:       frps,
					TargetPort: intstr.FromString("frps"),
				},
				{
					Name:       "http",
					Protocol:   v1.ProtocolTCP,
					Port:       http,
					TargetPort: intstr.FromString("http"),
				},
				{
					Name:       "https",
					Protocol:   v1.ProtocolTCP,
					Port:       https,
					TargetPort: intstr.FromString("https"),
				},
			},
			ExternalIPs: []string{nodeIP},
			Selector:    map[string]string{"tenant": tenant},
		},
	}
}

func (c *ASController) newFRPSPod(refNS *v1.Namespace, tenant string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tenant,
			Namespace: os.Getenv("POD_NAMESPACE"),
			Labels:    map[string]string{"tenant": tenant},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(refNS, schema.GroupVersionKind{
					Group:   v1.SchemeGroupVersion.Group,
					Version: v1.SchemeGroupVersion.Version,
					Kind:    "Namespace",
				}),
			},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "frps",
					Image: c.cfg.ContainerImage,
					Command: []string{
						"frps",
						"--vhost_http_port=80",
						"--vhost_https_port=443",
					},
					Ports: []v1.ContainerPort{
						{
							Name:          "frps",
							Protocol:      v1.ProtocolTCP,
							ContainerPort: 7000,
						},
						{
							Name:          "http",
							Protocol:      v1.ProtocolTCP,
							ContainerPort: 80,
						},
						{
							Name:          "https",
							Protocol:      v1.ProtocolTCP,
							ContainerPort: 443,
						},
					},
				},
			},
		},
	}
}

func (c *ASController) newFRPCPod(ing *extensions.Ingress) *v1.Pod {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ing.Name,
			Namespace: ing.Namespace,
			Labels:    map[string]string{"app": ing.Name},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(ing, schema.GroupVersionKind{
					Group:   extensions.SchemeGroupVersion.Group,
					Version: extensions.SchemeGroupVersion.Version,
					Kind:    "Ingress",
				}),
			},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:    "frpc",
					Image:   c.cfg.ContainerImage,
					Command: []string{"frpc", "-c", "/etc/frpc/frpc.ini"},
					VolumeMounts: []v1.VolumeMount{{
						Name:      "frpc-ini",
						ReadOnly:  true,
						MountPath: "/etc/frpc",
					}},
				},
				{
					Name:  "sync",
					Image: c.cfg.ContainerImage,
					Command: []string{
						"ini-sync",
						"--frpc-ini", "/etc/frpc/frpc.ini",
						"--logtostderr",
					},
					VolumeMounts: []v1.VolumeMount{{
						Name:      "frpc-ini",
						MountPath: "/etc/frpc",
					}},
					Env: []v1.EnvVar{
						{
							Name:  "POD_NAMESPACE",
							Value: ing.Namespace,
						},
						{
							Name:  "INGRESS_NAME",
							Value: ing.Name,
						},
						{
							Name:  "KUBERNETES_SERVICE_HOST",
							Value: c.cfg.PublicMasterURL,
						},
					},
				},
			},
			Volumes: []v1.Volume{{
				Name:         "frpc-ini",
				VolumeSource: v1.VolumeSource{EmptyDir: &v1.EmptyDirVolumeSource{}},
			}},
		},
	}
	if c.cfg.FRPSToken != "" {
		pod.Spec.Containers[1].Env = append(
			pod.Spec.Containers[1].Env,
			v1.EnvVar{Name: "FRPS_TOKEN", Value: c.cfg.FRPSToken},
		)
	}
	return pod
}
