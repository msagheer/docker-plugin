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

	"github.com/docker/libnetwork/drivers/remote/api"

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
		"local",
	})
	if err != nil {
		Log.Fatal("get capability encode:", err)
		sendError(w, "encode error", http.StatusInternalServerError)
		return
	}
	Log.Infof("Get Capability completed")
}

// create network call
func (driver *driver) createNetwork(w http.ResponseWriter, r *http.Request) {
	var create api.CreateNetworkRequest
	err := json.NewDecoder(r.Body).Decode(&create)
	if err != nil {
		sendError(w, "Unable to decode JSON payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	Log.Infof("Create network request %+v", &create)

	pgBridgeCreate(create.NetworkID)

	emptyResponse(w)

	Log.Infof("Create network %s", create.NetworkID)
}

// delete network call
func (driver *driver) deleteNetwork(w http.ResponseWriter, r *http.Request) {
	var delete api.DeleteNetworkRequest
	if err := json.NewDecoder(r.Body).Decode(&delete); err != nil {
		sendError(w, "Unable to decode JSON payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	Log.Infof("Delete network request: %+v", &delete)

	pgBridgeDestroy(delete.NetworkID)

	emptyResponse(w)
	Log.Infof("Destroy network %s", delete.NetworkID)
}

// create endpoint call
func (driver *driver) createEndpoint(w http.ResponseWriter, r *http.Request) {
	var create api.CreateEndpointRequest
	if err := json.NewDecoder(r.Body).Decode(&create); err != nil {
		sendError(w, "Unable to decode JSON payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	Log.Infof("Create endpoint request %+v", &create)
	Log.Infof("Create endpoint request %+v", create)

	endID := create.EndpointID

	ip := create.Interface.Address
	Log.Infof("Got IP from IPAM %s", ip)

	ip_mac, ipnet_mac, err_mac := net.ParseCIDR(ip)
	if err_mac == nil {
		ipnet_mac.IP = ip_mac
	}
	mac := makeMac(ipnet_mac.IP)

	respIface := &api.EndpointInterface{
		MacAddress: mac,
	}
	resp := &api.CreateEndpointResponse{
		Interface: respIface,
	}

	objectResponse(w, resp)
	Log.Infof("Create endpoint %s %+v", endID, resp)
}

// delete endpoint call
func (driver *driver) deleteEndpoint(w http.ResponseWriter, r *http.Request) {
	var delete api.DeleteEndpointRequest
	if err := json.NewDecoder(r.Body).Decode(&delete); err != nil {
		sendError(w, "Could not decode JSON encode payload", http.StatusBadRequest)
		return
	}
	Log.Debugf("Delete endpoint request: %+v", &delete)
	emptyResponse(w)
	Log.Infof("Delete endpoint %s", delete.EndpointID)
}

// endpoint info request
func (driver *driver) infoEndpoint(w http.ResponseWriter, r *http.Request) {
	var info api.EndpointInfoRequest
	if err := json.NewDecoder(r.Body).Decode(&info); err != nil {
		sendError(w, "Could not decode JSON encode payload", http.StatusBadRequest)
		return
	}
	Log.Infof("Endpoint info request: %+v", &info)
	objectResponse(w, &api.EndpointInfoResponse{Value: map[string]interface{}{}})
	Log.Infof("Endpoint info %s", info.EndpointID)
}

// join call
func (driver *driver) joinEndpoint(w http.ResponseWriter, r *http.Request) {
	var j api.JoinRequest
	if err := json.NewDecoder(r.Body).Decode(&j); err != nil {
		sendError(w, "Could not decode JSON encode payload", http.StatusBadRequest)
		return
	}
	Log.Infof("Join request: %+v", &j)

	netID := j.NetworkID
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
	cmdStr2 := "sudo /opt/pg/bin/ifc_ctl gateway ifup " + if_local_name + " access_vm vm_" + endID[:2] + " " + mac[:17] + " pgtag2=bridge-" + netID[:10] + " pgtag1=" + pgtag1
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

	ifname := &api.InterfaceName{
		SrcName:   local.PeerName,
		DstPrefix: "ethpg",
	}

	res := &api.JoinResponse{
		InterfaceName: ifname,
	}

	/*if driver.nameserver != "" {
		routeToDNS := &api.StaticRoute{
			Destination: driver.nameserver + "/32",
			RouteType:   types.CONNECTED,
			NextHop:     "",
			InterfaceID: 0,
		}
		res.StaticRoutes = []Rpi.StaticRoute{routeToDNS}
	}*/

	objectResponse(w, res)
	Log.Infof("Join endpoint %s:%s to %s", j.NetworkID, j.EndpointID, j.SandboxKey)
}

// leave call
func (driver *driver) leaveEndpoint(w http.ResponseWriter, r *http.Request) {
	var l api.LeaveRequest
	if err := json.NewDecoder(r.Body).Decode(&l); err != nil {
		sendError(w, "Could not decode JSON encode payload", http.StatusBadRequest)
		return
	}
	Log.Infof("Leave request: %+v", &l)

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
