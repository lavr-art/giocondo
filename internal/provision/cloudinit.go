package provision

import (
	"encoding/base64"
	"fmt"
	"path"

	"gopkg.in/yaml.v3"
)

// CloudInitDir is where user-data puts the installation files; the directory is removed afterwards.
const CloudInitDir = "/root/vpnctl"

// CloudInitLog is the installation log on the node.
const CloudInitLog = "/var/log/vpnctl-install.log"

type cloudConfig struct {
	WriteFiles []cloudFile `yaml:"write_files"`
	RunCmd     [][]string  `yaml:"runcmd"`
}

type cloudFile struct {
	Path        string `yaml:"path"`
	Encoding    string `yaml:"encoding"`
	Content     string `yaml:"content"`
	Permissions string `yaml:"permissions"`
	Owner       string `yaml:"owner"`
}

// CloudInit returns a #cloud-config that on the server's first boot puts files into CloudInitDir,
// runs cmd from that directory (log in CloudInitLog) and removes the directory. The node certificate
// is stored in user-data in the clear: it is visible in the cloud console while the server exists.
func CloudInit(files []File, cmd string) ([]byte, error) {
	cc := cloudConfig{}
	for _, f := range files {
		cc.WriteFiles = append(cc.WriteFiles, cloudFile{
			Path: path.Join(CloudInitDir, f.Path), Encoding: "b64", Owner: "root:root",
			Content: base64.StdEncoding.EncodeToString(f.Data), Permissions: fmt.Sprintf("%#o", f.Mode),
		})
	}
	script := fmt.Sprintf("cd %s && %s >%s 2>&1; status=$?; rm -rf %s; exit $status", CloudInitDir, cmd, CloudInitLog, CloudInitDir)
	cc.RunCmd = [][]string{{"sh", "-c", script}}
	body, err := yaml.Marshal(cc)
	if err != nil {
		return nil, err
	}
	head := "#cloud-config\n" +
		"# vpnctl: node agent installation. Contains the node private key, do not publish this file.\n" +
		"# Installation log on the server: " + CloudInitLog + "\n"
	return append([]byte(head), body...), nil
}
