package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	tokenFile          = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	caCertFile         = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	namespaceFile      = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	outputKubeconfig   = "/host/etc/cni/net.d/wg-cni.kubeconfig"
	wgCNIBinSource     = "/opt/cni/bin/wg-cni"
	wgCNIBinDest       = "/host/opt/cni/bin/wg-cni"
	kubeconfigTemplate = `apiVersion: v1
kind: Config
clusters:
- name: default-cluster
  cluster:
    certificate-authority-data: %s
    server: %s
contexts:
- name: default-context
  context:
    cluster: default-cluster
    namespace: %s
    user: default-user
current-context: default-context
users:
- name: default-user
  user:
    token: %s
`
)

func getEnvOrDie(key string) string {
	val := os.Getenv(key)
	if val == "" {
		fmt.Fprintf(os.Stderr, "Error: required environment variable %s is not set\n", key)
		os.Exit(1)
	}
	return val
}

func installWgCNIBinary() error {
	// Read source binary
	data, err := os.ReadFile(wgCNIBinSource)
	if err != nil {
		return fmt.Errorf("failed to read wg-cni binary from %s: %w", wgCNIBinSource, err)
	}

	// Ensure destination directory exists
	destDir := filepath.Dir(wgCNIBinDest)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Write binary with executable permissions
	if err := os.WriteFile(wgCNIBinDest, data, 0755); err != nil {
		return fmt.Errorf("failed to write wg-cni binary to %s: %w", wgCNIBinDest, err)
	}

	fmt.Printf("Successfully installed wg-cni binary to %s\n", wgCNIBinDest)
	return nil
}

func readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func generateKubeconfig() error {
	// Read the token
	token, err := readFile(tokenFile)
	if err != nil {
		return fmt.Errorf("failed to read token file: %w", err)
	}

	// Read and base64 encode the CA certificate
	caContents, err := readFile(caCertFile)
	if err != nil {
		return fmt.Errorf("failed to read CA cert file: %w", err)
	}
	caBase64 := base64.StdEncoding.EncodeToString([]byte(caContents))

	// Read the namespace from the namespace file
	namespace, err := readFile(namespaceFile)
	if err != nil {
		return fmt.Errorf("failed to read namespace file: %w", err)
	}

	// Get API server endpoint from environment variable
	server := getEnvOrDie("KUBERNETES_APISERVER_ENDPOINT")

	// Generate kubeconfig content
	kubeconfig := fmt.Sprintf(kubeconfigTemplate, caBase64, server, namespace, token)

	// Ensure output directory exists
	outputDir := filepath.Dir(outputKubeconfig)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Write output file with 0600 permissions
	if err := os.WriteFile(outputKubeconfig, []byte(kubeconfig), 0600); err != nil {
		return fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	fmt.Printf("Successfully generated %s\n", outputKubeconfig)
	return nil
}

func watchTokenFile() error {
	// Install wg-cni binary once at the beginning
	if err := installWgCNIBinary(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to install wg-cni binary: %v\n", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	defer watcher.Close()

	tokenDir := filepath.Dir(tokenFile)

	// Watch the serviceaccount directory for any inotify events which indicate token rotation
	if err := watcher.Add(tokenDir); err != nil {
		return fmt.Errorf("failed to watch directory %s: %w", tokenDir, err)
	}

	fmt.Printf("Watching directory %s for token changes\n", tokenDir)
	fmt.Printf("Output kubeconfig will be written to %s\n", outputKubeconfig)

	// Initial generation
	if err := generateKubeconfig(); err != nil {
		fmt.Fprintf(os.Stderr, "Initial kubeconfig generation failed: %v\n", err)
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return fmt.Errorf("watcher events channel closed")
			}

			// Only react to CREATE events on ..data, which is the final atomic swap in token rotation
			if filepath.Base(event.Name) == "..data" && event.Has(fsnotify.Create) {
				fmt.Printf("Detected token rotation (..data CREATE), regenerating kubeconfig...\n")
				// Small delay to allow the file content to settle
				time.Sleep(50 * time.Millisecond)
				if err := generateKubeconfig(); err != nil {
					fmt.Fprintf(os.Stderr, "Error generating kubeconfig: %v\n", err)
				}
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return fmt.Errorf("watcher errors channel closed")
			}
			fmt.Fprintf(os.Stderr, "Watcher error: %v\n", err)
		}
	}
}

func main() {
	if err := watchTokenFile(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
