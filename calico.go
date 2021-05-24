package main

import (
	"encoding/json"
	"fmt"
	"github.com/Microsoft/hcsshim/hcn"
	"net"
	"net/http"
	"os/exec"
	"regexp"
	"time"
)

func deleteAllNetworksOnNodeRestart(backend string) error {
	backends := map[string]bool{
		"windows-bgp": true,
		"vxlan":       true,
	}

	if backends[backend] {
		networks, err := hcn.ListNetworks()
		if err != nil {
			return err
		}

		for _, n := range networks {
			if n.Name != "nat" {
				// TODO: How to handle making sure it deleted
				n.Delete()
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

	fmt.Println(good)
	return nil
}

func checkIfNetworkExists(n string) bool {
	_, err := hcn.GetNetworkByName(n)
	if err != nil {
		return false
	}
	return true
}

func createFirewallRule() error {
	args := []string{
		"New-NetFirewallRule",
		"-Name OverlayTraffic4789UDP",
		"-Description \"Overlay network traffic UDP\"",
		"-Action Allow",
		"-LocalPort 4789",
		"-Enabled True",
		"-DisplayName \"Overlay Traffic 4789 UDP\"",
		"-Protocol UDP",
		"-ErrorAction SilentlyContinue\"",
	}
	cmd := exec.Command("powershell", args...)
	return cmd.Run()
}

func createExternalNetwork(backend string) {
	for !(checkIfNetworkExists("External")) {
		var network hcn.HostComputeNetwork
		if backend == "vxlan" {
			createFirewallRule()
			network = hcn.HostComputeNetwork{
				Type: hcn.Overlay,
				Name: "External",
				Ipams: []hcn.Ipam{
					{
						Subnets: []hcn.Subnet{
							{
								IpAddressPrefix: "192.168.255.0/30",
							},
							{
								IpAddressPrefix: "192.168.255.1",
								Policies: []json.RawMessage{
									[]byte("{ Type = \"VSID\", VSID = 9999 }"),
								},
							},
						},
					},
				},
			}
		} else {
			network = hcn.HostComputeNetwork{
				Type: hcn.L2Bridge,
				Name: "External",
				Ipams: []hcn.Ipam{
					{
						Subnets: []hcn.Subnet{
							{
								IpAddressPrefix: "192.168.255.0/30",
							},
							{
								IpAddressPrefix: "192.168.255.1",
							},
						},
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
	aksNet, _ := hcn.GetNetworkByName("azure")
	if aksNet != nil {
		return "aks"
	}

	eksNet, _ := hcn.GetNetworkByName("vpcbr*")
	if eksNet != nil {
		return "eks"
	}

	// EC2
	ec2Resp, _ := http.Get("http://169.254.169.254/latest/meta-data/local-hostname")
	if ec2Resp != nil {
		defer ec2Resp.Body.Close()
		return "ec2"
	}

	// GCE
	client := &http.Client{}
	req, _ := http.NewRequest("GET", "http://metadata.google.internal/computeMetadata/v1/instance/hostname", nil)
	req.Header.Add("Metadata-Flavor", "Google")
	gceResp, _ := client.Do(req)
	if gceResp != nil {
		defer gceResp.Body.Close()
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