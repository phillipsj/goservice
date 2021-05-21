package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/Microsoft/hcsshim/hcn"
)

func main() {
	dataDir := ""
	containerdErr := make(chan error)

	ctx, cancel := context.WithTimeout(context.Background(), (5 * time.Minute))
	defer cancel()

	calicoCmd := exec.CommandContext(ctx, "calico-node.exe")
	go startCalico(dataDir, containerdErr, calicoCmd)

	felixCmd := exec.CommandContext(ctx, "calico-node.exe")
	go startFelix(dataDir, containerdErr, felixCmd)
}

func startFelix(dataDir string, errChan chan error, cmd *exec.Cmd) {
	pamType := "host-local"

	os.Setenv("FELIX_FELIXHOSTNAME", "")
	os.Setenv("FELIX_METADATAADDR", "")
	os.Setenv("FELIX_VXLANVNI", "")
	os.Setenv("CNI_IPAM_TYPE", "")

	// Autoconfigure the IPAM block mode.
	if pamType == "host-local" {
		os.Setenv("USE_POD_CIDR", "true")
	} else {
		os.Setenv("USE_POD_CIDR", "false")
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

	cmd.Stdout = logFile
	cmd.Stderr = errFile

	errChan <- cmd.Run()
}

func startCalico(dataDir string, errChan chan error, cmd *exec.Cmd) {
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
		return "ec2"
	}

	// GCE
	client := &http.Client{}
	req, _ := http.NewRequest("GET", "http://metadata.google.internal/computeMetadata/v1/instance/hostname", nil)
	req.Header.Add("Metadata-Flavor", "Google")
	gceResp, _ := client.Do(req)
	if gceResp != nil {
		return "gce"
	}

	return "bare-metal"
}
