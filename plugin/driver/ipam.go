package driver

import (
	"net"
	"strconv"
)

var global_counter int

// returns an IP for the ID given, allocating a fresh one if necessary
func (d *driver) allocateIP(ID string) (*net.IPNet, error) {
	ip, ipnet, err := net.ParseCIDR(ipAssigner())
	if err == nil {
		ipnet.IP = ip
	}
	return ipnet, err
}

func ipAssigner() string {
	global_counter = global_counter + 1
	temp := "10.0.0." + strconv.Itoa(global_counter) + "/16"
	return temp
}

// release an IP which is no longer needed
func (d *driver) releaseIP(ID string) error {
	global_counter = global_counter - 1
	return nil
}
