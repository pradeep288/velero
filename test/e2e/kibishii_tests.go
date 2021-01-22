package e2e

import (
	"fmt"
	"os/exec"
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/pkg/errors"
	"golang.org/x/net/context"
	"k8s.io/client-go/kubernetes"

	veleroexec "github.com/vmware-tanzu/velero/pkg/util/exec"
)

const (
	kibishiiNamespace = "kibishii-workload"
)

func installKibishii(ctx context.Context, namespace string, cloudPlatform string) error {
	// We use kustomize to generate YAML for Kibishii from the checked-in yaml directories
	kibishiiInstallCmd := exec.CommandContext(ctx, "kubectl", "apply", "-n", namespace, "-k",
		"github.com/vmware-tanzu-experiments/distributed-data-generator/kubernetes/yaml/"+cloudPlatform)

	_, _, err := veleroexec.RunCommand(kibishiiInstallCmd)
	if err != nil {
		return errors.Wrap(err, "failed to install kibishii")
	}

	kibishiiSetWaitCmd := exec.CommandContext(ctx, "kubectl", "rollout", "status", "statefulset.apps/kibishii-deployment",
		"-n", namespace, "-w", "--timeout=30m")
	_, _, err = veleroexec.RunCommand(kibishiiSetWaitCmd)

	if err != nil {
		return err
	}

	jumpPadWaitCmd := exec.CommandContext(ctx, "kubectl", "wait", "--for=condition=ready", "-n", namespace, "pod/jump-pad")
	_, _, err = veleroexec.RunCommand(jumpPadWaitCmd)

	return err
}

func generateData(ctx context.Context, namespace string, levels int, filesPerLevel int, dirsPerLevel int, fileSize int,
	blockSize int, passNum int, expectedNodes int) error {
	kibishiiGenerateCmd := exec.CommandContext(ctx, "kubectl", "exec", "-n", namespace, "jump-pad", "--",
		"/usr/local/bin/generate.sh", strconv.Itoa(levels), strconv.Itoa(filesPerLevel), strconv.Itoa(dirsPerLevel), strconv.Itoa(fileSize),
		strconv.Itoa(blockSize), strconv.Itoa(passNum), strconv.Itoa(expectedNodes))
	fmt.Printf("kibishiiGenerateCmd cmd =%v\n", kibishiiGenerateCmd)

	_, _, err := veleroexec.RunCommand(kibishiiGenerateCmd)
	if err != nil {
		return errors.Wrap(err, "failed to generate")
	}

	return nil
}

func verifyData(ctx context.Context, namespace string, levels int, filesPerLevel int, dirsPerLevel int, fileSize int,
	blockSize int, passNum int, expectedNodes int) error {
	kibishiiVerifyCmd := exec.CommandContext(ctx, "kubectl", "exec", "-n", namespace, "jump-pad", "--",
		"/usr/local/bin/verify.sh", strconv.Itoa(levels), strconv.Itoa(filesPerLevel), strconv.Itoa(dirsPerLevel), strconv.Itoa(fileSize),
		strconv.Itoa(blockSize), strconv.Itoa(passNum), strconv.Itoa(expectedNodes))
	fmt.Printf("kibishiiVerifyCmd cmd =%v\n", kibishiiVerifyCmd)

	_, _, err := veleroexec.RunCommand(kibishiiVerifyCmd)
	if err != nil {
		return errors.Wrap(err, "failed to verify")
	}
	return nil
}

// RunKibishiiTests runs kibishii tests on the provider.
func RunKibishiiTests(client *kubernetes.Clientset, providerName, veleroCLI, backupName, restoreName string) error {
	fiveMinTimeout, _ := context.WithTimeout(context.Background(), 5*time.Minute)
	oneHourTimeout, _ := context.WithTimeout(context.Background(), time.Minute*60)

	if err := CreateNamespace(fiveMinTimeout, client, kibishiiNamespace); err != nil {
		errors.Wrapf(err, "Failed to create namespace %s to install Kibishii workload", kibishiiNamespace)
	}

	if err := installKibishii(fiveMinTimeout, kibishiiNamespace, providerName); err != nil {
		errors.Wrap(err, "Failed to install Kibishii workload")
	}

	if err := generateData(oneHourTimeout, kibishiiNamespace, 2, 10, 10, 1024, 1024, 0, 2); err != nil {
		return errors.Wrap(err, "Failed to generate data")
	}

	if err := VeleroBackupNamespace(oneHourTimeout, veleroCLI, backupName, kibishiiNamespace); err != nil {
		return errors.Wrapf(err, "Failed to backup kibishii namespace %s", kibishiiNamespace)
	}

	fmt.Printf("Simulating a disaster by removing namespace %s\n", kibishiiNamespace)
	if err := client.CoreV1().Namespaces().Delete(oneHourTimeout, kibishiiNamespace, metav1.DeleteOptions{}); err != nil {
		return errors.Wrap(err, "Failed to simulate a disaster")
	}

	if err := VeleroRestore(oneHourTimeout, veleroCLI, restoreName, backupName); err != nil {
		return errors.Wrapf(err, "Restore %s failed from backup %s", restoreName, backupName)
	}

	// TODO - check that namespace exists
	fmt.Printf("running kibishii verify\n")
	if err := verifyData(oneHourTimeout, kibishiiNamespace, 2, 10, 10, 1024, 1024, 0, 2); err != nil {
		return errors.Wrap(err, "Failed to verify data generated by kibishii")
	}

	if err := client.CoreV1().Namespaces().Delete(oneHourTimeout, kibishiiNamespace, metav1.DeleteOptions{}); err != nil {
		return errors.Wrapf(err, "Failed to cleanup %s wrokload namespace", kibishiiNamespace)
	}

	// kibishii test completed successfully
	return nil
}
