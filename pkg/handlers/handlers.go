package handlers

import (
	"fmt"
	"net/http"
	"os"

	"github.com/golang/glog"
	"github.com/gorilla/mux"
	"github.com/sparkcorp/allspark/pkg/api"
	"github.com/sparkcorp/allspark/pkg/controller"
	"github.com/sparkcorp/allspark/pkg/httputil"
	ini "gopkg.in/ini.v1"
	"k8s.io/apimachinery/pkg/api/errors"
)

const (
	MessageIngressNotFound = "ingress '%s/%s' in work queue no longer exists"
)

type Handler struct {
	ctrl        *controller.ASController
	common      *api.FrpcCommon
	frpsAddress string
	frpsPort    int32
}

func New(ctrl *controller.ASController, common *api.FrpcCommon) *Handler {
	return &Handler{ctrl: ctrl, common: common}
}

func (h *Handler) IngressToIni(w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	namespace, ingressName := params["namespace"], params["name"]
	ing, err := h.ctrl.IngressLister.Ingresses(namespace).Get(ingressName)
	if err != nil {
		if errors.IsNotFound(err) {
			httputil.HttpError(400, "IngressNotFound").
				MessageF(MessageIngressNotFound, namespace, ingressName).
				Write(w)

			return
		}
	}
	// Search for the tenant
	ns, err := h.ctrl.NamespaceLister.Get(params["namespace"])
	if err != nil {
		httputil.HttpError(400, "FetchNamespaceErr").
			MessageF("Failed fetching for namespace %q: %v", params["namespace"], err).
			Write(w)
		return
	}
	var tenant string
	if ns.Labels != nil {
		tenant = ns.Labels["allspark.sh/tenant"]
	}
	if tenant == "" {
		httputil.HttpError(400, "TenantNotFound").
			MessageF("This namespace doesn't have a tenant").
			Write(w)
		return
	}
	svc, err := h.ctrl.ServiceLister.Services(os.Getenv("POD_NAMESPACE")).Get(tenant)
	if err != nil {
		httputil.HttpError(400, "FetchServiceErr").
			MessageF("Failed fetching frps service: %v", err).
			Write(w)
		return
	}
	for _, port := range svc.Spec.Ports {
		if port.Name == "frps" {
			h.common.ServerPort = port.Port
		}
	}
	if h.common.ServerPort == 0 {
		httputil.HttpError(400, "PortNotFound").
			MessageF("Failed finding FRPS port for service %q", svc.Name).
			Write(w)
		return
	}
	var httpSections []api.FprcHTTP
	// TODO: Check if has repeated paths for a given host
	for _, r := range ing.Spec.Rules {
		if r.HTTP == nil {
			continue
		}
		for _, p := range r.HTTP.Paths {
			frpcHTTP := api.FprcHTTP{}
			frpcHTTP.Section = fmt.Sprintf("%s%s", r.Host, p.Path)
			frpcHTTP.Type = "http"
			frpcHTTP.LocalIP = fmt.Sprintf("%s.%s.svc.cluster.local",
				p.Backend.ServiceName,
				ing.Namespace,
			)
			frpcHTTP.LocalPort = p.Backend.ServicePort.IntVal
			frpcHTTP.Locations = p.Path
			frpcHTTP.CustomDomains = r.Host
			if p.Backend.ServicePort.IntVal == 443 {
				frpcHTTP.Type = "https"
			}
			httpSections = append(httpSections, frpcHTTP)
		}
	}
	frpcini := ini.Empty()
	for _, s := range httpSections {
		if _, err := frpcini.NewSection(s.Section); err != nil {
			httputil.HttpError(500, "InvalidIngressSectionErr").
				MessageF("Invalid section: %v", err).
				Write(w)
			return
		}
		if err := frpcini.Section(s.Section).ReflectFrom(&s); err != nil {
			httputil.HttpError(500, "InvalidIngressMappingErr").
				MessageF("Invalid mapping: %v", err).
				Write(w)
			return
		}
	}
	c, err := frpcini.NewSection("common")
	if err != nil {
		httputil.HttpError(500, "InvalidSectionErr").
			MessageF("Failed creating 'common' section: %v", err).
			Write(w)
		return
	}
	if err := c.ReflectFrom(h.common); err != nil {
		httputil.HttpError(500, "InvalidSectionErr").
			MessageF("Failed injecting keys to section 'common': %v", err).
			Write(w)
		return
	}
	if len(frpcini.Sections()) == 0 {
		glog.Warningf("found 0 sections, the ingress resource may have an error")
	}
	if _, err := frpcini.WriteTo(w); err != nil {
		glog.Errorf("failed writing ini file: %v", err)
	}
}
