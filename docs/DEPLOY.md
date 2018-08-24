# Deploy

Steps to reproduce a working Kubernetes cluster in the cloud with AllSpark

> **Important:** this is an experimental project and is under development!
> The instructions assume the use of Container Linux as the Operating System.

### Cloud Requirements

- 1 instance to host master control plane (cloud)
  * API Server exposed public serving on a secure port
- 1 instance to expose tunnels (cloud)
  * Firewall rules allowing TCP ports from 20000-21000 to internet

## Requirements

- kubeadm to boostrap master and nodes
- Kubernetes v1.10.X (but it should work also on v1.11.X also)
- [Calico](../deploy/cni-calico.yml)
- [CoreDNS as DaemonSet](../deploy/corends.yml)

All components must connect to the public api server, only change the `api.lab.acme.org`
to the name of your public api server.

## Setup - Master Control Plane

Some modifications are required to bootstrap the master control plane,
the kubeadm config below could be used as a reference:

```yaml
# /etc/kubernetes/kubeadm.cfg
apiVersion: kubeadm.k8s.io/v1alpha1
kind: MasterConfiguration
api:
  controlPlaneEndpoint: api.lab.acme.org
  advertiseAddress: <master-ip-address>
  bindPort: 443
authorizationModes:
  - Node
  - RBAC
certificatesDir: /etc/kubernetes/pki
apiServerCertSANs:
  - api.lab.acme.org
apiServerExtraArgs:
  admission-control: NamespaceLifecycle,LimitRanger,ServiceAccount,PersistentVolumeLabel,DefaultStorageClass,DefaultTolerationSeconds,NodeRestriction,PodNodeSelector,MutatingAdmissionWebhook,ValidatingAdmissionWebhook,ResourceQuota
  kubelet-preferred-address-types: Hostname,ExternalDNS,ExternalIP
apiServerExtraVolumes:
- name: ca-certs
  hostPath: /usr/share/ca-certificates
  mountPath: /etc/ssl/certs
controllerManagerExtraArgs:
  configure-cloud-routes: 'false'
  address: 0.0.0.0
controllerManagerExtraVolumes:
- name: ca-certs
  hostPath: /usr/share/ca-certificates
  mountPath: /etc/ssl/certs
schedulerExtraArgs:
  address: 0.0.0.0
etcd:
  endpoints: null
  caFile: ""
  certFile: ""
  keyFile: ""
  dataDir: /var/lib/etcd
imageRepository: k8s.gcr.io
kubernetesVersion: v1.10.7
networking:
  dnsDomain: cluster.local
  podSubnet: 192.168.0.0/16
  serviceSubnet: 10.96.0.0/12
kubeProxy:
  config:
    mode: ipvs
    ipvs:
      scheduler: sed
nodeName: ip-172-20-101-254.us-east-2.compute.internal
unifiedControlPlaneImage: ""
featureGates:
  CoreDNS: true
```

### PodNodeSelector admission Controller

Each pod will be scheduled respecting node selectors

```yaml
apiVersion: v1
kind: Namespace
metadata:
  annotations:
    scheduler.alpha.kubernetes.io/node-selector: allspark.sh/tenant=acme
  name: office
```

The pods on this namespaces will only be scheduled on nodes that have the selector `allspark.sh/tenant=acme`

### kubelet-preferred-address-types option

Use the hostname to register the node, all nodes must be registered using the suffix:
`<node-name>.allspark.svc.cluster.local` this address will be resolved by the api server when
executing `kubectl <logs|port-forward|exec> <pod>`

### Kube-Proxy IPVS

Using the ipvs scheduler `sed` to solve reaching to the right coredns instance

### API Server POD DNS

The api-server needs to use the internal DNS to be able to tunnel
logs, port-forward and exec commands.

```conf
# /etc/resolv.conf
nameserver <cluster-dns-ip>
```

# Deploying master and node (cloud)

1) Bootstrap a master control plane on the cloud (`kubeadm init --config /etc/kubernetes/kubeadm.cfg`)
2) Create one node to join the cluster on the cloud (kubeadm join)

> [More Info](https://kubernetes.io/docs/setup/independent/install-kubeadm/)

3) Deploy the AllSpark Controller

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: allspark
---
apiVersion: rbac.authorization.k8s.io/v1beta1
kind: ClusterRole
metadata:
  name: allspark-clusterrole
rules:
  - apiGroups:
      - ""
    resources:
      - pods
      - namespaces
      - nodes
    verbs:
      - list
      - watch
      - get
  - apiGroups:
      - ""
    resources:
      - pods
    verbs:
      - create
      - patch
      - update
  - apiGroups:
      - ""
    resources:
      - nodes
    verbs:
      - get
  - apiGroups:
      - ""
    resources:
      - services
    verbs:
      - create
      - patch
      - update
      - get
      - list
      - watch
  - apiGroups:
      - "extensions"
    resources:
      - ingresses
    verbs:
      - get
      - list
      - watch
  - apiGroups:
      - ""
    resources:
        - events
    verbs:
        - create
        - patch
  - apiGroups:
      - "extensions"
    resources:
      - ingresses/status
    verbs:
      - update
---
apiVersion: rbac.authorization.k8s.io/v1beta1
kind: ClusterRoleBinding
metadata:
  name: allspark-binding
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: allspark-clusterrole
subjects:
  - kind: ServiceAccount
    name: default
    namespace: allspark
---
apiVersion: rbac.authorization.k8s.io/v1beta1
kind: ClusterRole
metadata:
  name: allspark-public
rules:
  - apiGroups:
      - ""
    resources:
      - services
    verbs:
      - get
      - watch
      - list
    resourceNames:
      - ini-server
      - valhala
---
apiVersion: rbac.authorization.k8s.io/v1beta1
kind: RoleBinding
metadata:
  name: allspark-public
  namespace: kube-public
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: allspark-public
subjects:
- kind: Group
  name: system:serviceaccounts
  apiGroup: rbac.authorization.k8s.io
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: as-controller
  namespace: allspark
spec:
  selector:
    matchLabels:
      app: as-controller
  template:
    metadata:
      labels:
        app: as-controller
    spec:
      containers:
      - name: as-controller
        image: quay.io/sandromello/allspark:v0.0.1-rc.2
        command:
          - /usr/local/bin/allspark-controller-manager
          - --public-master-url=<api-server-public-address>
          - --frps-address=<frps-public-address>
          - --image=quay.io/sandromello/allspark:v0.0.1-rc.2
          - --node-ip=<private-ipv4>
          - --logtostderr
          - --v=2
        ports:
        - name: http
          containerPort: 3500
          protocol: TCP
        env:
        - name: POD_NAMESPACE
          valueFrom:
            fieldRef:
              apiVersion: v1
              fieldPath: metadata.namespace
---
apiVersion: v1
kind: Service
metadata:
  name: frpc-ini-server
  namespace: allspark
spec:
  selector:
    app: as-controller
  ports:
    - name: http
      port: 80
      targetPort: 3500
      protocol: TCP
```

- public-master-url option is the public kubernetes api server
- frps-address option is your cloud-node instance public address
- node-ip option is the ip of your node to expose ports (20000-21000)

4) Configure the public routes to `ini-server` and `valhala` services in `kube-public` namespace

- The **ini-server** is an API that reads ingress resources and converts to FRPC ini files. It must be public accessible if you wish to expose your local apps to the internet.

> You could use an ingress to expose it. If the port of the service is 443 than the discovery will assume that's a secure connection

- The **valhala** service it's your tunnel server (node) running frp for each tenant

```yaml
apiVersion: v1
kind: Service
metadata:
  name: ini-server
  namespace: kube-public
spec:
  type: ExternalName
  externalName: <ini-server-public-address> # respond to frps operator
  ports:
    - name: http
      port: 443
      protocol: TCP
---
apiVersion: v1
kind: Service
metadata:
  name: valhala
  namespace: kube-public
spec:
  type: ExternalName
  externalName: <frps-public-address>
```

5) Create a new "tenant"

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: office
  annotations:
    scheduler.alpha.kubernetes.io/node-selector: allspark.sh/tenant=acme
  labels:
    allspark.sh/tenant: acme
```

The controller will create a new frps pod named `acme` on your `cloud-node` instance and expose
a service with 3 random ports:

- FRPS server address
- Vhost HTTP
- Vhost HTTPS

6) Deploy the FRPC Kubelet Daemon Set, it will create a tunnel for each local node

The frpc kubelet discover the address using the `valhala` service and then connect with the
FRPS address of your tenant.

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: frpc-kubelet
  namespace: allspark
  labels:
    app: frpc-kubelet
spec:
  selector:
    matchLabels:
      name: frpc-kubelet
  template:
    metadata:
      labels:
        name: frpc-kubelet
    spec:
      nodeSelector:
        node-role.kubernetes.io/on-prem: ''
      tolerations:
      - key: node-role.kubernetes.io/master
        effect: NoSchedule
      containers:
      - name: frpc
        image: quay.io/sandromello/allspark:v0.0.1-rc.2
        command:
          - /usr/local/bin/frpc
          - -c
          - /etc/frpc/frpc.ini
        resources:
          limits:
            memory: 200Mi
            cpu: 200m
          requests:
            cpu: 50m
            memory: 100Mi
        volumeMounts:
        - mountPath: /etc/frpc/
          name: frpc-ini
          readOnly: true
      - name: sync
        image: quay.io/sandromello/allspark:v0.0.1-rc.2
        command:
          - /usr/local/bin/ini-sync
          - --frpc-ini=/etc/frpc/frpc.ini
          - --sync=Kubelet
          - --logtostderr
        resources:
          limits:
            memory: 200Mi
            cpu: 200m
          requests:
            cpu: 50m
            memory: 100Mi
        env:
        - name: POD_NODE_NAME
          valueFrom:
            fieldRef:
              fieldPath: spec.nodeName
        - name: POD_NAMESPACE
          valueFrom:
            fieldRef:
              fieldPath: metadata.namespace
        - name: POD_HOST_IP
          valueFrom:
            fieldRef:
              fieldPath: status.hostIP
        - name: KUBERNETES_SERVICE_HOST
          value: <YOUR_PUBLIC_API_SERVER_HOST>
        volumeMounts:
        - mountPath: /etc/frpc/
          name: frpc-ini
      volumes:
      - name: frpc-ini
        emptyDir: {}
      terminationGracePeriodSeconds: 30
```

- Change the `KUBERNETES_SERVICE_HOST` env to your public api server host

6) Configure your local machine/server (vagrant on your local notebook/desktop)

```bash
vagrant plugin install vagrant-ignition
git clone https://github.com/sparkcorp/box.git
cd box
# Edit VagrantFile and change the $public_dns_suffix variable to <your-node>.allspark.svc.cluster.local
ct --platform=vagrant-virtualbox < cl.conf > config.ign
vagrant up
vagrant ssh
```

7) Join your node to master

```bash
# On Master: copy the output of the command
kubeadm token create --print-join-command

# On your vagrant/server
kubeadm join (...)
```

8) Once you have pods running on your local node, try to check the logs in your master using `kubectl`

```bash
kubectl logs frpc-kubelet -n office -c frpc
```

> The frpc-kubelet pod must be running in order for this to work!

9) Expose a local app in your vagrant box

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: http-svc
  namespace: prod
spec:
  replicas: 3
  selector:
    matchLabels:
      app: http-svc
  template:
    metadata:
      labels:
        app: http-svc
    spec:
      containers:
      - name: http-svc
        image: gcr.io/google_containers/echoserver:1.8
        ports:
        - containerPort: 8080
        env:
          - name: NODE_NAME
            valueFrom:
              fieldRef:
                fieldPath: spec.nodeName
          - name: POD_NAME
            valueFrom:
              fieldRef:
                fieldPath: metadata.name
          - name: POD_NAMESPACE
            valueFrom:
              fieldRef:
                fieldPath: metadata.namespace
          - name: POD_IP
            valueFrom:
              fieldRef:
                fieldPath: status.podIP
---
apiVersion: v1
kind: Service
metadata:
  name: http-svc
  namespace: prod
  labels:
    app: http-svc
spec:
  ports:
  - port: 80
    targetPort: 8080
    protocol: TCP
    name: http
  selector:
    app: http-svc
---
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  name: foo-bar
  namespace: office
  annotations:
    kubernetes.io/ingress.class: "frp"
spec:
  rules:
  - host: foo.bar
    http:
      paths:
      - path: /
        backend:
          serviceName: http-svc
          servicePort: 80
```

Then try to reach through the tunnel

```bash
curl http://<your-cloud-node>:<frps-vhost-http-port> -H 'Host: foo.bar'
```
