package driver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"

	Log "github.com/Sirupsen/logrus"
	docker "github.com/fsouza/go-dockerclient"

	"github.com/gorilla/mux"
	"github.com/vishvananda/netlink"
)

const (
	MethodReceiver = "NetworkDriver"
)

type Driver interface {
	//SetNameserver(string) error
	Listen(net.Listener) error
}

type driver struct {
	client  *docker.Client
	version string
	network string
	//nameserver string
}

func New(version string) (Driver, error) {
	client, err := docker.NewClient("unix:///var/run/docker.sock")
	if err != nil {
		return nil, fmt.Errorf("could not connect to docker: %s", err)
	}

	return &driver{
		client:  client,
		version: version,
	}, nil
}

/*func (driver *driver) SetNameserver(nameserver string) error {
	if net.ParseIP(nameserver) == nil {
		return fmt.Errorf(`cannot parse IP address "%s"`, nameserver)
	}
	driver.nameserver = nameserver
	return nil
}*/

func (driver *driver) Listen(socket net.Listener) error {
	router := mux.NewRouter()
	router.NotFoundHandler = http.HandlerFunc(notFound)

	router.Methods("GET").Path("/status").HandlerFunc(driver.status)
	router.Methods("POST").Path("/Plugin.Activate").HandlerFunc(driver.handshake)

	handleMethod := func(method string, h http.HandlerFunc) {
		router.Methods("POST").Path(fmt.Sprintf("/%s.%s", MethodReceiver, method)).HandlerFunc(h)
	}

	handleMethod("GetCapabilities", driver.getCapabilities)
	handleMethod("CreateNetwork", driver.createNetwork)
	handleMethod("DeleteNetwork", driver.deleteNetwork)
	handleMethod("CreateEndpoint", driver.createEndpoint)
	handleMethod("DeleteEndpoint", driver.deleteEndpoint)
	handleMethod("EndpointOperInfo", driver.infoEndpoint)
	handleMethod("Join", driver.joinEndpoint)
	handleMethod("Leave", driver.leaveEndpoint)

	return http.Serve(socket, router)
}

func notFound(w http.ResponseWriter, r *http.Request) {
	Log.Warningf("[plugin] Not found: %+v", r)
	http.NotFound(w, r)
}

func sendError(w http.ResponseWriter, msg string, code int) {
	Log.Errorf("%d %s", code, msg)
	http.Error(w, msg, code)
}

func errorResponsef(w http.ResponseWriter, fmtString string, item ...interface{}) {
	json.NewEncoder(w).Encode(map[string]string{
		"Err": fmt.Sprintf(fmtString, item...),
	})
}

func objectResponse(w http.ResponseWriter, obj interface{}) {
	if err := json.NewEncoder(w).Encode(obj); err != nil {
		sendError(w, "Could not JSON encode response", http.StatusInternalServerError)
		return
	}
}

func emptyResponse(w http.ResponseWriter) {
	json.NewEncoder(w).Encode(map[string]string{})
}

// === protocol handlers

type handshakeResp struct {
	Implements []string
}

func (driver *driver) handshake(w http.ResponseWriter, r *http.Request) {
	err := json.NewEncoder(w).Encode(&handshakeResp{
		[]string{"NetworkDriver"},
	})
	if err != nil {
		sendError(w, "encode error", http.StatusInternalServerError)
		Log.Error("handshake encode:", err)
		return
	}
	Log.Infof("Handshake completed")
}

func (driver *driver) status(w http.ResponseWriter, r *http.Request) {
	io.WriteString(w, fmt.Sprintln("pgrid plugin", driver.version))
}

type getcapabilitiesResp struct {
	Scope string
}

func (driver *driver) getCapabilities(w http.ResponseWriter, r *http.Request) {
	err := json.NewEncoder(w).Encode(&getcapabilitiesResp{
		"global",
	})
	if err != nil {
		Log.Fatal("get capability encode:", err)
		sendError(w, "encode error", http.StatusInternalServerError)
		return
	}
	Log.Infof("Get Capability completed")
}

type networkCreate struct {
	NetworkID string
	Options   map[string]interface{}
}

func (driver *driver) createNetwork(w http.ResponseWriter, r *http.Request) {
	var create networkCreate
	err := json.NewDecoder(r.Body).Decode(&create)
	if err != nil {
		sendError(w, "Unable to decode JSON payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	Log.Debugf("Create network request %+v", &create)

	if driver.network != "" {
		errorResponsef(w, "You get just one network, and you already made %s", driver.network)
		return
	}

	driver.network = create.NetworkID

	emptyResponse(w)
	Log.Infof("Create network %s", driver.network)
}

type networkDelete struct {
	NetworkID string
}

func (driver *driver) deleteNetwork(w http.ResponseWriter, r *http.Request) {
	var delete networkDelete
	if err := json.NewDecoder(r.Body).Decode(&delete); err != nil {
		sendError(w, "Unable to decode JSON payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	Log.Debugf("Delete network request: %+v", &delete)
	if delete.NetworkID != driver.network {
		errorResponsef(w, "Network %s not found", delete.NetworkID)
		return
	}
	driver.network = ""

	emptyResponse(w)
	Log.Infof("Destroy network %s", delete.NetworkID)
}

type endpointCreate struct {
	NetworkID  string
	EndpointID string
	Interfaces []*iface
	Options    map[string]interface{}
}

type iface struct {
	ID         int
	SrcName    string
	DstPrefix  string
	Address    string
	MacAddress string
}

type endpointResponse struct {
	Interfaces []*iface
}

func (driver *driver) createEndpoint(w http.ResponseWriter, r *http.Request) {
	var create endpointCreate
	if err := json.NewDecoder(r.Body).Decode(&create); err != nil {
		sendError(w, "Unable to decode JSON payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	Log.Debugf("Create endpoint request %+v", &create)
	netID := create.NetworkID
	endID := create.EndpointID

	if netID != driver.network {
		errorResponsef(w, "No such network %s", netID)
		return
	}

	ip, err := driver.allocateIP(endID)
	if err != nil {
		Log.Warningf("Error allocating IP: %s", err)
		sendError(w, "Unable to allocate IP", http.StatusInternalServerError)
		return
	}
	Log.Debugf("Got IP from IPAM %s", ip.String())

	mac := makeMac(ip.IP)

	respIface := &iface{
		Address:    ip.String(),
		MacAddress: mac,
	}
	resp := &endpointResponse{
		Interfaces: []*iface{respIface},
	}

	objectResponse(w, resp)
	Log.Infof("Create endpoint %s %+v", endID, resp)
}

type endpointDelete struct {
	NetworkID  string
	EndpointID string
}

func (driver *driver) deleteEndpoint(w http.ResponseWriter, r *http.Request) {
	var delete endpointDelete
	if err := json.NewDecoder(r.Body).Decode(&delete); err != nil {
		sendError(w, "Could not decode JSON encode payload", http.StatusBadRequest)
		return
	}
	Log.Debugf("Delete endpoint request: %+v", &delete)
	emptyResponse(w)
	if err := driver.releaseIP(delete.EndpointID); err != nil {
		Log.Warningf("error releasing IP: %s", err)
	}
	Log.Infof("Delete endpoint %s", delete.EndpointID)
}

type endpointInfoReq struct {
	NetworkID  string
	EndpointID string
}

type endpointInfo struct {
	Value map[string]interface{}
}

func (driver *driver) infoEndpoint(w http.ResponseWriter, r *http.Request) {
	var info endpointInfoReq
	if err := json.NewDecoder(r.Body).Decode(&info); err != nil {
		sendError(w, "Could not decode JSON encode payload", http.StatusBadRequest)
		return
	}
	Log.Debugf("Endpoint info request: %+v", &info)
	objectResponse(w, &endpointInfo{Value: map[string]interface{}{}})
	Log.Infof("Endpoint info %s", info.EndpointID)
}

type joinInfo struct {
	InterfaceNames []*iface
	Gateway        string
	GatewayIPv6    string
	HostsPath      string
	ResolvConfPath string
}

type join struct {
	NetworkID  string
	EndpointID string
	SandboxKey string
	Options    map[string]interface{}
}

type staticRoute struct {
	Destination string
	RouteType   int
	NextHop     string
}

type joinResponse struct {
	Gateway        string
	InterfaceNames []*iface
	StaticRoutes   []*staticRoute
}

func (driver *driver) joinEndpoint(w http.ResponseWriter, r *http.Request) {
	var j join
	if err := json.NewDecoder(r.Body).Decode(&j); err != nil {
		sendError(w, "Could not decode JSON encode payload", http.StatusBadRequest)
		return
	}
	Log.Debugf("Join request: %+v", &j)

	endID := j.EndpointID

	// create and attach local name to the bridge
	local := vethPair(endID[:5])
	if err := netlink.LinkAdd(local); err != nil {
		Log.Error(err)
		errorResponsef(w, "could not create veth pair")
		return
	}

	if_local_name := "tap" + endID[:5]

	//getting mac address of tap...
	cmdStr0 := "ifconfig " + if_local_name + " | awk '/HWaddr/ {print $NF}'"
	Log.Infof("mac address cmd: %s", cmdStr0)
	cmd0 := exec.Command("/bin/sh", "-c", cmdStr0)
	var out0 bytes.Buffer
	cmd0.Stdout = &out0
	err0 := cmd0.Run()
	if err0 != nil {
		Log.Error("Error thrown: ", err0)
	}
	mac := out0.String()
	Log.Infof("output of cmd: %s\n", mac)

	//first command {adding port on plumgrid}
	cmdStr1 := "sudo /opt/pg/bin/ifc_ctl gateway add_port " + if_local_name
	Log.Infof("second cmd: %s", cmdStr1)
	cmd1 := exec.Command("/bin/sh", "-c", cmdStr1)
	var out1 bytes.Buffer
	cmd1.Stdout = &out1
	err1 := cmd1.Run()
	if err1 != nil {
		Log.Error("Error thrown: ", err1)
	}
	Log.Infof("output of cmd: %+v\n", out1.String())

	//second command {up the port on plumgrid}
	cmdStr2 := "sudo /opt/pg/bin/ifc_ctl gateway ifup " + if_local_name + " access_vm vm_" + endID[:2] + " " + mac[:17] + " pgtag2=bridge-1 pgtag1=pgrid"
	Log.Infof("third cmd: %s", cmdStr2)
	cmd2 := exec.Command("/bin/sh", "-c", cmdStr2)
	var out2 bytes.Buffer
	cmd2.Stdout = &out2
	err2 := cmd2.Run()
	if err2 != nil {
		Log.Error("Error thrown: ", err2)
	}
	Log.Infof("output of cmd: %+v\n", out2.String())

	if netlink.LinkSetUp(local) != nil {
		errorResponsef(w, `unable to bring veth up`)
		return
	}

	ifname := &iface{
		SrcName:   local.PeerName,
		DstPrefix: "ethpg",
		ID:        0,
	}

	res := &joinResponse{
		InterfaceNames: []*iface{ifname},
	}

	/*if driver.nameserver != "" {
		routeToDNS := &staticRoute{
			Destination: driver.nameserver + "/32",
			RouteType:   types.CONNECTED,
			NextHop:     "",
			InterfaceID: 0,
		}
		res.StaticRoutes = []*staticRoute{routeToDNS}
	}*/

	objectResponse(w, res)
	Log.Infof("Join endpoint %s:%s to %s", j.NetworkID, j.EndpointID, j.SandboxKey)
}

type leave struct {
	NetworkID  string
	EndpointID string
	Options    map[string]interface{}
}

func (driver *driver) leaveEndpoint(w http.ResponseWriter, r *http.Request) {
	var l leave
	if err := json.NewDecoder(r.Body).Decode(&l); err != nil {
		sendError(w, "Could not decode JSON encode payload", http.StatusBadRequest)
		return
	}
	Log.Debugf("Leave request: %+v", &l)

	if_local_name := "tap" + l.EndpointID[:5]

	//getting mac address of tap...
	cmdStr0 := "ifconfig " + if_local_name + " | awk '/HWaddr/ {print $NF}'"
	Log.Infof("mac address cmd: %s", cmdStr0)
	cmd0 := exec.Command("/bin/sh", "-c", cmdStr0)
	var out0 bytes.Buffer
	cmd0.Stdout = &out0
	err0 := cmd0.Run()
	if err0 != nil {
		Log.Error("Error thrown: ", err0)
	}
	mac := out0.String()
	Log.Infof("output of cmd: %s\n", mac)

	//first command {adding port on plumgrid}
	cmdStr1 := "sudo /opt/pg/bin/ifc_ctl gateway ifdown " + if_local_name + " access_vm vm_" + l.EndpointID[:5] + " " + mac[:17]
	Log.Infof("second cmd: %s", cmdStr1)
	cmd1 := exec.Command("/bin/sh", "-c", cmdStr1)
	var out1 bytes.Buffer
	cmd1.Stdout = &out1
	err1 := cmd1.Run()
	if err1 != nil {
		Log.Error("Error thrown: ", err1)
	}
	Log.Infof("output of cmd: %+v\n", out1.String())

	//second command {up the port on plumgrid}
	cmdStr2 := "sudo /opt/pg/bin/ifc_ctl gateway del_port " + if_local_name
	Log.Infof("third cmd: %s", cmdStr2)
	cmd2 := exec.Command("/bin/sh", "-c", cmdStr2)
	var out2 bytes.Buffer
	cmd2.Stdout = &out2
	err2 := cmd2.Run()
	if err2 != nil {
		Log.Error("Error thrown: ", err2)
	}
	Log.Infof("output of cmd: %+v\n", out2.String())

	local := vethPair(l.EndpointID[:5])
	if err := netlink.LinkDel(local); err != nil {
		Log.Warningf("unable to delete veth on leave: %s", err)
	}
	emptyResponse(w)
	Log.Infof("Leave %s:%s", l.NetworkID, l.EndpointID)
}

// ===

func vethPair(suffix string) *netlink.Veth {
	return &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{Name: "tap" + suffix},
		PeerName:  "ns" + suffix,
	}
}

func makeMac(ip net.IP) string {
	hw := make(net.HardwareAddr, 6)
	hw[0] = 0x7a
	hw[1] = 0x42
	copy(hw[2:], ip.To4())
	return hw.String()
}
