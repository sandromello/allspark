package controller

import (
	"time"

	"github.com/golang/glog"
	extensions "k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

var KeyFunc = cache.DeletionHandlingMetaNamespaceKeyFunc

const (
	// ingressClassKey picks a specific "class" for the Ingress. The controller
	// only processes Ingresses with this annotation either unset, or set
	// to either gceIngessClass or the empty string.
	ingressClassKey = "kubernetes.io/ingress.class"
	frpIngressClass = "frp"
)

// ingAnnotations represents Ingress annotations.
type ingAnnotations map[string]string

func (ing ingAnnotations) ingressClass() string {
	val, ok := ing[ingressClassKey]
	if !ok {
		return ""
	}
	return val
}

// isFrpIngress returns true if the given Ingress either doesn't specify the
// ingress.class annotation, or it's set to "frp".
func isFrpIngress(ing *extensions.Ingress) bool {
	class := ingAnnotations(ing.ObjectMeta.Annotations).ingressClass()
	return class == "" || class == frpIngressClass
}

// isAllSparkResource returns true if the given namespace has a specific Label
func isAllSparkResource(meta metav1.Object) bool {
	labels := meta.GetLabels()
	if labels == nil {
		return false
	}
	if labels["allspark.sh/tenant"] != "" {
		return true
	}
	return false
}

// TaskQueue manages a work queue through an independent worker that
// invokes the given sync function for every work item inserted.
type TaskQueue struct {
	// queue is the work queue the worker polls
	queue workqueue.RateLimitingInterface
	// sync is called for each item in the queue
	sync func(string) error
	// workerDone is closed when the worker exits
	workerDone chan struct{}
}

func (t *TaskQueue) run(period time.Duration, stopCh <-chan struct{}) {
	wait.Until(t.runWorker, period, stopCh)
}

// Len retrieves the lenght of the queue
func (t *TaskQueue) Len() int { return t.queue.Len() }

// Add enqueues ns/name of the given api object in the task queue.
func (t *TaskQueue) Add(obj interface{}) {
	key, err := KeyFunc(obj)
	if err != nil {
		glog.Infof("Couldn't get key for object %+v: %v", obj, err)
		return
	}
	t.queue.Add(key)
}

func (t *TaskQueue) runWorker() {
	for {
		// hot loop until we're told to stop.  processNextWorkItem will automatically
		// wait until there's work available, so we don't worry about secondary waits
		t.processNextWorkItem()
	}
}

// worker processes work in the queue through sync.
func (t *TaskQueue) processNextWorkItem() {
	key, quit := t.queue.Get()
	if quit {
		close(t.workerDone)
		return
	}
	if key == nil {
		return
	}
	glog.V(2).Infof("Syncing %v", key)
	if err := t.sync(key.(string)); err != nil {
		glog.V(2).Infof("Requeuing %v, err: %v", key, err)
		t.queue.AddRateLimited(key)
	} else {
		t.queue.Forget(key)
	}
	t.queue.Done(key)
}

// shutdown shuts down the work queue and waits for the worker to ACK
func (t *TaskQueue) Shutdown() {
	t.queue.ShutDown()
	<-t.workerDone
}

// NewTaskQueue creates a new task queue with the given sync function.
// The sync function is called for every element inserted into the queue.
func NewTaskQueue(queueName string, syncFn func(string) error) *TaskQueue {
	rateLimitQueue := workqueue.NewNamedRateLimitingQueue(
		workqueue.DefaultControllerRateLimiter(),
		queueName,
	)
	return &TaskQueue{
		queue:      rateLimitQueue,
		sync:       syncFn,
		workerDone: make(chan struct{}),
	}
}
