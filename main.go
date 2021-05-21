package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/Microsoft/hcsshim/hcn"
)

type FelixConfig struct {
	metadataaddr string
	vxlanvni     string
	ipamType     string
}

type CalicoConfig struct {
	hostname          string
	ipamType          string
	networkingBackend string
	nodeNameFile      string
	platform          string
	felix             FelixConfig
}

func main() {
	hostname, err := os.Hostname()
	if err != nil {
		// What to do?
	}
	calicoCfg := CalicoConfig{
		hostname:          hostname,
		ipamType:          "calico-ipam",
		networkingBackend: "vxlan",
		platform:          getPlatformType(),
		felix: FelixConfig{
			metadataaddr: "none",
			vxlanvni:     "4096",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), (5 * time.Minute))
	defer cancel()

	calicoErr := make(chan error)
	calicoCmd := exec.CommandContext(ctx, "calico-node.exe")
	go startCalico(calicoCfg, calicoErr, calicoCmd)
	felixErr := make(chan error)
	felixCmd := exec.CommandContext(ctx, "calico-node.exe")
	go startFelix(calicoCfg, felixErr, felixCmd)
}

func autoConfigureIpam(it string) string {
	if it == "host-local" {
		return "USE_POD_CIDR=true"
	} else {
		return "USE_POD_CIDR=false"
	}
}

func startFelix(config CalicoConfig, errChan chan error, cmd *exec.Cmd) {

	cmd.Env = []string{
		fmt.Sprintf("FELIX_FELIXHOSTNAME=", config.hostname),
		fmt.Sprintf("FELIX_VXLANVNI=", config.felix.vxlanvni),
		fmt.Sprintf("FELIX_METADATAADDR=", config.felix.metadataaddr),
		fmt.Sprintf("CNI_IPAM_TYPE=", config.ipamType),
		fmt.Sprintf("USE_POD_CIDR=", autoConfigureIpam(config.ipamType)),
	}

	args := []string{
		"-felix",
	}
	cmd.Args = append(cmd.Args, args...)

	logFile, err := os.OpenFile("calico-node.log", os.O_RDWR|os.O_CREATE, 0755)
	if err != nil {
		log.Fatal(err)
	}
	if err := logFile.Close(); err != nil {
		log.Fatal(err)
	}

	errFile, err := os.OpenFile("calico-node.err.log", os.O_RDWR|os.O_CREATE, 0755)
	if err != nil {
		log.Fatal(err)
	}
	if err := errFile.Close(); err != nil {
		log.Fatal(err)
	}

	cmd.Stdout = nil
	cmd.Stderr = nil
	errChan <- cmd.Run()
}

func startCalico(config CalicoConfig, errChan chan error, cmd *exec.Cmd) {
	cmd.Env = []string{
		fmt.Sprintf("CALICO_NODENAME_FILE=", config.nodeNameFile),
		fmt.Sprintf("FELIX_VXLANVNI=", config.felix.vxlanvni),
		fmt.Sprintf("FELIX_METADATAADDR=", config.felix.metadataaddr),
		fmt.Sprintf("CNI_IPAM_TYPE=", config.ipamType),
		fmt.Sprintf("USE_POD_CIDR=", autoConfigureIpam(config.ipamType)),
	}

	args := []string{
		"-startup",
	}
	cmd.Args = append(cmd.Args, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil

	errChan <- cmd.Run()
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
