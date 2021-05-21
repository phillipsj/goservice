package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/Microsoft/hcsshim/hcn"
)

type FelixConfig struct {
	metadataaddr    string
	vxlanvni        string
	macPrefix       string
	logSeverityFile string
	logSeveritySys  string
}

type CniConfig struct {
	binDir       string
	confDir      string
	ipamType     string
	confFileName string
}

type CalicoConfig struct {
	hostname              string
	kubeNetwork           string
	kubeConfig            string
	networkingBackend     string
	serviceCidr           string
	dnsServers            string
	dnsSearch             string
	datastoreType         string
	nodeNameFile          string
	platform              string
	startUpValidIpTimeout int
	ip                    string
	logDir                string
	felix                 FelixConfig
	cni                   CniConfig
}

func main() {
	hostname, err := os.Hostname()
	if err != nil {
		// What to do?
	}

	// TODO:  Some of this can be pulled from the worker config, need to figure that out
	calicoCfg := CalicoConfig{
		hostname:              hostname,
		kubeNetwork:           "Calico.*",
		serviceCidr:           "10.42.0.0/16",
		dnsServers:            "10.43.0.10",
		dnsSearch:             "svc.cluster.local",
		datastoreType:         "kubernetes",
		networkingBackend:     "vxlan",
		platform:              getPlatformType(),
		startUpValidIpTimeout: 90,
		logDir:                "",
		ip:                    "autodetect",
		felix: FelixConfig{
			metadataaddr:    "none",
			vxlanvni:        "4096",
			macPrefix:       "0E-2A",
			logSeverityFile: "none",
			logSeveritySys:  "none",
		},
		cni: CniConfig{
			binDir:   filepath.Join("c:", "opt", "cni", "bin"),
			confDir:  filepath.Join("c:", "etc", "cni", "net.d"),
			ipamType: "calico-ipam",
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

func generateGeneralCalicoEnvs(config CalicoConfig) []string {
	return []string{
		fmt.Sprintf("KUBE_NETWORK=%s", config.kubeNetwork),
		fmt.Sprintf("KUBECONFIG=%s", config.kubeConfig),
		fmt.Sprintf("K8S_SERVICE_CIDR=%s", config.serviceCidr),

		fmt.Sprintf("CALICO_NETWORKING_BACKEND=%s", config.networkingBackend),
		fmt.Sprintf("CALICO_DATASTORE_TYPE=%s", config.datastoreType),
		fmt.Sprintf("CALICO_K8S_NODE_REF=%s", config.hostname),
		fmt.Sprintf("CALICO_LOG_DIR=%s", config.logDir),

		fmt.Sprintf("DNS_NAME_SERVERS=%s", config.dnsServers),
		fmt.Sprintf("DNS_SEARCH=%s", config.dnsSearch),

		fmt.Sprintf("ETCD_ENDPOINTS=%s", config.felix.vxlanvni),
		fmt.Sprintf("ETCD_KEY_FILE=%s", config.felix.metadataaddr),
		fmt.Sprintf("ETCD_CERT_FILE=%s", config.felix.vxlanvni),
		fmt.Sprintf("ETCD_CA_CERT_FILE=%s", config.felix.metadataaddr),

		fmt.Sprintf("CNI_BIN_DIR=%s", config.cni.binDir),
		fmt.Sprintf("CNI_CONF_DIR=%s", config.cni.confDir),
		fmt.Sprintf("CNI_CONF_FILENAME=%s", config.cni.confFileName),
		fmt.Sprintf("CNI_IPAM_TYPE=%s", config.cni.ipamType),

		fmt.Sprintf("FELIX_LOGSEVERITYFILE=%s", config.felix.logSeverityFile),
		fmt.Sprintf("FELIX_LOGSEVERITYSYS=%s", config.felix.logSeveritySys),

		fmt.Sprintf("STARTUP_VALID_IP_TIMEOUT=%s", autoConfigureIpam(config.cni.ipamType)),
		fmt.Sprintf("IP=%s", autoConfigureIpam(config.cni.ipamType)),
		fmt.Sprintf("USE_POD_CIDR=%s", autoConfigureIpam(config.cni.ipamType)),
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

func startFelix(config CalicoConfig, errChan chan error, cmd *exec.Cmd) {

	specificEnvs := []string{
		fmt.Sprintf("FELIX_FELIXHOSTNAME=%s", config.hostname),
		fmt.Sprintf("FELIX_VXLANVNI=%s", config.felix.vxlanvni),
		fmt.Sprintf("FELIX_METADATAADDR=%s", config.felix.metadataaddr),
	}

	args := []string{
		"-felix",
	}
	cmd.Args = append(cmd.Args, args...)
	cmd.Env = append(generateGeneralCalicoEnvs(config), specificEnvs...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	errChan <- cmd.Run()
}

func startCalico(config CalicoConfig, errChan chan error, cmd *exec.Cmd) {
	specificEnvs := []string{
		fmt.Sprintf("CALICO_NODENAME_FILE=%s", config.nodeNameFile),
	}

	args := []string{
		"-startup",
	}
	cmd.Args = append(cmd.Args, args...)
	cmd.Env = append(generateGeneralCalicoEnvs(config), specificEnvs...)
	cmd.Stdout = nil
	cmd.Stderr = nil

	errChan <- cmd.Run()
}

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

	// TODO: Line 84 in node-service.ps1
	fmt.Println(good)
	return nil
}
