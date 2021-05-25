package main

import (
	"encoding/json"
	"fmt"
	"github.com/Microsoft/hcsshim"
	"github.com/google/gopacket/routing"
	wapi "github.com/iamacarpet/go-win64api"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	cniConf = `{
  "name": "Calico",
  "windows_use_single_network": true,

  "cniVersion": "0.3.1",
  "type": "calico",
  "mode": "__MODE__",

  "vxlan_mac_prefix":  "__MAC_PREFIX__",
  "vxlan_vni": __VNI__,

  "policy": {
    "type": "k8s"
  },

  "log_level": "info",

  "capabilities": {"dns": true},

  "DNS":  {
    "Nameservers":  [__DNS_NAME_SERVERS__],
    "Search":  [
      "svc.cluster.local"
    ]
  },

  "nodename_file": "__NODENAME_FILE__",

  "datastore_type": "__DATASTORE_TYPE__",

  "etcd_endpoints": "__ETCD_ENDPOINTS__",
  "etcd_key_file": "__ETCD_KEY_FILE__",
  "etcd_cert_file": "__ETCD_CERT_FILE__",
  "etcd_ca_cert_file": "__ETCD_CA_CERT_FILE__",

  "kubernetes": {
    "kubeconfig": "__KUBECONFIG__"
  },

  "ipam": {
    "type": "__IPAM_TYPE__",
    "subnet": "usePodCidr"
  },

  "policies":  [
    {
      "Name":  "EndpointPolicy",
      "Value":  {
        "Type":  "OutBoundNAT",
        "ExceptionList":  [
          "__K8S_SERVICE_CIDR__"
        ]
      }
    },
    {
      "Name":  "EndpointPolicy",
      "Value":  {
        "Type":  "SDNROUTE",
        "DestinationPrefix":  "__K8S_SERVICE_CIDR__",
        "NeedEncap":  true
      }
    }
  ]}

`
)

func createCniConfg(config CalicoConfig) error {
	conf := cniConf
	if config.cni.confDir == "" {
		return nil
	}
	p := filepath.Join(config.cni.confDir, config.cni.confFileName)
	strings.Replace(string(conf), "__NODENAME_FILE__", config.nodeNameFile, 1)
	strings.Replace(string(conf), "__KUBECONFIG__", config.kubeConfig, 1)
	strings.Replace(string(conf), "__K8S_SERVICE_CIDR__", config.serviceCidr, 1)
	strings.Replace(string(conf), "__DNS_NAME_SERVERS__", config.dnsServers, 1)
	strings.Replace(string(conf), "__DATASTORE_TYPE__", config.datastoreType, 1)
	strings.Replace(string(conf), "__IPAM_TYPE__", config.cni.ipamType, 1)
	strings.Replace(string(conf), "__MODE__", config.networkingBackend, 1)
	strings.Replace(string(conf), "__VNI__", config.felix.vxlanvni, 1)
	strings.Replace(string(conf), "__MAC_PREFIX__", config.felix.macPrefix, 1)
	return os.WriteFile(p, []byte(conf), os.ModePerm)
}

func deleteAllNetworksOnNodeRestart(backend string) error {
	backends := map[string]bool{
		"windows-bgp": true,
		"vxlan":       true,
	}

	if backends[backend] {
		networks, err := hcsshim.HNSListNetworkRequest("GET", "", "")
		if err != nil {
			return err
		}

		for _, n := range networks {
			if n.Name != "nat" {
				// TODO: How to handle making sure it deleted
				_, err = n.Delete()
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func checkForCorrectInterface() (bool, error) {
	iFaces, err := net.Interfaces()
	if err != nil {
		return false, err
	}

	for _, iFace := range iFaces {
		addrs, err := iFace.Addrs()
		if err != nil {
			return false, err
		}
		for _, addr := range addrs {
			if addr.(*net.IPNet).IP.To4() != nil {
				match1, _ := regexp.Match("(^127\\.0\\.0\\.)", addr.(*net.IPNet).IP)
				match2, _ := regexp.Match("(^169\\.254\\.)", addr.(*net.IPNet).IP)
				if !(match1 || match2) {
					return true, nil
				}
				return false, nil
			}
			return false, nil
		}
	}

	return false, nil
}

func generateCalicoNetworks(config CalicoConfig) error {

	err := deleteAllNetworksOnNodeRestart(config.networkingBackend)
	if err != nil {
		return err
	}
	good, err := checkForCorrectInterface()
	if err != nil {
		return err
	}

	createExternalNetwork(config.networkingBackend)

	// TODO: Figure out this Wait-ForManagementIP Shizzle
	mgmt := waitForManagementIp("External")
	time.Sleep(10 * time.Second)
	platform := getPlatformType()
	if platform == "ec2" || platform == "gce" {
		err := setMetaDataServerRoute(mgmt)
		if err != nil {
			return err
		}
	}
	if config.networkingBackend == "windows-bgp" {
		_ = wapi.StopService("RemoteAccess")
		_ = wapi.StartService("RemoteAccess")
	}

	fmt.Println(good)
	return nil
}

func checkIfNetworkExists(n string) bool {
	_, err := hcsshim.GetHNSNetworkByName(n)
	if err != nil {
		return false
	}
	return true
}

func createExternalNetwork(backend string) {
	for !(checkIfNetworkExists("External")) {
		var network hcsshim.HNSNetwork
		if backend == "vxlan" {

			_, err := wapi.FirewallRuleAdd(
				"OverlayTraffic4789UDP",
				"Overlay network traffic UDP",
				"",
				"4789",
				wapi.NET_FW_IP_PROTOCOL_UDP,
				wapi.NET_FW_PROFILE2_ALL,
			)
			if err != nil {
				// TODO: Something better here
				return
			}

			network = hcsshim.HNSNetwork{
				Type: "Overlay",
				Name: "External",
				Subnets: []hcsshim.Subnet{
					{
						AddressPrefix:  "192.168.255.0/30",
						GatewayAddress: "192.168.255.1",
						Policies: []json.RawMessage{
							[]byte("{ Type = \"VSID\", VSID = 9999 }"),
						},
					},
				},
			}
		} else {
			network = hcsshim.HNSNetwork{
				Type: "L2Bridge",
				Name: "External",
				Subnets: []hcsshim.Subnet{
					{
						AddressPrefix:  "192.168.255.0/30",
						GatewayAddress: "192.168.255.1",
					},
				},
			}
		}
		_, err := network.Create()
		if err != nil {
			time.Sleep(1 * time.Second)
		}
		break
	}
}

func getPlatformType() string {
	// AKS
	aksNet, _ := hcsshim.GetHNSNetworkByName("azure")
	if aksNet != nil {
		return "aks"
	}

	eksNet, _ := hcsshim.GetHNSNetworkByName("vpcbr*")
	if eksNet != nil {
		return "eks"
	}

	// EC2
	ec2Resp, _ := http.Get("http://169.254.169.254/latest/meta-data/local-hostname")
	if ec2Resp != nil {
		defer func(Body io.ReadCloser) {
			_ = Body.Close()
		}(ec2Resp.Body)
		return "ec2"
	}

	// GCE
	client := &http.Client{}
	req, _ := http.NewRequest("GET", "http://metadata.google.internal/computeMetadata/v1/instance/hostname", nil)
	req.Header.Add("Metadata-Flavor", "Google")
	gceResp, _ := client.Do(req)
	if gceResp != nil {
		defer func(Body io.ReadCloser) {
			_ = Body.Close()
		}(gceResp.Body)
		return "gce"
	}

	return "bare-metal"
}

func autoConfigureIpam(it string) string {
	if it == "host-local" {
		return "USE_POD_CIDR=true"
	} else {
		return "USE_POD_CIDR=false"
	}
}

func waitForManagementIp(networkName string) string {
	var mgmt string
	for {
		network, err := hcsshim.GetHNSNetworkByName(networkName)
		if err != nil {
			continue
		}
		mgmt = network.ManagementIP
		break
	}
	return mgmt
}

func setMetaDataServerRoute(mgmt string) error {
	var ip net.IP
	if ip = net.ParseIP(mgmt); ip == nil {
		return fmt.Errorf("Not a valid IP.")
	}

	metaIp := net.ParseIP("169.254.169.254/32")

	router, err := routing.New()
	if err != nil {
		return err
	}

	route, _, preferredSrc, err := router.Route(ip)
	if err != nil {
		return err
	}
	_, _, _, err = router.RouteWithSrc(route.HardwareAddr, preferredSrc, metaIp)
	if err != nil {
		return err
	}
	return nil
}
