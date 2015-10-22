package driver

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/cookiejar"

	Log "github.com/Sirupsen/logrus"
)

func (d *driver) plumgridBridge(ID string) {
	cookieJar, _ := cookiejar.New(nil)

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{
		Jar:       cookieJar,
		Transport: tr,
	}

	url := "https://192.168.122.100/0/login"
	Log.Infof("URL:> %s", url)

	var jsonStr = []byte(`{"userName":"plumgrid", "password":"plumgrid"}`)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		fmt.Println(err)
	}
	defer resp.Body.Close()

	fmt.Println("response Status:", resp.Status)
	fmt.Println("response Headers:", resp.Header)
	fmt.Println("response Cookies:", resp.Cookies())
	body, _ := ioutil.ReadAll(resp.Body)
	fmt.Println("response Body:", string(body))

	//== second call GET

	/*url1 := "https://192.168.122.100/0/connectivity/domain"
	fmt.Println("URL:>", url1)

	req1, err := http.NewRequest("GET", url1, nil)
	req1.Header.Set("Accept", "application/json")
	req1.Header.Set("Content-Type", "application/json")

	resp1, err1 := client.Do(req1)
	if err1 != nil {
		fmt.Println(err1)
	}
	defer resp1.Body.Close()

	fmt.Println("response Status:", resp1.Status)
	fmt.Println("response Headers:", resp1.Header)
	body1, _ := ioutil.ReadAll(resp1.Body)
	fmt.Println("response Body:", string(body1))*/

	//== POST call

	url2 := "https://192.168.122.100/0/connectivity/domain/admin"
	fmt.Println("URL:>", url2)

	var jsonStr1 = []byte(`{
		"container_group": "admin",
		"link": {},
		"ne": {
			"bri3c3570d6-5320-1789-b54w-9984598d5a46": {
				"DynamicMacAddressTable": {},
				"HostTable": {},
				"StaticMacAddressTable": {},
				"action": {
					"action1": {
						"action_text": "create_and_link_ifc(DYN_1)"
					}
				},
				"config_template": "",
				"ifc": {},
				"mobility": "true",
				"ne_description": "PLUMgrid Bridge",
				"ne_dname": "bridge-1",
				"ne_group": "Bridge",
				"ne_type": "bridge",
				"ne_version": "9.32.3        ",
				"number_interfaces": 0,
				"position_x": "201",
				"position_y": "68",
				"rate_limiter": {}
			}
		},
		"properties": {
			"position_x": null,
			"position_y": null,
			"rule_group": {
				"cnfc3af87cd-bf75-4cd1-bbc5-c815fa4d22ac": {
					"mark_disabled": false,
					"ne_dest": "/ne/bri3c3570d6-5320-1789-b54w-9984598d5a46/action/action1",
					"ne_dname": "cnf-vmgroup-2",
					"ne_type": "cnf-vmgroup",
					"position_x": "375",
					"position_y": "136",
					"rule": {
						"rules96d8f496c6234e0f883c8458c4f09dc5": {
							"add_context": "",
							"criteria": "pgtag2",
							"match": "bridge-1"
						}
					}
				}
			}
		},
		"topology_name": "admin"
	}`)

	req2, err := http.NewRequest("POST", url2, bytes.NewBuffer(jsonStr1))
	req2.Header.Set("Accept", "application/json")
	req2.Header.Set("Content-Type", "application/json")

	resp2, err2 := client.Do(req2)
	if err2 != nil {
		fmt.Println(err2)
	}
	defer resp2.Body.Close()

	fmt.Println("response Status:", resp2.Status)
	fmt.Println("response Headers:", resp2.Header)
	body2, _ := ioutil.ReadAll(resp2.Body)
	fmt.Println("response Body:", string(body2))

}
