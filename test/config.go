package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gruntwork-io/terratest/modules/aws"
	"github.com/gruntwork-io/terratest/modules/git"
	"github.com/gruntwork-io/terratest/modules/ssh"
	"github.com/gruntwork-io/terratest/modules/terraform"
)

type TestConfig struct {
	ID                      string
	Owner                   string
	Region                  string
	ExampleDir              string
	TestDir                 string
	KeyPair                 *aws.Ec2Keypair
	SshAgent                ssh.SshAgent
	Rke2Version             string
	RancherVersion          string
	TerraformRcPath         string
	DnsZone                 string
	KubeconfigPath          string
	BackendTerraformOptions *terraform.Options
	TerraformOptions        *terraform.Options
	TfOptions               []*terraform.Options
}

func NewTestConfig(t *testing.T, directory string) *TestConfig {

	id := GetId()
	region := GetRegion()
	owner := "terraform-ci@suse.com"
	dnsZone := os.Getenv("ZONE")

	SetAcmeServer()

	repoRoot, err := filepath.Abs(git.GetRepoRoot(t))
	if err != nil {
		t.Fatalf("Error getting git root directory: %v", err)
	}
	exampleDir := filepath.Join(repoRoot, "examples", directory)
	testDir := filepath.Join(repoRoot, "test", "data", id)
	kubeconfigPath := filepath.Join(testDir, "kubeconfig")

	err = CreateTestDirectories(t, id)
	if err != nil {
		os.RemoveAll(testDir)
		t.Fatalf("Error creating test data directories: %s", err)
	}

	keyPair, err := CreateKeypair(t, region, owner, id)
	if err != nil {
		os.RemoveAll(testDir)
		t.Fatalf("Error creating test key pair: %s", err)
	}

	err = os.WriteFile(filepath.Join(testDir, "id_rsa"), []byte(keyPair.KeyPair.PrivateKey), 0600)
	if err != nil {
		err = aws.DeleteEC2KeyPairE(t, keyPair)
		if err != nil {
			t.Logf("Failed to destroy key pair: %v", err)
		}
		os.RemoveAll(testDir)
		t.Fatalf("Error creating test key pair: %s", err)
	}

	sshAgent := ssh.SshAgentWithKeyPair(t, keyPair.KeyPair)
	t.Logf("Key %s created and added to agent", keyPair.Name)

	backendTerraformOptions, err := CreateObjectStorageBackend(t, id, owner, region)
	tfOptions := []*terraform.Options{backendTerraformOptions}
	if err != nil {
		t.Log("Test failed, tearing down...")
		Teardown(t, testDir, exampleDir, tfOptions, keyPair, sshAgent)
		t.Fatalf("Error creating cluster: %s", err)
	}

	// use oldest RKE2, remember it releases much more than Rancher
	_, _, rke2Version, err := GetRke2Releases()
	if err != nil {
		Teardown(t, testDir, exampleDir, tfOptions, keyPair, sshAgent)
		t.Fatalf("Error getting Rke2 release version: %s", err)
	}

	rancherVersion := os.Getenv("RANCHER_VERSION")
	if rancherVersion == "" {
		// use stable version if not specified
		// using stable prevents problems where the Rancher provider hasn't released to fit the latest Rancher
		_, rancherVersion, _, err = GetRancherReleases()
	}
	if err != nil {
		Teardown(t, testDir, exampleDir, tfOptions, keyPair, sshAgent)
		t.Fatalf("Error getting Rancher release version: %s", err)
	}

	terraformRcPath := filepath.Join(testDir, ".terraformrc")
	err = WriteTerraformRc(t, terraformRcPath)
	if err != nil {
		Teardown(t, testDir, exampleDir, tfOptions, keyPair, sshAgent)
		t.Fatalf("Error writing Terraform RC file: %s", err)
	}

	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{
		// https://pkg.go.dev/github.com/gruntwork-io/terratest/modules/terraform#Options
		TerraformDir: exampleDir,
		// Variables to pass to our Terraform code using -var options
		Vars: map[string]interface{}{
			"identifier":      id,
			"owner":           owner,
			"key_name":        keyPair.Name,
			"key":             keyPair.KeyPair.PublicKey,
			"zone":            dnsZone,
			"rke2_version":    rke2Version,
			"rancher_version": rancherVersion,
			"file_path":       testDir,
		},
		// Environment variables to set when running Terraform
		EnvVars: map[string]string{
			"AWS_DEFAULT_REGION": region,
			"AWS_REGION":         region,
			"TF_DATA_DIR":        testDir,
			"TF_IN_AUTOMATION":   "1",
			"TF_CLI_CONFIG_FILE": terraformRcPath,
		},
		RetryableTerraformErrors: GetRetryableTerraformErrors(),
		NoColor:                  true,
		SshAgent:                 sshAgent,
		BackendConfig: map[string]interface{}{
			"bucket": strings.ToLower(id),
		},
		Reconfigure: true,
		Upgrade:     true,
		Parallelism: 5,
	})
	// we need to prepend the main options because we need to destroy it before the backend
	tfOptions = []*terraform.Options{terraformOptions, backendTerraformOptions}

	return &TestConfig{
		ID:                      id,
		Owner:                   owner,
		Region:                  region,
		ExampleDir:              exampleDir,
		TestDir:                 testDir,
		KeyPair:                 keyPair,
		SshAgent:                *sshAgent,
		BackendTerraformOptions: backendTerraformOptions,
		TerraformOptions:        terraformOptions,
		Rke2Version:             rke2Version,
		RancherVersion:          rancherVersion,
		TerraformRcPath:         terraformRcPath,
		DnsZone:                 dnsZone,
		TfOptions:               tfOptions,
		KubeconfigPath:          kubeconfigPath,
	}
}

func (config *TestConfig) Teardown(t *testing.T) {
	Teardown(t, config.TestDir, config.ExampleDir, config.TfOptions, config.KeyPair, &config.SshAgent)
}

func (config *TestConfig) GetErrorLogs(t *testing.T) {
	GetErrorLogs(t, config.KubeconfigPath)
}

func (config *TestConfig) CheckReady(t *testing.T) {
	CheckReady(t, config.KubeconfigPath)
}

func (config *TestConfig) CheckRunning(t *testing.T) {
	CheckRunning(t, config.KubeconfigPath)
}

func (config *TestConfig) AddVars(vars map[string]interface{}) {
	for key, value := range vars {
		config.TerraformOptions.Vars[key] = value
	}
}
