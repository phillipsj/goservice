package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
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
	// TODO:  Config validation, how do we do it in RKE2
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

	ctx, cancel := context.WithTimeout(context.Background(), 5 * time.Minute)
	defer cancel()

	err = createCniConfg(calicoCfg)
    if err != nil {
    	// Do something
	}

	felixErr := make(chan error)
	felixCmd := exec.CommandContext(ctx, "calico-node.exe")
	go startFelix(calicoCfg, felixErr, felixCmd)

	calicoErr := make(chan error)
	calicoCmd := exec.CommandContext(ctx, "calico-node.exe")
	go startCalico(calicoCfg, calicoErr, calicoCmd)
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

	// Lots of setup that has to occur before starting Calico
    err := generateCalicoNetworks(config)
    if err != nil {
    	errChan <- err
	}

	// TODO: Ensure kubelet has started, if not don't start
	// I guess we wouldn't start calico until after kubelet is running.
	errChan <- cmd.Run()
}
