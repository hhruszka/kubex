package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/spf13/cobra"
	"io"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	exec2 "k8s.io/client-go/util/exec"
	"k8s.io/client-go/util/homedir"
	"os"
	"path/filepath"
	"strings"
)

// App global variables
var (
	config    *rest.Config
	clientset *kubernetes.Clientset
)

// CLI options variables
var (
	kubeconfig string
	namespace  string
	pod        string
	container  string
	debug      bool
	format     string
)

var ExitCodes map[int]string = map[int]string{
	-1:  "Internal app error",
	0:   "Success",
	1:   "General error, unspecified error",
	2:   "Misuse of shell builtins",
	126: "Command cannot execute",
	127: "Command not found",
	128: "Invalid argument to exit",
	//130: "Script terminated by Control-C (SIGINT)",
	255: "Exit status out of range",
	// Signal based exit codes (128+n)
	129: "Fatal error signal 1 (SIGHUP)",
	130: "Fatal error signal 2 (SIGINT)",
	131: "Fatal error signal 3 (SIGQUIT)",
	132: "Fatal error signal 4 (SIGILL)",
	133: "Fatal error signal 5 (SIGTRAP)",
	134: "Fatal error signal 6 (SIGABRT/SIGIOT)",
	135: "Fatal error signal 7 (SIGBUS)",
	136: "Fatal error signal 8 (SIGFPE)",
	137: "Fatal error signal 9 (SIGKILL)",
	138: "Fatal error signal 10 (SIGUSR1)",
	139: "Fatal error signal 11 (SIGSEGV)",
	140: "Fatal error signal 12 (SIGUSR2)",
	141: "Fatal error signal 13 (SIGPIPE)",
	142: "Fatal error signal 14 (SIGALRM)",
	143: "Fatal error signal 15 (SIGTERM)",
	// Add more signal based codes as needed
}

func getExitCode(err error) (int, string) {
	var e exec2.CodeExitError
	if !errors.As(err, &e) {
		return -1, ""
	}
	if _, ok := ExitCodes[e.Code]; !ok {
		return -1, ""
	}
	return e.Code, ExitCodes[e.Code]
}

func getExitCodeDescription(code int) string {
	if _, ok := ExitCodes[code]; !ok {
		return ""
	}
	return ExitCodes[code]
}

func Init() {
	var err error

	config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	clientset, err = kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
}

func getPods(clientset *kubernetes.Clientset, namespace string, options metaV1.ListOptions) ([]corev1.Pod, error) {
	var pods *corev1.PodList
	pods, err := clientset.CoreV1().Pods(namespace).List(context.TODO(), options)
	if err != nil {
		return nil, err
	}
	return pods.Items, nil
}

func getDeployments(clientset *kubernetes.Clientset, namespace string) (*v1.DeploymentList, error) {
	var deployments *v1.DeploymentList
	deployments, err := clientset.AppsV1().Deployments(namespace).List(context.TODO(), metaV1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return deployments, nil
}

func getStatefulSets(clientset *kubernetes.Clientset, namespace string) (*v1.StatefulSetList, error) {
	var statefulSets *v1.StatefulSetList
	statefulSets, err := clientset.AppsV1().StatefulSets(namespace).List(context.TODO(), metaV1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return statefulSets, nil
}

// mapToLabelSelector converts a map of key-value pairs to a Kubernetes label selector string.
func mapToLabelSelector(labels map[string]string) string {
	var selectorParts []string
	for key, value := range labels {
		selectorParts = append(selectorParts, fmt.Sprintf("%s=%s", key, value))
	}
	return strings.Join(selectorParts, ",")
}

func getUniquePods(clientset *kubernetes.Clientset, namespace string) (int, []corev1.Pod, error) {
	var uniquePods []corev1.Pod

	var deploymentPods map[string]int = make(map[string]int)
	deployments, err := getDeployments(clientset, namespace)
	if err != nil {
		return 0, nil, err
	}

	for _, deployment := range deployments.Items {
		// to find all pods that are part of a given deployment we need to use deployment.Spec.Selector.MatchLabels
		// from the deployment. This is essential.
		options := metaV1.ListOptions{LabelSelector: mapToLabelSelector(deployment.Spec.Selector.MatchLabels)}
		pods, err := getPods(clientset, namespace, options)
		if err != nil {
			continue
		}
		// we are interested only in one instance of a pod
		if len(pods) > 0 {
			uniquePods = append(uniquePods, pods[0])
		}
		for _, pod := range pods {
			deploymentPods[pod.Name]++
		}
	}
	//log(fmt.Sprintf("[+] Found %d pods in %d deployments\n", len(deploymentPods), len(deployments.Items)))

	var statefulSetsPods map[string]int = make(map[string]int)
	statefulSets, err := getStatefulSets(clientset, namespace)
	if err != nil {
		return 0, nil, err
	}

	for _, statefulSet := range statefulSets.Items {
		// to find all pods that are part of a given deployment we need to use statefulSet.Spec.Selector.MatchLabels
		// from the deployment. This is essential.
		options := metaV1.ListOptions{LabelSelector: mapToLabelSelector(statefulSet.Spec.Selector.MatchLabels)}
		pods, err := getPods(clientset, namespace, options)
		if err != nil {
			continue
		}
		// we are interested only in one instance of a pod
		//podCount += len(pods)
		if len(pods) > 0 {
			uniquePods = append(uniquePods, pods[0])
		}
		for _, pod := range pods {
			statefulSetsPods[pod.Name]++
		}
	}
	//log(fmt.Sprintf("[+] Found %d pods in %d statefulsets\n", len(statefulSetsPods), len(statefulSets.Items)))

	podsList, err := clientset.CoreV1().Pods(namespace).List(context.TODO(), metaV1.ListOptions{})
	if err != nil {
		return 0, nil, err
	}
	for _, pod := range podsList.Items {
		if _, ok := deploymentPods[pod.Name]; ok {
			continue
		}
		if _, ok := statefulSetsPods[pod.Name]; ok {
			continue
		}
		uniquePods = append(uniquePods, pod)
	}

	return len(podsList.Items), uniquePods, nil
}

func exec(clientset *kubernetes.Clientset, config *rest.Config, namespace string, podName string, containerName string, cmd []string, stdin io.Reader, stdout io.Writer, stderr io.Writer, tty bool) (int, error) {

	//command := []string{cmd}

	req := clientset.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   cmd,
			Stdin:     stdin != nil,
			Stdout:    stdout != nil,
			Stderr:    stderr != nil,
			TTY:       tty,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		if debug {
			fmt.Printf("[-] Execution failed with error code %d\n", err)
		}
		return -1, err
	}

	err = executor.StreamWithContext(context.TODO(), remotecommand.StreamOptions{
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
		Tty:    false,
	})

	//fmt.Println(stdout)
	git
	return 0, nil
}

type ExecutionStatus struct {
	Pod       string   `json:"Pod"`
	Container string   `json:"Container"`
	RetCode   int      `json:"RetCode"`
	Error     []string `json:"Error"`
	Stdout    []string `json:"Stdout"`
	Stderr    []string `json:"Stderr"`
}

type EnumerationStatus struct {
	Stdin     string             `json:"Stdin"`
	Args      []string           `json:"Args"`
	Namespace string             `json:"Namespace"`
	Statuses  []*ExecutionStatus `json:"Statuses"`
}

func NewEnumerationStatus(pipeCommand string, command []string, namespace string) *EnumerationStatus {
	if len(pipeCommand) > 0 {
		pipeCommand = fmt.Sprintf("%s... too long", pipeCommand[:40])
	}
	return &EnumerationStatus{Stdin: pipeCommand, Args: command, Namespace: namespace}
}

func NewExecutionStatus(pod string, container string, retCode int, error string, stdout string, stderr string) *ExecutionStatus {
	return &ExecutionStatus{Pod: pod, Container: container, RetCode: retCode, Error: strings.Split(error, "\n"), Stdout: strings.Split(stdout, "\n"), Stderr: strings.Split(stderr, "\n")}
}

func Exec(clientset *kubernetes.Clientset, config *rest.Config, namespace string, podName string, containerName string, args []string, stdin io.Reader) *ExecutionStatus {
	var stdout, stderr bytes.Buffer
	var errMessage string

	retCode, err := exec(clientset, config, namespace, podName, containerName, args, stdin, &stdout, &stderr, false)
	if err != nil {
		errMessage = err.Error()
	}
	return NewExecutionStatus(podName, containerName, retCode, errMessage, stdout.String(), stderr.String())
}

func run(args []string) error {
	Init()

	//Prepare to capture stdin
	var stdinBuf bytes.Buffer

	if fi, err := os.Stdin.Stat(); err == nil {
		if (fi.Mode() & os.ModeCharDevice) == 0 {
			_, err = io.Copy(&stdinBuf, os.Stdin)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to read stdin: %v\n", err)
				os.Exit(1)
			}
		}
	}

	if stdinBuf.Len() > 0 && len(args) == 0 {
		// no command to pipe to has been providing defaulting to shell
		args = []string{"sh"}
	}

	enumStatus := NewEnumerationStatus(stdinBuf.String(), args, namespace)
	switch {
	case pod != "" && container == "":
		_pod, err := clientset.CoreV1().Pods(namespace).Get(context.TODO(), pod, metaV1.GetOptions{})
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		if _pod.Status.Phase == "Running" {
			for _, _container := range _pod.Spec.Containers {
				// each execution of command will empty stdin therefore
				// we need to preserve it and recreate for each iteration
				streamedCmd := bytes.NewBuffer(stdinBuf.Bytes())
				status := Exec(clientset, config, namespace, _pod.Name, _container.Name, args, streamedCmd)
				enumStatus.Statuses = append(enumStatus.Statuses, status)
			}
		}
	case pod != "" && container != "":
		_pod, err := clientset.CoreV1().Pods(namespace).Get(context.TODO(), pod, metaV1.GetOptions{})
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		if _pod.Status.Phase != "Running" {
			fmt.Printf("Pod %s is not in Running phase\n", pod)
			os.Exit(1)
		}

		status := Exec(clientset, config, namespace, pod, container, args, &stdinBuf)
		enumStatus.Statuses = append(enumStatus.Statuses, status)
	case pod == "" && container == "":
		pods, err := getPods(clientset, namespace, metaV1.ListOptions{})
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		for _, _pod := range pods {
			if _pod.Status.Phase == "Running" {
				for _, _container := range _pod.Spec.Containers {
					// each execution of command will empty stdin therefore
					// we need to preserve it and recreate for each iteration
					streamedCmd := bytes.NewBuffer(stdinBuf.Bytes())
					status := Exec(clientset, config, namespace, _pod.Name, _container.Name, args, streamedCmd)
					enumStatus.Statuses = append(enumStatus.Statuses, status)
				}
			}
		}
	}

	switch format {
	case "json":
		jsonBuff, err := json.MarshalIndent(enumStatus, "", "    ")
		if err != nil {
			return err
		}
		fmt.Println(string((jsonBuff)))
	case "text":
		fmt.Printf("STDIN COMMAND: %s\n", enumStatus.Stdin)
		fmt.Printf("COMMAND: %q\n\n", enumStatus.Args)
		fmt.Printf("Namespace: %s\n", enumStatus.Namespace)
		for _, status := range enumStatus.Statuses {
			fmt.Printf("CONTAINER: %s/%s\n", status.Pod, status.Container)
			fmt.Printf("Returned exit code: %d [%s]\n", status.RetCode, getExitCodeDescription(status.RetCode))
			if strings.Trim(strings.Join(status.Error, "\n"), "\n") != "" {
				fmt.Printf("Returned error: %s\n", strings.Join(status.Error, "\n"))
			}
			fmt.Printf("Standard output:\n%s", strings.Join(status.Stdout, "\n"))
			fmt.Printf("Standard error:\n%s", strings.Join(status.Stderr, "\n"))
			fmt.Println()
		}
	}

	return nil
}

func main() {
	var cmd = &cobra.Command{
		Use:   "k8sexec",
		Short: "k8sexec is a command line application that executes commands in containers",
		Long:  ``,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(args)
		},
	}

	if home := homedir.HomeDir(); home != "" {
		cmd.Flags().StringVarP(&kubeconfig, "kubeconfig", "k", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		cmd.Flags().StringVarP(&kubeconfig, "kubeconfig", "k", "", "absolute path to the kubeconfig file")
	}

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "CNF namespace")
	cmd.Flags().StringVarP(&pod, "pod", "p", "", "a pod name, if not provided then all containers in a namespace will be enumerated.")
	cmd.Flags().StringVarP(&container, "container", "c", "", "a container name")
	cmd.Flags().BoolVarP(&debug, "debug", "d", false, "debug")
	cmd.Flags().StringVarP(&format, "output", "o", "text", "Output format: text, or json")

	// Disable automatic printing of usage when an error occurs
	cmd.SilenceUsage = true

	cmd.Flags().SetInterspersed(false)

	// Custom PreRunE to check for parse errors
	cmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		// This checks for any parse errors
		if err := cmd.ParseFlags(args); err != nil {
			return err
		}
		return nil
	}

	cmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		// When a non-existing option is invoked, print the usage
		if err := c.Usage(); err != nil {
			fmt.Fprintf(os.Stderr, "Error printing usage: %v\n", err)
		}
		// Return the original error to stop execution
		return err
	})

	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
